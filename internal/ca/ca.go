// Package ca manages the local certificate authority used to intercept TLS.
//
// The CA exists only so JetBrains Toolbox will accept our proxy's certificates.
// It is never installed into an OS trust store: instead we emit a PKCS#12
// truststore that Toolbox loads via its own `network.keystore` setting, which
// *adds* to the default trust set rather than replacing it. That keeps the blast
// radius to Toolbox alone and survives Toolbox self-updates, which replace the
// bundled JRE (and would silently revert any patch to its cacerts).
package ca

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
	"path/filepath"
	"sync"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const (
	caCertFile = "ca.crt"
	caKeyFile  = "ca.key"

	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 825 * 24 * time.Hour
)

// Authority mints leaf certificates on demand, signed by a locally generated CA.
type Authority struct {
	dir    string
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	certDER []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
	serial int64
}

// Load returns the CA in dir, generating one if absent.
func Load(dir string) (*Authority, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create ca dir: %w", err)
	}
	a := &Authority{dir: dir, leaves: make(map[string]*tls.Certificate)}

	certPath := filepath.Join(dir, caCertFile)
	keyPath := filepath.Join(dir, caKeyFile)

	certPEM, errC := os.ReadFile(certPath)
	keyPEM, errK := os.ReadFile(keyPath)
	if errC == nil && errK == nil {
		if err := a.load(certPEM, keyPEM); err == nil {
			// Regenerate rather than hand back something already expired.
			if time.Now().Before(a.cert.NotAfter) {
				return a, nil
			}
		}
	}

	if err := a.generate(); err != nil {
		return nil, err
	}
	if err := a.save(certPath, keyPath); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Authority) load(certPEM, keyPEM []byte) error {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return fmt.Errorf("malformed CA pem")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return err
	}
	a.cert, a.key, a.certDER = cert, key, cb.Bytes
	return nil
}

func (a *Authority) generate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ca key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "jtaccel local CA",
			Organization: []string{"jetbrains-toolbox-accelerator"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create ca cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	a.cert, a.key, a.certDER = cert, key, der
	return nil
}

func (a *Authority) save(certPath, keyPath string) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.certDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	der, err := x509.MarshalECPrivateKey(a.key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	// The CA key can mint certificates for any host: never group/world readable.
	return os.WriteFile(keyPath, keyPEM, 0o600)
}

// LeafFor returns a certificate valid for host, minting and caching on first use.
func (a *Authority) LeafFor(host string) (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.leaves[host]; ok {
		return c, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("mint leaf for %s: %w", host, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	tc := &tls.Certificate{
		Certificate: [][]byte{der, a.certDER},
		PrivateKey:  key,
		Leaf:        leaf,
	}
	a.leaves[host] = tc
	return tc, nil
}

// CertPEM returns the CA certificate in PEM form.
func (a *Authority) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.certDER})
}

// Certificate returns the parsed CA certificate.
func (a *Authority) Certificate() *x509.Certificate { return a.cert }

// WriteTrustStore emits a PKCS#12 truststore holding only this CA.
//
// Toolbox reads it through NetworkSettings.keystore and *adds* the contents to
// its default trust set (CertificateManagerImpl.addCertificatesFromKeystore), so
// a single-entry store is correct -- it does not need the system roots.
func (a *Authority) WriteTrustStore(path, password string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := pkcs12.Modern.EncodeTrustStore([]*x509.Certificate{a.cert}, password)
	if err != nil {
		return fmt.Errorf("encode truststore: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	return n, nil
}
