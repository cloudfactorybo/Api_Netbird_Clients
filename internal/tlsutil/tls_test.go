package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "tls", "server.crt"), filepath.Join(dir, "tls", "server.key")
	sans := []string{"localhost", "127.0.0.1", "100.64.0.1", "colector01.netbird.selfhosted"}

	cert, info, err := LoadOrCreate(crt, key, sans, 365, true)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Generated || len(info.SHA256) != 64 || len(info.IPAddresses) != 2 || len(info.DNSNames) != 2 {
		t.Fatalf("info inesperada: %+v", info)
	}
	if st, _ := os.Stat(key); st.Mode().Perm() != 0o600 {
		t.Errorf("la clave debe ser 0600, es %v", st.Mode().Perm())
	}

	// El certificado debe validar contra sí mismo (fijarlo como CA en el cliente).
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "colector01.netbird.selfhosted"}); err != nil {
		t.Errorf("no valida contra sí mismo: %v", err)
	}

	// Segundo arranque con los mismos SAN: se reutiliza (misma huella).
	_, info2, err := LoadOrCreate(crt, key, sans, 365, true)
	if err != nil || info2.Generated || info2.SHA256 != info.SHA256 {
		t.Errorf("debía reutilizarse: generated=%v err=%v", info2.Generated, err)
	}

	// Un SAN nuevo obliga a regenerar.
	_, info3, err := LoadOrCreate(crt, key, append(sans, "192.168.1.20"), 365, true)
	if err != nil || !info3.Generated || info3.SHA256 == info.SHA256 {
		t.Errorf("debía regenerarse con el SAN nuevo: generated=%v err=%v", info3.Generated, err)
	}

	// Sin autogen y sin archivos: error claro.
	if _, _, err := LoadOrCreate(filepath.Join(dir, "x.crt"), filepath.Join(dir, "x.key"), sans, 365, false); err == nil {
		t.Error("TLS_AUTOGEN=false sin archivos debía fallar")
	}
}

func TestServerConfig(t *testing.T) {
	c := ServerConfig(tls.Certificate{})
	if c.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x", c.MinVersion)
	}
}
