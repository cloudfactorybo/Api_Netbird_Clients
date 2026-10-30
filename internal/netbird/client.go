// Package netbird obtiene el estado del cliente NetBird local ejecutando
// `netbird status --json` contra el socket del daemon.
package netbird

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Runner ejecuta el CLI y devuelve su salida JSON. Se reemplaza en los tests.
type Runner func(ctx context.Context) ([]byte, error)

// CLIRunner ejecuta `<bin> status --json --daemon-addr <addr>`.
// El CLI reintenta para siempre si el daemon no responde, por eso el timeout.
func CLIRunner(bin, daemonAddr string, timeout time.Duration) Runner {
	return func(ctx context.Context) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "status", "--json", "--daemon-addr", daemonAddr)
		cmd.Env = []string{"HOME=/tmp", "PATH=/usr/local/bin:/usr/bin:/bin"}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("netbird status no respondió en %s (¿daemon detenido o socket inaccesible?)", timeout)
		}
		if err != nil {
			return nil, fmt.Errorf("netbird status: %w: %s", err, lastLine(stderr.String()))
		}
		return out, nil
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := lines[len(lines)-1]
	if len(l) > 300 {
		l = l[:300]
	}
	return l
}

// Parse convierte la salida JSON del CLI en Status.
func Parse(raw []byte, now time.Time, staleAfter time.Duration) (*Status, error) {
	var r rawStatus
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("JSON de netbird inválido: %w", err)
	}
	s := &Status{
		Timestamp: now.UTC(),
		Host: Host{
			FQDN:            r.FQDN,
			NetbirdIP:       stripMask(r.IP),
			NetbirdCIDR:     r.IP,
			PublicKey:       r.PubKey,
			KernelInterface: r.KernelInterface,
			WireguardPort:   r.WgPort,
			LazyConnection:  r.LazyConnection,
			Profile:         r.ProfileName,
			Networks:        nonNil(r.Networks),
		},
		Version:      Version{CLI: r.CliVersion, Daemon: r.DaemonVersion},
		DaemonStatus: r.DaemonStatus,
		Management:   service(r.Management),
		Signal:       service(r.Signal),
		Relays:       Relays{Total: r.Relays.Total, Available: r.Relays.Available, Details: []Relay{}},
		Peers:        make([]Peer, 0, len(r.Peers.Details)),
	}
	for _, rl := range r.Relays.Details {
		s.Relays.Details = append(s.Relays.Details, Relay{URI: rl.URI, Available: rl.Available, Transport: rl.Transport, Error: rl.Error})
	}

	for _, rp := range r.Peers.Details {
		p := Peer{
			FQDN:              rp.FQDN,
			Name:              strings.SplitN(rp.FQDN, ".", 2)[0],
			IP:                stripMask(rp.IP),
			PublicKey:         rp.PubKey,
			Status:            rp.Status,
			Connected:         strings.EqualFold(rp.Status, "Connected"),
			RxBytes:           rp.TransferReceived,
			TxBytes:           rp.TransferSent,
			QuantumResistance: rp.Rosenpass,
			Networks:          nonNil(rp.Networks),
			NetworksCount:     len(rp.Networks),
			LastStatusUpdate:  timePtr(rp.LastStatusUpdate),
		}
		if p.Connected {
			p.ConnectionType = rp.ConnType
			p.P2P = strings.EqualFold(rp.ConnType, "P2P")
			if !p.P2P {
				p.RelayAddress = rp.RelayAddress
			}
			p.ICE = ICE{
				LocalType:      rp.IceCandidateType.Local,
				RemoteType:     rp.IceCandidateType.Remote,
				LocalEndpoint:  rp.IceCandidateEndpoint.Local,
				RemoteEndpoint: rp.IceCandidateEndpoint.Remote,
			}
			p.LatencyMs = float64(rp.Latency) / float64(time.Millisecond)
			if hs := timePtr(rp.LastWireguardHandshake); hs != nil {
				p.LastHandshake = hs
				age := now.Sub(*hs).Seconds()
				p.HandshakeAgeSeconds = &age
				// WireGuard renueva el handshake cada 2 min mientras hay tráfico:
				// uno más viejo indica un túnel que dejó de responder.
				p.HandshakeStale = now.Sub(*hs) > staleAfter
			}
		}
		s.Peers = append(s.Peers, p)
	}
	sort.Slice(s.Peers, func(i, j int) bool { return s.Peers[i].FQDN < s.Peers[j].FQDN })

	s.Counts = countPeers(s.Peers)
	s.Health, s.HealthReason = health(s)
	return s, nil
}

func countPeers(peers []Peer) Counts {
	c := Counts{Total: len(peers)}
	for _, p := range peers {
		if !p.Connected {
			c.Disconnected++
			continue
		}
		c.Connected++
		if p.P2P {
			c.P2P++
		} else {
			c.Relayed++
		}
		if p.HandshakeStale {
			c.HandshakeStale++
		}
	}
	return c
}

func health(s *Status) (string, []string) {
	var reasons []string
	if !s.Management.Connected {
		reasons = append(reasons, "management desconectado")
	}
	if !s.Signal.Connected {
		reasons = append(reasons, "signal desconectado")
	}
	if len(reasons) > 0 {
		return "unhealthy", reasons
	}
	if s.Relays.Total > 0 && s.Relays.Available == 0 {
		reasons = append(reasons, "ningún relay disponible")
	}
	if s.Counts.HandshakeStale > 0 {
		reasons = append(reasons, fmt.Sprintf("%d peers con handshake vencido", s.Counts.HandshakeStale))
	}
	if len(reasons) > 0 {
		return "degraded", reasons
	}
	return "healthy", nil
}

func service(r rawService) Service {
	return Service{Connected: r.Connected, URL: r.URL, Error: r.Error}
}

func stripMask(ip string) string { return strings.SplitN(ip, "/", 2)[0] }

func timePtr(t time.Time) *time.Time {
	if t.IsZero() || t.Year() < 2000 {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Source guarda el último estado durante CacheTTL. Las consultas simultáneas
// esperan a la misma ejecución del CLI en vez de lanzar una cada una.
type Source struct {
	run        Runner
	ttl        time.Duration
	staleAfter time.Duration
	maxFails   int
	onFatal    func(error)

	mu       sync.Mutex
	status   *Status
	raw      []byte
	at       time.Time
	err      error
	failures int
}

func NewSource(run Runner, ttl, staleAfter time.Duration, maxFails int, onFatal func(error)) *Source {
	return &Source{run: run, ttl: ttl, staleAfter: staleAfter, maxFails: maxFails, onFatal: onFatal}
}

// Get devuelve el estado (cacheado si es reciente) y la salida cruda del CLI.
func (s *Source) Get(ctx context.Context) (*Status, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < s.ttl {
		return s.status, s.raw, s.err
	}
	raw, err := s.run(ctx)
	var st *Status
	if err == nil {
		st, err = Parse(raw, time.Now(), s.staleAfter)
	}
	s.at = time.Now()
	if err != nil {
		s.status, s.raw, s.err = nil, nil, err
		s.failures++
		if s.maxFails > 0 && s.failures >= s.maxFails && s.onFatal != nil {
			s.onFatal(fmt.Errorf("%d fallos seguidos consultando el daemon: %w", s.failures, err))
		}
		return nil, nil, err
	}
	s.status, s.raw, s.err, s.failures = st, raw, nil, 0
	return st, raw, nil
}

// ErrNotFound se devuelve cuando un peer pedido no existe.
var ErrNotFound = errors.New("peer no encontrado")

// FindPeer busca por IP de NetBird, FQDN o nombre corto.
func (s *Status) FindPeer(key string) (*Peer, error) {
	for i := range s.Peers {
		p := &s.Peers[i]
		if p.IP == key || strings.EqualFold(p.FQDN, key) || strings.EqualFold(p.Name, key) {
			return p, nil
		}
	}
	return nil, ErrNotFound
}
