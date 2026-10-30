// Package api expone el estado del cliente NetBird por HTTPS (net/http).
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/netbird"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/probe"
)

// StatusSource es lo que la API necesita del cliente NetBird.
type StatusSource interface {
	Get(ctx context.Context) (*netbird.Status, []byte, error)
}

// PingFunc mide las IPs dadas; nil desactiva el ping.
type PingFunc func(ctx context.Context, ips []string) ([]probe.Result, error)

type Server struct {
	src     StatusSource
	ping    PingFunc
	pingTTL time.Duration
	token   string
	version string
	log     *slog.Logger

	pingMu  sync.Mutex
	pingAt  time.Time
	pingRes map[string]probe.Result
	pingErr error
}

func New(src StatusSource, ping PingFunc, pingTTL time.Duration, token, version string, log *slog.Logger) *Server {
	return &Server{src: src, ping: ping, pingTTL: pingTTL, token: token, version: version, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /api/v1/health", s.auth(s.health))
	mux.Handle("GET /api/v1/status", s.auth(s.status))
	mux.Handle("GET /api/v1/status/raw", s.auth(s.statusRaw))
	mux.Handle("GET /api/v1/peers", s.auth(s.peers))
	mux.Handle("GET /api/v1/peers/{peer}", s.auth(s.peer))
	mux.Handle("GET /api/v1/ping", s.auth(s.pingAll))
	mux.Handle("GET /metrics", s.auth(s.metrics))
	return s.logRequests(securityHeaders(mux))
}

// auth exige "Authorization: Bearer <token>" o "X-API-Key: <token>".
func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if got == "" || got == r.Header.Get("Authorization") {
				got = r.Header.Get("X-API-Key")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api-netbird-clients"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token inválido o ausente"})
				return
			}
		}
		next(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" && rec.code == http.StatusOK {
			return // el healthcheck de Docker no ensucia el log
		}
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.code,
			"duration_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) unavailable(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error":   "no se pudo leer el estado de NetBird",
		"details": err.Error(),
	})
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "api-netbird-clients",
		"version": s.version,
		"endpoints": map[string]string{
			"healthz":    "/healthz (sin token)",
			"health":     "/api/v1/health",
			"status":     "/api/v1/status",
			"status_raw": "/api/v1/status/raw",
			"peers":      "/api/v1/peers?status=connected&type=p2p|relayed",
			"peer":       "/api/v1/peers/{ip|fqdn|nombre}",
			"ping":       "/api/v1/ping",
			"prometheus": "/metrics",
		},
	})
}

// healthz: liveness sin datos, para el healthcheck del contenedor.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.src.Get(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "up"})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	st, _, err := s.src.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"health": "unhealthy", "error": err.Error()})
		return
	}
	code := http.StatusOK
	if st.Health == "unhealthy" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"timestamp":      st.Timestamp,
		"health":         st.Health,
		"health_reasons": st.HealthReason,
		"management":     st.Management.Connected,
		"signal":         st.Signal.Connected,
		"relays":         map[string]int{"total": st.Relays.Total, "available": st.Relays.Available},
		"counts":         st.Counts,
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st, _, err := s.src.Get(r.Context())
	if err != nil {
		s.unavailable(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) statusRaw(w http.ResponseWriter, r *http.Request) {
	_, raw, err := s.src.Get(r.Context())
	if err != nil {
		s.unavailable(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(raw)
}

func (s *Server) peers(w http.ResponseWriter, r *http.Request) {
	st, _, err := s.src.Get(r.Context())
	if err != nil {
		s.unavailable(w, err)
		return
	}
	wantStatus := strings.ToLower(r.URL.Query().Get("status"))
	wantType := strings.ToLower(r.URL.Query().Get("type"))
	out := make([]netbird.Peer, 0, len(st.Peers))
	for _, p := range st.Peers {
		if wantStatus == "connected" && !p.Connected || wantStatus == "disconnected" && p.Connected {
			continue
		}
		if wantType != "" && strings.ToLower(p.ConnectionType) != wantType {
			continue
		}
		out = append(out, p)
	}
	// Objeto con el arreglo dentro: el response split de httpjson apunta a body.peers.
	writeJSON(w, http.StatusOK, map[string]any{
		"timestamp": st.Timestamp,
		"host":      st.Host.FQDN,
		"counts":    st.Counts,
		"peers":     out,
	})
}

func (s *Server) peer(w http.ResponseWriter, r *http.Request) {
	st, _, err := s.src.Get(r.Context())
	if err != nil {
		s.unavailable(w, err)
		return
	}
	p, err := st.FindPeer(r.PathValue("peer"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"timestamp": st.Timestamp, "host": st.Host.FQDN, "peer": p}
	if res, err := s.pingResults(r.Context(), st); err == nil {
		if pr, ok := res[p.IP]; ok {
			resp["ping"] = pr
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type pingEntry struct {
	Name           string `json:"name"`
	FQDN           string `json:"fqdn"`
	ConnectionType string `json:"connection_type"`
	probe.Result
}

func (s *Server) pingAll(w http.ResponseWriter, r *http.Request) {
	if s.ping == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "ping desactivado (PING_ENABLED=false)"})
		return
	}
	st, _, err := s.src.Get(r.Context())
	if err != nil {
		s.unavailable(w, err)
		return
	}
	res, err := s.pingResults(r.Context(), st)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]pingEntry, 0, len(res))
	reachable := 0
	for _, p := range st.Peers {
		pr, ok := res[p.IP]
		if !ok {
			continue
		}
		if pr.Reachable {
			reachable++
		}
		out = append(out, pingEntry{Name: p.Name, FQDN: p.FQDN, ConnectionType: p.ConnectionType, Result: pr})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timestamp":   time.Now().UTC(),
		"host":        st.Host.FQDN,
		"total":       len(out),
		"reachable":   reachable,
		"unreachable": len(out) - reachable,
		"results":     out,
	})
}

// pingResults mide solo los peers conectados y guarda el resultado PingTTL.
func (s *Server) pingResults(ctx context.Context, st *netbird.Status) (map[string]probe.Result, error) {
	if s.ping == nil {
		return nil, errors.New("ping desactivado")
	}
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	if !s.pingAt.IsZero() && time.Since(s.pingAt) < s.pingTTL {
		return s.pingRes, s.pingErr
	}
	var ips []string
	for _, p := range st.Peers {
		if p.Connected && p.IP != "" {
			ips = append(ips, p.IP)
		}
	}
	res := map[string]probe.Result{}
	var err error
	if len(ips) > 0 {
		var list []probe.Result
		list, err = s.ping(ctx, ips)
		for _, r := range list {
			res[r.IP] = r
		}
	}
	if err != nil {
		s.log.Warn("ping falló", "error", err)
	}
	s.pingAt, s.pingRes, s.pingErr = time.Now(), res, err
	return res, err
}
