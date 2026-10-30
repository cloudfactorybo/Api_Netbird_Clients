package netbird

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

var fixtureNow = time.Date(2026, 10, 30, 15, 10, 0, 0, time.UTC)

func loadFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParse(t *testing.T) {
	st, err := Parse(loadFixture(t), fixtureNow, 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if st.Host.NetbirdIP != "100.64.0.1" || st.Host.NetbirdCIDR != "100.64.0.1/16" {
		t.Errorf("IP local: %q / %q", st.Host.NetbirdIP, st.Host.NetbirdCIDR)
	}
	want := Counts{Total: 3, Connected: 2, Disconnected: 1, P2P: 1, Relayed: 1, HandshakeStale: 1}
	if st.Counts != want {
		t.Errorf("counts = %+v, want %+v", st.Counts, want)
	}
	if st.Health != "degraded" {
		t.Errorf("health = %s (%v), want degraded por el handshake vencido", st.Health, st.HealthReason)
	}

	elk, err := st.FindPeer("100.64.0.10")
	if err != nil {
		t.Fatal(err)
	}
	if !elk.P2P || elk.RelayAddress != "" || elk.Name != "cliente-elk" {
		t.Errorf("peer P2P mal interpretado: %+v", elk)
	}
	if elk.LatencyMs < 14.19 || elk.LatencyMs > 14.2 {
		t.Errorf("latency_ms = %v, want ~14.19", elk.LatencyMs)
	}
	if elk.HandshakeAgeSeconds == nil || *elk.HandshakeAgeSeconds != 60 || elk.HandshakeStale {
		t.Errorf("handshake P2P: age=%v stale=%v", elk.HandshakeAgeSeconds, elk.HandshakeStale)
	}

	fw, _ := st.FindPeer("router-cliente")
	if fw.P2P || fw.RelayAddress != "rels://netbird.example.com:443" || !fw.HandshakeStale {
		t.Errorf("peer relayed mal interpretado: %+v", fw)
	}

	idle, _ := st.FindPeer("laptop-apagada.netbird.selfhosted")
	if idle.Connected || idle.LastHandshake != nil || idle.ConnectionType != "" || idle.Networks == nil {
		t.Errorf("peer idle mal interpretado: %+v", idle)
	}
	if _, err := st.FindPeer("10.9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindPeer inexistente: %v", err)
	}
}

func TestParseHealth(t *testing.T) {
	st, _ := Parse([]byte(`{"management":{"connected":false},"signal":{"connected":true}}`), fixtureNow, time.Minute)
	if st.Health != "unhealthy" {
		t.Errorf("sin management debe ser unhealthy, fue %s", st.Health)
	}
	if _, err := Parse([]byte("no es json"), fixtureNow, time.Minute); err == nil {
		t.Error("JSON inválido debería fallar")
	}
}

func TestSourceCacheAndFatal(t *testing.T) {
	raw := loadFixture(t)
	calls := 0
	fail := false
	run := func(context.Context) ([]byte, error) {
		calls++
		if fail {
			return nil, errors.New("socket caído")
		}
		return raw, nil
	}
	var fatal error
	src := NewSource(run, time.Hour, 3*time.Minute, 2, func(err error) { fatal = err })

	for i := 0; i < 3; i++ {
		if _, _, err := src.Get(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("el caché debía evitar llamadas: %d", calls)
	}

	src.ttl = 0
	fail = true
	src.Get(context.Background())
	if fatal != nil {
		t.Error("un solo fallo no debe ser fatal")
	}
	src.Get(context.Background())
	if fatal == nil {
		t.Error("dos fallos seguidos debían llamar onFatal")
	}
}
