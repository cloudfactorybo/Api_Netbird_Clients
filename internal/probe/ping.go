// Package probe mide RTT y pérdida con ICMP echo dentro del proceso
// (golang.org/x/net/icmp), sin depender del binario `ping`.
package probe

import (
	"context"
	"errors"
	"math"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type Result struct {
	IP        string  `json:"ip"`
	Sent      int     `json:"packets_sent"`
	Received  int     `json:"packets_received"`
	LossRatio float64 `json:"loss_ratio"`
	Reachable bool    `json:"reachable"`
	RTTMinMs  float64 `json:"rtt_min_ms"`
	RTTAvgMs  float64 `json:"rtt_avg_ms"`
	RTTMaxMs  float64 `json:"rtt_max_ms"`
	JitterMs  float64 `json:"jitter_ms"` // desviación estándar del RTT
	Error     string  `json:"error,omitempty"`
}

type Options struct {
	Count    int
	Interval time.Duration
	Timeout  time.Duration // espera tras el último envío
}

// Pinger envía todos los echo por un único socket y empareja las respuestas
// por secuencia y origen, así una ronda a 60 peers usa un socket y dos goroutines.
type Pinger struct {
	opts Options
	id   int
}

func New(opts Options) *Pinger {
	return &Pinger{opts: opts, id: os.Getpid() & 0xffff}
}

type sent struct {
	target int
	at     time.Time
}

// Ping mide todas las IPs en paralelo. Usa un socket raw (CAP_NET_RAW) y, si no
// hay permiso, el socket ICMP sin privilegios (net.ipv4.ping_group_range).
func (p *Pinger) Ping(ctx context.Context, ips []string) ([]Result, error) {
	results := make([]Result, len(ips))
	targets := make([]*net.IPAddr, len(ips))
	byIP := map[string]int{}
	for i, s := range ips {
		results[i] = Result{IP: s}
		ip := net.ParseIP(s).To4()
		if ip == nil {
			results[i].Error = "IP inválida o no IPv4"
			continue
		}
		targets[i] = &net.IPAddr{IP: ip}
		byIP[ip.String()] = i
	}

	conn, network, err := listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	privileged := network == "ip4:icmp"

	rtts := make([][]float64, len(ips))
	pending := map[int]sent{}
	var mu sync.Mutex

	// Lector: corre hasta el deadline del socket.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			now := time.Now()
			msg, err := icmp.ParseMessage(1, buf[:n])
			if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
				continue
			}
			echo, ok := msg.Body.(*icmp.Echo)
			// Con el socket sin privilegios el kernel reescribe el ID: solo se valida en raw.
			if !ok || (privileged && echo.ID != p.id) {
				continue
			}
			src := addrIP(peer)
			mu.Lock()
			s, ok := pending[echo.Seq]
			if ok && targets[s.target] != nil && targets[s.target].IP.Equal(src) {
				delete(pending, echo.Seq)
				rtts[s.target] = append(rtts[s.target], float64(now.Sub(s.at))/float64(time.Millisecond))
			}
			mu.Unlock()
		}
	}()

	seq := 0
	for round := 0; round < p.opts.Count; round++ {
		if round > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(p.opts.Interval):
			}
		}
		if ctx.Err() != nil {
			break
		}
		for i, t := range targets {
			if t == nil {
				continue
			}
			seq = (seq + 1) & 0xffff
			b, _ := (&icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Body: &icmp.Echo{ID: p.id, Seq: seq, Data: []byte("api-netbird-clients")},
			}).Marshal(nil)
			var dst net.Addr = t
			if !privileged {
				dst = &net.UDPAddr{IP: t.IP}
			}
			mu.Lock()
			pending[seq] = sent{target: i, at: time.Now()}
			mu.Unlock()
			if _, err := conn.WriteTo(b, dst); err != nil {
				results[i].Error = err.Error()
			}
			results[i].Sent++
		}
	}
	// Espera respuestas hasta el timeout, o menos si ya llegaron todas.
	deadline := time.Now().Add(p.opts.Timeout)
	_ = conn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		mu.Lock()
		left := len(pending)
		mu.Unlock()
		if left == 0 {
			_ = conn.SetReadDeadline(time.Now())
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = conn.SetReadDeadline(time.Now())
	<-done

	for i := range results {
		summarize(&results[i], rtts[i])
	}
	return results, nil
}

func listen() (*icmp.PacketConn, string, error) {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err == nil {
		return conn, "ip4:icmp", nil
	}
	conn, err2 := icmp.ListenPacket("udp4", "0.0.0.0")
	if err2 == nil {
		return conn, "udp4", nil
	}
	return nil, "", errors.Join(errors.New("no se pudo abrir un socket ICMP (falta CAP_NET_RAW o net.ipv4.ping_group_range)"), err, err2)
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPAddr:
		return v.IP
	case *net.UDPAddr:
		return v.IP
	}
	return nil
}

func summarize(r *Result, rtts []float64) {
	r.Received = len(rtts)
	if r.Sent > 0 {
		r.LossRatio = float64(r.Sent-r.Received) / float64(r.Sent)
	}
	r.Reachable = r.Received > 0
	if !r.Reachable {
		if r.Error == "" && r.Sent > 0 {
			r.Error = "sin respuesta (peer caído o ICMP bloqueado por la política)"
		}
		return
	}
	r.RTTMinMs, r.RTTMaxMs = rtts[0], rtts[0]
	var sum float64
	for _, v := range rtts {
		sum += v
		r.RTTMinMs = math.Min(r.RTTMinMs, v)
		r.RTTMaxMs = math.Max(r.RTTMaxMs, v)
	}
	r.RTTAvgMs = sum / float64(len(rtts))
	var sq float64
	for _, v := range rtts {
		sq += (v - r.RTTAvgMs) * (v - r.RTTAvgMs)
	}
	r.JitterMs = math.Sqrt(sq / float64(len(rtts)))
	r.Error = ""
}
