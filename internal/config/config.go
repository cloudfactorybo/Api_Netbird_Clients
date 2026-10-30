// Package config lee la configuración desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddrs  []string // LISTEN_ADDRS: "127.0.0.1:8443,100.64.0.1:8443"
	APIToken     string   // API_TOKEN: obligatorio salvo AUTH_DISABLED=true
	AuthDisabled bool

	TLSCert      string   // ruta del certificado (se genera si no existe)
	TLSKey       string   // ruta de la clave privada
	TLSAutogen   bool     // false: solo cargar los archivos, nunca generar
	TLSSANs      []string // IPs y nombres DNS del certificado autogenerado
	TLSValidDays int

	NetbirdBin        string // binario del cliente NetBird
	DaemonAddr        string // socket del daemon NetBird
	CommandTimeout    time.Duration
	CacheTTL          time.Duration
	ExitAfterFailures int // reinicia el proceso si el daemon no responde N veces seguidas (0 = nunca)
	HandshakeStale    time.Duration

	PingEnabled  bool
	PingCount    int
	PingInterval time.Duration
	PingTimeout  time.Duration
	PingTTL      time.Duration
}

func Load() (Config, error) {
	c := Config{
		ListenAddrs:       list(env("LISTEN_ADDRS", ":8443")),
		APIToken:          os.Getenv("API_TOKEN"),
		AuthDisabled:      boolean("AUTH_DISABLED", false),
		TLSCert:           env("TLS_CERT", "/data/tls/server.crt"),
		TLSKey:            env("TLS_KEY", "/data/tls/server.key"),
		TLSAutogen:        boolean("TLS_AUTOGEN", true),
		TLSSANs:           list(env("TLS_SANS", "localhost,127.0.0.1")),
		TLSValidDays:      integer("TLS_VALID_DAYS", 1825),
		NetbirdBin:        env("NETBIRD_BIN", "netbird"),
		DaemonAddr:        env("NETBIRD_DAEMON_ADDR", "unix:///var/run/netbird.sock"),
		CommandTimeout:    duration("NETBIRD_TIMEOUT", 8*time.Second),
		CacheTTL:          duration("CACHE_TTL", 10*time.Second),
		ExitAfterFailures: integer("EXIT_AFTER_FAILURES", 5),
		HandshakeStale:    duration("HANDSHAKE_STALE", 3*time.Minute),
		PingEnabled:       boolean("PING_ENABLED", true),
		PingCount:         integer("PING_COUNT", 3),
		PingInterval:      duration("PING_INTERVAL", 200*time.Millisecond),
		PingTimeout:       duration("PING_TIMEOUT", 2*time.Second),
		PingTTL:           duration("PING_TTL", 30*time.Second),
	}
	var errs []error
	if len(c.ListenAddrs) == 0 {
		errs = append(errs, errors.New("LISTEN_ADDRS vacío"))
	}
	if c.APIToken == "" && !c.AuthDisabled {
		errs = append(errs, errors.New("API_TOKEN es obligatorio (o AUTH_DISABLED=true)"))
	}
	if c.APIToken != "" && len(c.APIToken) < 16 {
		errs = append(errs, errors.New("API_TOKEN debe tener al menos 16 caracteres"))
	}
	if c.PingCount < 1 || c.PingCount > 20 {
		errs = append(errs, fmt.Errorf("PING_COUNT fuera de rango (1-20): %d", c.PingCount))
	}
	if c.TLSValidDays < 1 {
		errs = append(errs, fmt.Errorf("TLS_VALID_DAYS inválido: %d", c.TLSValidDays))
	}
	return c, errors.Join(errs...)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// list acepta valores separados por comas o espacios.
func list(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func boolean(key string, def bool) bool {
	v, err := strconv.ParseBool(env(key, strconv.FormatBool(def)))
	if err != nil {
		return def
	}
	return v
}

func integer(key string, def int) int {
	v, err := strconv.Atoi(env(key, strconv.Itoa(def)))
	if err != nil {
		return def
	}
	return v
}

func duration(key string, def time.Duration) time.Duration {
	v, err := time.ParseDuration(env(key, def.String()))
	if err != nil {
		return def
	}
	return v
}
