package api

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/netbird"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/probe"
)

// metrics escribe el formato de texto de Prometheus (exposition format 0.0.4),
// listo para la integración Prometheus de Elastic Agent.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	st, _, err := s.src.Get(r.Context())
	m := &metricWriter{w: w}
	m.family("netbird_up", "gauge", "1 si el daemon NetBird respondió a esta consulta.")
	if err != nil {
		m.sample("netbird_up", nil, 0)
		return
	}
	m.sample("netbird_up", nil, 1)
	var pings map[string]probe.Result
	if s.ping != nil {
		pings, _ = s.pingResults(r.Context(), st)
	}
	writeMetrics(m, st, pings, s.version)
}

func writeMetrics(m *metricWriter, st *netbird.Status, pings map[string]probe.Result, version string) {
	m.family("netbird_info", "gauge", "Versiones y datos del cliente local (valor siempre 1).")
	m.sample("netbird_info", labels{"fqdn", st.Host.FQDN, "netbird_ip", st.Host.NetbirdIP,
		"cli_version", st.Version.CLI, "daemon_version", st.Version.Daemon, "api_version", version}, 1)

	m.family("netbird_health", "gauge", "Salud del cliente: 2=healthy, 1=degraded, 0=unhealthy.")
	m.sample("netbird_health", nil, map[string]float64{"healthy": 2, "degraded": 1}[st.Health])

	m.family("netbird_management_connected", "gauge", "1 si el cliente está conectado al management.")
	m.sample("netbird_management_connected", labels{"url", st.Management.URL}, b2f(st.Management.Connected))
	m.family("netbird_signal_connected", "gauge", "1 si el cliente está conectado a signal.")
	m.sample("netbird_signal_connected", labels{"url", st.Signal.URL}, b2f(st.Signal.Connected))

	m.family("netbird_relay_available", "gauge", "1 si el relay está disponible para este cliente.")
	for _, rl := range st.Relays.Details {
		m.sample("netbird_relay_available", labels{"uri", rl.URI, "transport", rl.Transport}, b2f(rl.Available))
	}

	m.family("netbird_peers", "gauge", "Cantidad de peers por estado.")
	for _, kv := range []struct {
		state string
		n     int
	}{{"known", st.Counts.Total}, {"connected", st.Counts.Connected}, {"disconnected", st.Counts.Disconnected},
		{"p2p", st.Counts.P2P}, {"relayed", st.Counts.Relayed}, {"handshake_stale", st.Counts.HandshakeStale}} {
		m.sample("netbird_peers", labels{"state", kv.state}, float64(kv.n))
	}

	peers := append([]netbird.Peer(nil), st.Peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].FQDN < peers[j].FQDN })

	// connection_type/relay/ICE van solo en la métrica *_info para que un cambio
	// P2P <-> relay no cree series nuevas en las demás métricas.
	m.family("netbird_peer_info", "gauge", "Detalle de conexión de cada peer (valor siempre 1).")
	for _, p := range peers {
		m.sample("netbird_peer_info", labels{"peer", p.Name, "fqdn", p.FQDN, "ip", p.IP, "status", p.Status,
			"connection_type", p.ConnectionType, "relay", p.RelayAddress,
			"ice_local", p.ICE.LocalType, "ice_remote", p.ICE.RemoteType}, 1)
	}
	m.family("netbird_peer_connected", "gauge", "1 si el peer está conectado.")
	for _, p := range peers {
		m.sample("netbird_peer_connected", labels{"peer", p.Name, "ip", p.IP}, b2f(p.Connected))
	}
	m.family("netbird_peer_p2p", "gauge", "1 si la conexión es P2P, 0 si va por relay o está caída.")
	for _, p := range peers {
		m.sample("netbird_peer_p2p", labels{"peer", p.Name, "ip", p.IP}, b2f(p.P2P))
	}
	m.family("netbird_peer_latency_seconds", "gauge", "Latencia que informa NetBird para el peer.")
	m.family("netbird_peer_receive_bytes_total", "counter", "Bytes recibidos del peer por WireGuard.")
	m.family("netbird_peer_transmit_bytes_total", "counter", "Bytes enviados al peer por WireGuard.")
	m.family("netbird_peer_last_handshake_timestamp_seconds", "gauge", "Último handshake WireGuard (epoch).")
	m.family("netbird_peer_handshake_stale", "gauge", "1 si el último handshake es más viejo que HANDSHAKE_STALE.")
	for _, p := range peers {
		if !p.Connected {
			continue
		}
		l := labels{"peer", p.Name, "ip", p.IP}
		m.sample("netbird_peer_latency_seconds", l, p.LatencyMs/1000)
		m.sample("netbird_peer_receive_bytes_total", l, float64(p.RxBytes))
		m.sample("netbird_peer_transmit_bytes_total", l, float64(p.TxBytes))
		if p.LastHandshake != nil {
			m.sample("netbird_peer_last_handshake_timestamp_seconds", l, float64(p.LastHandshake.Unix()))
		}
		m.sample("netbird_peer_handshake_stale", l, b2f(p.HandshakeStale))
	}

	if pings == nil {
		return
	}
	m.family("netbird_peer_ping_up", "gauge", "1 si el peer respondió al ping ICMP.")
	m.family("netbird_peer_ping_rtt_seconds", "gauge", "RTT del ping ICMP (stat=min|avg|max).")
	m.family("netbird_peer_ping_jitter_seconds", "gauge", "Desviación estándar del RTT.")
	m.family("netbird_peer_ping_loss_ratio", "gauge", "Proporción de paquetes perdidos (0-1).")
	for _, p := range peers {
		pr, ok := pings[p.IP]
		if !ok {
			continue
		}
		l := labels{"peer", p.Name, "ip", p.IP}
		m.sample("netbird_peer_ping_up", l, b2f(pr.Reachable))
		m.sample("netbird_peer_ping_loss_ratio", l, pr.LossRatio)
		if pr.Reachable {
			for _, st := range []struct {
				k string
				v float64
			}{{"min", pr.RTTMinMs}, {"avg", pr.RTTAvgMs}, {"max", pr.RTTMaxMs}} {
				m.sample("netbird_peer_ping_rtt_seconds", append(labels{"stat", st.k}, l...), st.v/1000)
			}
			m.sample("netbird_peer_ping_jitter_seconds", l, pr.JitterMs/1000)
		}
	}
}

// labels: pares nombre, valor.
type labels []string

type metricWriter struct {
	w io.Writer
}

func (m *metricWriter) family(name, typ, help string) {
	fmt.Fprintf(m.w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func (m *metricWriter) sample(name string, l labels, v float64) {
	var sb strings.Builder
	sb.WriteString(name)
	if len(l) > 0 {
		sb.WriteByte('{')
		for i := 0; i+1 < len(l); i += 2 {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%s=\"%s\"", l[i], escape(l[i+1]))
		}
		sb.WriteByte('}')
	}
	fmt.Fprintf(m.w, "%s %g\n", sb.String(), v)
}

// escape aplica los únicos escapes que admite el formato de Prometheus: \\, \" y \n.
var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
