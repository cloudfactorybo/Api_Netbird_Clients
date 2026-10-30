// api-netbird-clients: API HTTPS de solo lectura con el estado del cliente
// NetBird local (P2P/relay, latencia, handshake, ping) y métricas Prometheus.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/api"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/config"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/netbird"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/probe"
	"github.com/cloudfactorybo/Api_Netbird_Clients/internal/tlsutil"
)

// version se fija al compilar: -ldflags "-X main.version=v1.0.0".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "healthcheck":
		err = healthcheck()
	case "cert-info":
		err = certInfo(len(os.Args) > 2 && os.Args[2] == "--pem")
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		fmt.Println("uso: api-netbird-clients [serve|healthcheck|cert-info [--pem]|version]")
	default:
		err = fmt.Errorf("subcomando desconocido: %s (usa help)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serve() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	cert, info, err := tlsutil.LoadOrCreate(cfg.TLSCert, cfg.TLSKey, cfg.TLSSANs, cfg.TLSValidDays, cfg.TLSAutogen)
	if err != nil {
		return fmt.Errorf("TLS: %w", err)
	}
	log.Info("certificado TLS", "generado", info.Generated, "vence", info.NotAfter,
		"sha256", info.SHA256Colon, "dns", info.DNSNames, "ips", info.IPAddresses)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Si el daemon no responde varias veces seguidas se sale con error: Docker
	// reinicia el contenedor y vuelve a montar el socket (que cambia de inodo
	// cada vez que el daemon NetBird se reinicia).
	src := netbird.NewSource(
		netbird.CLIRunner(cfg.NetbirdBin, cfg.DaemonAddr, cfg.CommandTimeout),
		cfg.CacheTTL, cfg.HandshakeStale, cfg.ExitAfterFailures,
		func(err error) {
			log.Error("daemon NetBird inaccesible, se reinicia el proceso", "error", err)
			os.Exit(3)
		})

	var ping api.PingFunc
	if cfg.PingEnabled {
		p := probe.New(probe.Options{Count: cfg.PingCount, Interval: cfg.PingInterval, Timeout: cfg.PingTimeout})
		ping = p.Ping
	}
	token := cfg.APIToken
	if cfg.AuthDisabled {
		token = ""
		log.Warn("AUTH_DISABLED=true: la API no pide token")
	}
	handler := api.New(src, ping, cfg.PingTTL, token, version, log).Handler()
	tlsCfg := tlsutil.ServerConfig(cert)

	errc := make(chan error, len(cfg.ListenAddrs))
	var servers []*http.Server
	for _, addr := range cfg.ListenAddrs {
		srv := &http.Server{
			Addr:              addr,
			Handler:           handler,
			TLSConfig:         tlsCfg,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    16 << 10,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("no se pudo escuchar en %s: %w", addr, err)
		}
		servers = append(servers, srv)
		log.Info("escuchando", "addr", addr, "version", version)
		go func() {
			if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	log.Info("deteniendo")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shCtx)
	}
	return nil
}

// healthcheck consulta /healthz en el primer LISTEN_ADDRS (lo usa el HEALTHCHECK de Docker).
func healthcheck() error {
	addrs := strings.FieldsFunc(os.Getenv("LISTEN_ADDRS"), func(r rune) bool { return r == ',' || r == ' ' })
	addr := ":8443"
	if len(addrs) > 0 {
		addr = addrs[0]
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		// Conexión local al propio proceso: no hace falta validar el certificado.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
	}
	resp, err := client.Get("https://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz devolvió %d", resp.StatusCode)
	}
	return nil
}

func certInfo(pemOut bool) error {
	path := os.Getenv("TLS_CERT")
	if path == "" {
		path = "/data/tls/server.crt"
	}
	info, data, err := tlsutil.Describe(path)
	if err != nil {
		return err
	}
	if pemOut {
		_, err = os.Stdout.Write(data)
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(info)
}
