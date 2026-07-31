package proxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/ca"
)

// On hosts where a security product (Smart App Control in enforcement mode is the
// known case) interferes with TLS over the loopback interface, TCP-based proxy
// tests stall even though the code is correct. pipeHarness runs the entire
// client -> proxy -> origin chain over net.Pipe(), which never touches the OS
// network stack and so is immune to that interference. The same tests then also
// run under CI on ordinary hosts.

// pipeListener is a net.Listener whose Accept returns connections pushed onto ch.
type pipeListener struct {
	ch   chan net.Conn
	once sync.Once
}

func newPipeListener() *pipeListener { return &pipeListener{ch: make(chan net.Conn, 16)} }
func (l *pipeListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, errors.New("closed")
	}
	return c, nil
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.ch) }); return nil }
func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// selfSignedCert makes an ECDSA cert+key trusted by a caller-supplied pool.
func selfSignedCert(t *testing.T, commonName string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		DNSNames:              []string{commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf
}

// pipeHarness wires a client to the proxy and the proxy to an origin, all over
// in-memory pipes. The origin serves payload with Range support.
type pipeHarness struct {
	originHost string
	client     *http.Client
	server     *Server
	originLn   *pipeListener
	payload    []byte
}

func newPipeHarness(t *testing.T, payload []byte, cfg Config, originHost string) *pipeHarness {
	t.Helper()

	// --- origin: an HTTP server over TLS, reached via a pipe listener. ---
	originCert, originLeaf := selfSignedCert(t, originHost)
	originPool := x509.NewCertPool()
	originPool.AddCert(originLeaf)

	originLn := newPipeListener()
	tlsLn := tls.NewListener(originLn, &tls.Config{Certificates: []tls.Certificate{originCert}})
	originSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, "payload.bin", time.Unix(0, 0), bytes.NewReader(payload))
		}),
	}
	go func() { _ = originSrv.Serve(tlsLn) }()
	t.Cleanup(func() {
		_ = originSrv.Close()
		_ = originLn.Close()
	})

	// --- proxy: its upstream dial returns a pipe whose far end the origin serves. ---
	authority, err := ca.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.CA = authority
	cfg.UpstreamRootCAs = originPool
	cfg.UpstreamDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		clientEnd, originEnd := net.Pipe()
		select {
		case originLn.ch <- originEnd:
			return clientEnd, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, errors.New("origin listener queue full")
		}
	}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg)

	// --- client: dials the proxy over a pipe, on each request. ---
	mitmPool := x509.NewCertPool()
	mitmPool.AddCert(authority.Certificate())
	proxyURL, _ := url.Parse("http://jtaccel-proxy")
	clientTransport := &http.Transport{
		ForceAttemptHTTP2: false,
		Proxy:             http.ProxyURL(proxyURL),
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// The proxy is reached through a fresh pipe each time. The far end is
			// handed to the proxy's connection handler.
			c, p := net.Pipe()
			go srv.HandleConn(p)
			return c, nil
		},
		TLSClientConfig: &tls.Config{RootCAs: mitmPool},
	}

	return &pipeHarness{
		originHost: originHost,
		client:     &http.Client{Transport: clientTransport, Timeout: 30 * time.Second},
		server:     srv,
		originLn:   originLn,
		payload:    payload,
	}
}

func (h *pipeHarness) get(t *testing.T, path, rangeHdr string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://"+h.originHost+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

// HandleConn exposes the per-connection entry point for tests using pipes.
func (s *Server) HandleConn(conn net.Conn) { s.handleConn(conn) }
