package ca

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// TestMintedCertCompletesTLSHandshake verifies a minted leaf actually works for a
// real TLS handshake, independent of any proxy logic. Uses net.Pipe so it is
// immune to hosts where a security product interferes with loopback TLS.
func TestMintedCertCompletesTLSHandshake(t *testing.T) {
	a, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := a.LeafFor("example.com")
	if err != nil {
		t.Fatal(err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	serverErr := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		tc := tls.Server(serverConn, &tls.Config{
			Certificates: []tls.Certificate{*cert},
			MinVersion:   tls.VersionTLS12,
		})
		if err := tc.Handshake(); err != nil {
			serverErr <- err
			return
		}
		_, err = io.WriteString(tc, "ok")
		serverErr <- err
	}()

	pool := x509.NewCertPool()
	pool.AddCert(a.Certificate())

	tc := tls.Client(clientConn, &tls.Config{ServerName: "example.com", RootCAs: pool})
	if err := tc.HandshakeContext(ctxTimeout(t, 5*time.Second)); err != nil {
		t.Fatalf("client handshake: %v (server: %v)", err, <-serverErr)
	}

	buf := make([]byte, 2)
	if _, err := io.ReadFull(tc, buf); err != nil {
		t.Fatalf("client read: %v (server: %v)", err, <-serverErr)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}
	if string(buf) != "ok" {
		t.Fatalf("got %q", buf)
	}
	t.Logf("handshake OK, version=%x cipher=%x", tc.ConnectionState().Version, tc.ConnectionState().CipherSuite)
}

func ctxTimeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestTrustStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "truststore.p12")
	if err := a.WriteTrustStore(path, "secret123"); err != nil {
		t.Fatalf("write truststore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("truststore unreadable: %v", err)
	}
	// PKCS#12 is a DER SEQUENCE, so it must begin with tag 0x30.
	if len(data) == 0 || data[0] != 0x30 {
		t.Fatalf("truststore is not DER (first byte %#x, len %d)", data[0], len(data))
	}
	certs, err := pkcs12.DecodeTrustStore(data, "secret123")
	if err != nil {
		t.Fatalf("decode truststore: %v", err)
	}
	if len(certs) != 1 || !certs[0].Equal(a.Certificate()) {
		t.Fatalf("truststore does not contain exactly our CA (got %d certs)", len(certs))
	}
}

