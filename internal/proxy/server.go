// Package proxy implements the accelerating HTTP proxy.
//
// Toolbox is pointed at this proxy through its own settings. For each CONNECT we
// either splice bytes blind (hosts on the bypass list, so credential traffic is
// never decrypted) or terminate TLS with a locally minted certificate and decide,
// per response, whether the transfer is worth parallelising.
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/ca"
)

// Defaults chosen from measurements documented in README.md.
const (
	DefaultPort = 8899

	// Below this, a single stream is already fast enough that splitting only
	// adds latency.
	DefaultMinParallelSize int64 = 32 << 20

	// Segment size sets ordering granularity and therefore time-to-first-byte:
	// nothing can be written to the client until segment 0 lands, and bandwidth
	// is shared across all workers. Measured on an 8.5 MB/s line, 8 MiB segments
	// stalled the stream head for ~16s and yielded 1.55 MB/s -- worse than no
	// proxy at all. 1 MiB gives 0.94s TTFB and 8.25 MB/s. Do not raise this
	// without re-running the throughput test.
	DefaultSegmentSize int64 = 1 << 20

	DefaultWorkers      = 16
	DefaultMaxBuffered  = 128 // segments held in the reorder buffer (~128 MiB)
	DefaultSegmentTries = 4
)

// DefaultBypassHosts are tunneled without interception. Account and OAuth traffic
// carries credentials and is never a large download, so there is nothing to gain
// by decrypting it and something to lose.
var DefaultBypassHosts = []string{
	"account.jetbrains.com",
	"oauth.account.jetbrains.com",
	"auth.jetbrains.com",
}

type Config struct {
	Addr            string
	CA              *ca.Authority
	MinParallelSize int64
	SegmentSize     int64
	Workers         int
	MaxBuffered     int
	BypassHosts     []string
	IdleTimeout     time.Duration
	Logger          *slog.Logger

	// UpstreamRootCAs overrides the roots used when dialling origins. Production
	// leaves this nil (system roots); tests set it to trust a local origin.
	UpstreamRootCAs *x509.CertPool

	// UpstreamDial overrides how the proxy reaches origins. Production leaves this
	// nil (real TCP dial). Tests set it to return an in-memory pipe so the whole
	// client->proxy->origin chain runs without touching the OS network stack --
	// necessary on hosts where a security product interferes with loopback TLS.
	UpstreamDial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (c *Config) applyDefaults() {
	if c.Addr == "" {
		c.Addr = fmt.Sprintf("127.0.0.1:%d", DefaultPort)
	}
	if c.MinParallelSize <= 0 {
		c.MinParallelSize = DefaultMinParallelSize
	}
	if c.SegmentSize <= 0 {
		c.SegmentSize = DefaultSegmentSize
	}
	if c.Workers <= 0 {
		c.Workers = DefaultWorkers
	}
	if c.MaxBuffered <= 0 {
		c.MaxBuffered = DefaultMaxBuffered
	}
	// The writer emits segments in order, so a worker holding a low-numbered
	// segment must always be able to claim a buffer slot. Fewer slots than
	// workers can deadlock the pipeline.
	if c.MaxBuffered < c.Workers {
		c.MaxBuffered = c.Workers
	}
	if c.BypassHosts == nil {
		c.BypassHosts = DefaultBypassHosts
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

type Server struct {
	cfg       Config
	transport *http.Transport
	client    *http.Client
	log       *slog.Logger

	lastActivity atomic.Int64 // unix nanos
	inFlight     atomic.Int64
}

func New(cfg Config) *Server {
	cfg.applyDefaults()

	// HTTP/2 is deliberately disabled upstream. H2 multiplexes every request
	// over a SINGLE TCP connection, which is exactly the bottleneck we exist to
	// work around -- parallel ranged GETs would collapse back onto one stream
	// and gain nothing. Forcing http/1.1 gives us N genuinely independent
	// connections.
	tr := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: false,
		TLSClientConfig: &tls.Config{
			NextProtos: []string{"http/1.1"},
			RootCAs:    cfg.UpstreamRootCAs,
		},
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   cfg.Workers + 8,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if cfg.UpstreamDial != nil {
		tr.DialContext = cfg.UpstreamDial
	} else {
		tr.DialContext = (&net.Dialer{
			Timeout:   20 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}

	s := &Server{
		cfg:       cfg,
		transport: tr,
		client:    &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		log:       cfg.Logger,
	}
	s.touch()
	return s
}

func (s *Server) touch() { s.lastActivity.Store(time.Now().UnixNano()) }

// IdleFor reports how long the proxy has had no traffic. In-flight transfers
// always count as active, so a long download is never mistaken for idleness.
func (s *Server) IdleFor() time.Duration {
	if s.inFlight.Load() > 0 {
		return 0
	}
	return time.Since(time.Unix(0, s.lastActivity.Load()))
}

// ListenAndServe runs until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve runs on an existing listener (used for socket activation).
//
// This is a bare accept loop rather than an http.Server. A proxy must take the
// raw connection over after CONNECT, and net/http's Hijack hands back a reader
// entangled with its internal connReader bookkeeping -- bytes the client
// pipelines behind the CONNECT (typically the whole TLS ClientHello) are not
// reliably replayed, and the interception stalls. Owning the bufio.Reader
// ourselves removes that entire class of failure.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	if s.cfg.IdleTimeout > 0 {
		go s.watchIdle(ctx, ln)
	}

	s.log.Info("listening",
		"addr", ln.Addr().String(),
		"workers", s.cfg.Workers,
		"segment", s.cfg.SegmentSize,
		"bypass", strings.Join(s.cfg.BypassHosts, ","))

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) watchIdle(ctx context.Context, ln net.Listener) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.IdleFor() >= s.cfg.IdleTimeout {
				s.log.Info("idle timeout reached, exiting", "idle", s.IdleFor().Round(time.Second))
				_ = ln.Close()
				return
			}
		}
	}
}

// handleConn serves one client connection: a CONNECT to set up a tunnel, or a
// plain proxied HTTP request.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	s.touch()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		s.handleConnect(conn, br, req)
		return
	}
	s.handlePlainHTTP(conn, req)
}

// shouldBypass reports whether host must be tunneled without interception.
func (s *Server) shouldBypass(host string) bool {
	h := strings.ToLower(host)
	for _, b := range s.cfg.BypassHosts {
		b = strings.ToLower(strings.TrimSpace(b))
		if b == "" {
			continue
		}
		if h == b || strings.HasSuffix(h, "."+b) {
			return true
		}
	}
	return false
}

func (s *Server) handleConnect(conn net.Conn, br *bufio.Reader, req *http.Request) {
	host, port := splitHostPort(req.Host, "443")

	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	// Clients pipeline: the TLS ClientHello frequently arrives immediately behind
	// the CONNECT line and is already sitting in br. Continuing to read through br
	// replays those bytes; reading the socket directly would lose them and stall
	// the handshake forever.
	var client net.Conn = &readerConn{Conn: conn, r: br}
	s.log.Debug("connect established", "host", host, "port", port, "buffered", br.Buffered())
	if s.log.Enabled(context.Background(), slog.LevelDebug) {
		client = &traceConn{Conn: client, log: s.log, host: host}
	}

	if s.shouldBypass(host) {
		s.log.Debug("bypass (not decrypted)", "host", host)
		s.tunnel(client, net.JoinHostPort(host, port))
		return
	}
	s.intercept(client, host, port)
}

// readerConn reads through r (a buffered reader over Conn) while writes and
// connection control go to Conn directly.
type readerConn struct {
	net.Conn
	r io.Reader
}

func (c *readerConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// traceConn logs byte counts on the intercepted socket. Debug builds only.
type traceConn struct {
	net.Conn
	log  *slog.Logger
	host string
}

func (c *traceConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.log.Debug("client->proxy", "host", c.host, "n", n, "err", err)
	return n, err
}

func (c *traceConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.log.Debug("proxy->client", "host", c.host, "n", n, "err", err)
	return n, err
}

// tunnel splices bytes in both directions without inspecting them.
func (s *Server) tunnel(client net.Conn, addr string) {
	upstream, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		s.log.Debug("tunnel dial failed", "addr", addr, "err", err)
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

// handlePlainHTTP proxies non-CONNECT requests. Toolbox uses HTTPS throughout, so
// this is a courtesy path rather than a hot one.
func (s *Server) handlePlainHTTP(conn net.Conn, req *http.Request) {
	if !req.URL.IsAbs() {
		writeError(conn, http.StatusBadRequest, errors.New("not a proxy request"))
		return
	}
	outReq, err := http.NewRequest(req.Method, req.URL.String(), req.Body)
	if err != nil {
		writeError(conn, http.StatusBadGateway, err)
		return
	}
	outReq.Header = req.Header.Clone()
	stripHopByHop(outReq.Header)

	resp, err := s.client.Do(outReq)
	if err != nil {
		writeError(conn, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()

	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	for k, vs := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("Connection: close\r\n\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return
	}
	_, _ = io.Copy(conn, resp.Body)
}

func splitHostPort(hostport, defPort string) (string, string) {
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		return h, p
	}
	return hostport, defPort
}

