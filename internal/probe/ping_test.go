package probe

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	r := Result{Sent: 4}
	summarize(&r, []float64{10, 20, 30})
	if !r.Reachable || r.Received != 3 || r.LossRatio != 0.25 {
		t.Errorf("conteos: %+v", r)
	}
	if r.RTTMinMs != 10 || r.RTTMaxMs != 30 || r.RTTAvgMs != 20 || math.Abs(r.JitterMs-8.165) > 0.001 {
		t.Errorf("estadísticas: %+v", r)
	}

	down := Result{Sent: 3}
	summarize(&down, nil)
	if down.Reachable || down.LossRatio != 1 || down.Error == "" {
		t.Errorf("peer sin respuesta: %+v", down)
	}
}

// TestPingLoopback necesita CAP_NET_RAW o net.ipv4.ping_group_range; si no hay, se omite.
func TestPingLoopback(t *testing.T) {
	conn, _, err := listen()
	if err != nil {
		t.Skip("sin permiso para sockets ICMP:", err)
	}
	conn.Close()
	p := New(Options{Count: 2, Interval: 50 * time.Millisecond, Timeout: time.Second})
	res, err := p.Ping(context.Background(), []string{"127.0.0.1", "no-es-ip"})
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Reachable || res[0].Received != 2 {
		t.Errorf("loopback: %+v", res[0])
	}
	if res[1].Reachable || res[1].Error == "" {
		t.Errorf("IP inválida: %+v", res[1])
	}
}
