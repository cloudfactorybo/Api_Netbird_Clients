// Package tlsutil carga o genera el certificado del servidor solo con la
// biblioteca estándar de Go (crypto/ecdsa, crypto/x509, crypto/tls).
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// renewBefore: un certificado autogenerado que vence antes de esto se regenera al arrancar.
const renewBefore = 30 * 24 * time.Hour

type Info struct {
	Subject     string    `json:"subject"`
	DNSNames    []string  `json:"dns_names"`
	IPAddresses []string  `json:"ip_addresses"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	SHA256      string    `json:"sha256"`             // hex sin separadores (ca_trusted_fingerprint de Elastic)
	SHA256Colon string    `json:"sha256_fingerprint"` // AA:BB:...
	Generated   bool      `json:"generated"`
}

// LoadOrCreate devuelve el certificado. Si autogen está activo y el archivo no
// existe, vence pronto o no cubre todos los SAN pedidos, genera uno nuevo.
func LoadOrCreate(certPath, keyPath string, sans []string, days int, autogen bool) (tls.Certificate, Info, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err == nil {
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	}
	if err == nil && (!autogen || (time.Until(cert.Leaf.NotAfter) > renewBefore && covers(cert.Leaf, sans))) {
		return cert, describe(cert.Leaf, false), nil
	}
	if !autogen {
		return tls.Certificate{}, Info{}, fmt.Errorf("no se pudo cargar el certificado (TLS_AUTOGEN=false): %w", err)
	}
	cert, leaf, err := generate(certPath, keyPath, sans, days)
	if err != nil {
		return tls.Certificate{}, Info{}, err
	}
	return cert, describe(leaf, true), nil
}

// ServerConfig: TLS 1.2 como mínimo, curvas modernas; los cipher suites son los
// que Go elige por defecto (solo AEAD con forward secrecy en TLS 1.2).
func ServerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
		Certificates:     []tls.Certificate{cert},
	}
}

func generate(certPath, keyPath string, sans []string, days int) (tls.Certificate, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	host, _ := os.Hostname()
	now := time.Now().Add(-5 * time.Minute)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "api-netbird-clients " + host, Organization: []string{"CloudFactory Bolivia"}},
		NotBefore:    now,
		NotAfter:     now.Add(time.Duration(days) * 24 * time.Hour),
		// Autofirmado y marcado como CA: así sirve como su propia raíz de
		// confianza (certificate_authorities o ca_trusted_fingerprint en Elastic).
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, s := range sans {
		if ip := net.ParseIP(s); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, s)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := writeAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func covers(c *x509.Certificate, sans []string) bool {
	for _, s := range sans {
		if ip := net.ParseIP(s); ip != nil {
			found := false
			for _, have := range c.IPAddresses {
				if have.Equal(ip) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		} else if c.VerifyHostname(s) != nil {
			return false
		}
	}
	return true
}

// Describe lee un certificado PEM del disco (subcomando cert-info).
func Describe(certPath string) (Info, []byte, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return Info{}, nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return Info{}, nil, errors.New("el archivo no contiene un certificado PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Info{}, nil, err
	}
	return describe(leaf, false), data, nil
}

func describe(c *x509.Certificate, generated bool) Info {
	sum := sha256.Sum256(c.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	pairs := make([]string, 0, len(sum))
	for i := 0; i < len(h); i += 2 {
		pairs = append(pairs, h[i:i+2])
	}
	info := Info{
		Subject:     c.Subject.String(),
		DNSNames:    c.DNSNames,
		NotBefore:   c.NotBefore.UTC(),
		NotAfter:    c.NotAfter.UTC(),
		SHA256:      strings.ToLower(h),
		SHA256Colon: strings.Join(pairs, ":"),
		Generated:   generated,
	}
	for _, ip := range c.IPAddresses {
		info.IPAddresses = append(info.IPAddresses, ip.String())
	}
	return info
}
