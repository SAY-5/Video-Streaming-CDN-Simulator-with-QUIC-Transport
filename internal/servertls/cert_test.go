package servertls

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateAndParse(t *testing.T) {
	cert, err := LoadOrGenerate("", "", []string{"test.example"}, []net.IP{net.ParseIP("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Leaf == nil {
		t.Fatal("Leaf must be populated")
	}
	if cert.Leaf.NotAfter.Before(time.Now()) {
		t.Fatal("cert already expired")
	}
	// Check a SAN we asked for is present.
	found := false
	for _, name := range cert.Leaf.DNSNames {
		if name == "test.example" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SAN test.example missing: %v", cert.Leaf.DNSNames)
	}
	// Serial must be a positive 128-bit integer.
	if cert.Leaf.SerialNumber.Sign() <= 0 {
		t.Fatalf("serial not positive: %v", cert.Leaf.SerialNumber)
	}
}

func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	cert1, err := LoadOrGenerate(certPath, keyPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// File modes.
	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if certInfo.Mode().Perm() != 0o644 {
		t.Errorf("cert perm=%v, want 0644", certInfo.Mode().Perm())
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Errorf("key perm=%v, want 0600", keyInfo.Mode().Perm())
	}
	// LoadOrGenerate should now return the same cert bytes.
	cert2, err := LoadOrGenerate(certPath, keyPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(cert1.Certificate[0]) != string(cert2.Certificate[0]) {
		t.Fatal("reloaded cert differs from generated cert")
	}
}

func TestPoolFromCert(t *testing.T) {
	cert, err := LoadOrGenerate("", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := PoolFromCert(cert)
	if err != nil {
		t.Fatal(err)
	}
	// Verify the generated cert can be validated against the pool we just
	// built. Since this is a self-signed root+leaf, the cert is its own
	// CA; Verify should succeed against any of the SANs.
	opts := x509.VerifyOptions{
		Roots:   pool,
		DNSName: "localhost",
	}
	if _, err := cert.Leaf.Verify(opts); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestPoolFromCertNoLeaf(t *testing.T) {
	// Mimic what tls.LoadX509KeyPair produces: Certificate populated but
	// Leaf empty. PoolFromCert must parse on the fly.
	base, err := LoadOrGenerate("", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stripped := tls.Certificate{
		Certificate: base.Certificate,
		PrivateKey:  base.PrivateKey,
		// Leaf: nil
	}
	pool, err := PoolFromCert(stripped)
	if err != nil {
		t.Fatal(err)
	}
	if pool == nil {
		t.Fatal("nil pool returned")
	}
}

func TestUniqueSerialUnderRapidGeneration(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		cert, err := LoadOrGenerate("", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		k := cert.Leaf.SerialNumber.String()
		if seen[k] {
			t.Fatalf("duplicate serial after %d iterations: %s", i, k)
		}
		seen[k] = true
	}
}
