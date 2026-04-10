// Package servertls provides helpers for self-signed TLS certificate
// generation and loading. The CDN-sim emulated mode uses a throwaway
// self-signed cert across origin, shield, and edge so HTTP/2 and HTTP/3
// endpoints can be reached without external CA infrastructure.
//
// Design notes:
//
//   - Certificates here are "combined root+leaf": self-signed with BasicConstraints
//     IsCA true so clients that pin them via RootCAs verify successfully. For a
//     real deployment a separate CA + leaf (as in docker/certs/generate.sh) is
//     preferred; this package optimises for the "one process generates and
//     serves its own cert" testbed case.
//   - Serial numbers are drawn from crypto/rand (128 bits) so rapid successive
//     LoadOrGenerate calls in parallel tests cannot collide on the same
//     UnixNano bucket (RFC 5280 §4.1.2.2).
//   - Key files are written with mode 0600; certs with 0644.
package servertls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// LoadOrGenerate loads a TLS certificate from disk if both files exist,
// otherwise generates a self-signed cert valid for the supplied DNS names
// and IPs and writes it to disk. The same key/cert is reused for HTTP/2
// and HTTP/3 (the QUIC handshake uses TLS 1.3 by spec).
func LoadOrGenerate(certPath, keyPath string, dnsNames []string, ips []net.IP) (tls.Certificate, error) {
	if certPath != "" && keyPath != "" {
		if _, err := os.Stat(certPath); err == nil {
			if _, err := os.Stat(keyPath); err == nil {
				return tls.LoadX509KeyPair(certPath, keyPath)
			}
		}
	}
	cert, err := generateSelfSigned(dnsNames, ips)
	if err != nil {
		return tls.Certificate{}, err
	}
	if certPath != "" && keyPath != "" {
		if err := persist(cert, certPath, keyPath); err != nil {
			return tls.Certificate{}, err
		}
	}
	return cert, nil
}

func generateSelfSigned(dnsNames []string, ips []net.IP) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("ec key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("serial: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "cdn-sim",
			Organization: []string{"cdn-sim test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // combined root+leaf: clients pin via RootCAs
		DNSNames:              append([]string{"localhost", "cdn-sim"}, dnsNames...),
		IPAddresses:           append([]net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, ips...),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create cert: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		// Cannot happen in practice — x509.CreateCertificate just produced
		// these bytes — but never discard errors silently.
		return tls.Certificate{}, fmt.Errorf("parse generated cert: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
		Leaf:        leaf,
	}, nil
}

func persist(cert tls.Certificate, certPath, keyPath string) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	priv, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("unexpected key type %T", cert.PrivateKey)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

// PoolFromCert returns an x509 cert pool containing the supplied certificate's
// leaf. Used by clients to trust the self-signed cert. If the cert has no
// Leaf set (e.g. loaded via tls.LoadX509KeyPair without parsing), the cert
// is parsed on the fly.
func PoolFromCert(cert tls.Certificate) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	leaf := cert.Leaf
	if leaf == nil {
		if len(cert.Certificate) == 0 {
			return nil, fmt.Errorf("cert has no DER bytes")
		}
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parse cert for pool: %w", err)
		}
		leaf = parsed
	}
	pool.AddCert(leaf)
	return pool, nil
}
