package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/netbird"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/probe"
)

const token = "token-de-prueba-123456"

type fakeSource struct {
	st  *netbird.Status
	raw []byte
	err error
}

func (f fakeSource) Get(context.Context) (*netbird.Status, []byte, error) { return f.st, f.raw, f.err }

func newTestServer(t *testing.T, err error) *httptest.Server {
	t.Helper()
	raw, rerr := os.ReadFile("../netbird/testdata/status.json")
	if rerr != nil {
		t.Fatal(rerr)
	}
	st, perr := netbird.Parse(raw, time.Date(2026, 10, 30, 15, 10, 0, 0, time.UTC), 3*time.Minute)
	if perr != nil {
		t.Fatal(perr)
	}
	ping := func(_ context.Context, ips []string) ([]probe.Result, error) {
		out := make([]probe.Result, len(ips))
		for i, ip := range ips {
			out[i] = probe.Result{IP: ip, Sent: 3, Received: 3, Reachable: true, RTTMinMs: 10, RTTAvgMs: 12, RTTMaxMs: 15, JitterMs: 2}
		}
		return out, nil
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(fakeSource{st: st, raw: raw, err: err}, ping, time.Minute, token, "test", log)
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestAuth(t *testing.T) {
	ts := newTestServer(t, nil)
	for _, c := range []struct {
		name string
		hdr  []string
		code int
	}{
		{"sin token", nil, 401},
		{"token malo", []string{"Authorization", "Bearer otro-token-cualquiera"}, 401},
		{"bearer", []string{"Authorization", "Bearer " + token}, 200},
		{"x-api-key", []string{"X-API-Key", token}, 200},
	} {
		if resp, _ := get(t, ts, "/api/v1/status", c.hdr...); resp.StatusCode != c.code {
			t.Errorf("%s: código %d, want %d", c.name, resp.StatusCode, c.code)
		}
	}
	if resp, _ := get(t, ts, "/healthz"); resp.StatusCode != 200 {
		t.Errorf("/healthz no debe pedir token: %d", resp.StatusCode)
	}
}

func TestPeersFilterAndDetail(t *testing.T) {
	ts := newTestServer(t, nil)
	_, body := get(t, ts, "/api/v1/peers?status=connected&type=p2p", "X-API-Key", token)
	var out struct {
		Peers []netbird.Peer `json:"peers"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Peers) != 1 || out.Peers[0].Name != "cliente-elk" {
		t.Errorf("filtro p2p devolvió %+v", out.Peers)
	}

	resp, body := get(t, ts, "/api/v1/peers/cliente-elk", "X-API-Key", token)
	if resp.StatusCode != 200 || !strings.Contains(body, `"rtt_avg_ms": 12`) {
		t.Errorf("detalle sin ping: %d %s", resp.StatusCode, body)
	}
	if resp, _ := get(t, ts, "/api/v1/peers/no-existe", "X-API-Key", token); resp.StatusCode != 404 {
		t.Errorf("peer inexistente: %d", resp.StatusCode)
	}
}

func TestMetrics(t *testing.T) {
	ts := newTestServer(t, nil)
	resp, body := get(t, ts, "/metrics", "X-API-Key", token)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		"netbird_up 1",
		`netbird_peers{state="p2p"} 1`,
		`netbird_peers{state="handshake_stale"} 1`,
		`netbird_peer_p2p{peer="cliente-elk",ip="100.64.0.10"} 1`,
		`netbird_peer_p2p{peer="router-cliente",ip="100.64.0.20"} 0`,
		`netbird_peer_info{peer="router-cliente",fqdn="router-cliente.netbird.selfhosted",ip="100.64.0.20",status="Connected",connection_type="Relayed",relay="rels://netbird.example.com:443"`,
		`netbird_peer_receive_bytes_total{peer="cliente-elk",ip="100.64.0.10"} 1.99443302e+08`,
		`netbird_peer_ping_rtt_seconds{stat="avg",peer="cliente-elk",ip="100.64.0.10"} 0.012`,
		`netbird_relay_available{uri="rels://relay.example.com:443",transport="quic"} 1`,
		"# TYPE netbird_peer_transmit_bytes_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("falta en /metrics: %s", want)
		}
	}
	// El peer idle no publica latencia ni bytes.
	if strings.Contains(body, `netbird_peer_latency_seconds{peer="laptop-apagada"`) {
		t.Error("peer desconectado no debe tener latencia")
	}
}

func TestDaemonDown(t *testing.T) {
	ts := newTestServer(t, errors.New("socket caído"))
	if resp, _ := get(t, ts, "/healthz"); resp.StatusCode != 503 {
		t.Errorf("/healthz con daemon caído: %d", resp.StatusCode)
	}
	resp, body := get(t, ts, "/metrics", "X-API-Key", token)
	if resp.StatusCode != 200 || !strings.Contains(body, "netbird_up 0") {
		t.Errorf("metrics con daemon caído: %d %s", resp.StatusCode, body)
	}
}

func TestEscape(t *testing.T) {
	if got := escape("a\"b\\c\nd"); got != `a\"b\\c\nd` {
		t.Errorf("escape = %s", got)
	}
}
