package proxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// hopByHop headers must not be forwarded (RFC 7230 6.1).
var hopByHop = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailers", "Transfer-Encoding", "Upgrade", "Proxy-Connection",
}

func stripHopByHop(h http.Header) {
	for _, k := range hopByHop {
		h.Del(k)
	}
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isHopByHop(k string) bool {
	for _, h := range hopByHop {
		if strings.EqualFold(k, h) {
			return true
		}
	}
	return false
}

// intercept terminates TLS with a minted certificate and serves the requests the
// client sends inside the tunnel.
func (s *Server) intercept(client net.Conn, host, port string) {
	s.log.Debug("intercept: minting leaf", "host", host)
	cert, err := s.cfg.CA.LeafFor(host)
	if err != nil {
		s.log.Warn("mint certificate failed", "host", host, "err", err)
		return
	}
	s.log.Debug("intercept: leaf ready, starting handshake", "host", host)

	tlsConn := tls.Server(client, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"}, // never negotiate h2 with the client
	})
	if err := tlsConn.Handshake(); err != nil {
		// Overwhelmingly means the client does not trust our CA yet.
		s.log.Debug("client TLS handshake failed", "host", host, "err", err)
		return
	}
	defer tlsConn.Close()

	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Debug("read request", "host", host, "err", err)
			}
			return
		}
		s.touch()

		keepAlive := s.serveIntercepted(tlsConn, req, host, port)
		_ = req.Body.Close()
		if !keepAlive {
			return
		}
	}
}

// serveIntercepted handles one request inside the TLS tunnel. It returns whether
// the connection may be reused.
func (s *Server) serveIntercepted(out net.Conn, req *http.Request, host, port string) bool {
	target := &url.URL{Scheme: "https", Host: net.JoinHostPort(host, port), Opaque: ""}
	if port == "443" {
		target.Host = host
	}
	full := *target
	full.Path = req.URL.Path
	full.RawQuery = req.URL.RawQuery

	if req.Method != http.MethodGet {
		return s.relay(out, req, full.String())
	}

	// A GET is a candidate. Probe cheaply for size and range support before
	// committing to the parallel path.
	info, err := s.probe(req, full.String())
	if err != nil {
		s.log.Debug("probe failed, relaying", "url", full.String(), "err", err)
		return s.relay(out, req, full.String())
	}

	start, end, isRange, ok := resolveRange(req.Header.Get("Range"), info.size)
	if !ok {
		return s.relay(out, req, full.String())
	}
	span := end - start + 1

	if !info.acceptsRanges || span < s.cfg.MinParallelSize {
		return s.relay(out, req, full.String())
	}

	if err := s.accelerate(out, req, full.String(), info, start, end, isRange); err != nil {
		s.log.Warn("acceleration failed", "url", full.String(), "err", err)
	}
	return false // we always close after an accelerated response
}

// relay forwards a request upstream verbatim and streams the response back.
func (s *Server) relay(out net.Conn, req *http.Request, rawURL string) bool {
	outReq, err := http.NewRequest(req.Method, rawURL, req.Body)
	if err != nil {
		writeError(out, http.StatusBadGateway, err)
		return false
	}
	outReq.Header = req.Header.Clone()
	stripHopByHop(outReq.Header)
	outReq.Header.Del("Accept-Encoding") // let Go negotiate; we do not rewrite bodies here

	resp, err := s.client.Do(outReq)
	if err != nil {
		writeError(out, http.StatusBadGateway, err)
		return false
	}
	defer resp.Body.Close()

	// Write the response ourselves so we control framing precisely.
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
	if _, err := io.WriteString(out, b.String()); err != nil {
		return false
	}
	if req.Method != http.MethodHead {
		_, _ = io.Copy(out, resp.Body)
	}
	return false
}

type objectInfo struct {
	size          int64
	acceptsRanges bool
	header        http.Header
	status        int
}

// probe issues a one-byte ranged GET to learn the object's size and whether the
// origin honours ranges. A HEAD is not used: some CDNs answer HEAD differently
// from GET, and a 206 response is direct proof that ranges work.
func (s *Server) probe(req *http.Request, rawURL string) (*objectInfo, error) {
	outReq, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	outReq.Header = req.Header.Clone()
	stripHopByHop(outReq.Header)
	outReq.Header.Set("Range", "bytes=0-0")
	// Ranged reassembly requires unencoded bytes.
	outReq.Header.Set("Accept-Encoding", "identity")

	resp, err := s.client.Do(outReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}()

	info := &objectInfo{header: resp.Header.Clone(), status: resp.StatusCode}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		total, err := totalFromContentRange(resp.Header.Get("Content-Range"))
		if err != nil {
			return nil, err
		}
		info.size, info.acceptsRanges = total, true
	case http.StatusOK:
		info.size = resp.ContentLength
	default:
		return nil, fmt.Errorf("probe status %d", resp.StatusCode)
	}
	if info.size <= 0 {
		return nil, errors.New("unknown object size")
	}
	return info, nil
}

// totalFromContentRange parses the total length out of "bytes 0-0/12345".
func totalFromContentRange(cr string) (int64, error) {
	i := strings.LastIndex(cr, "/")
	if i < 0 {
		return 0, fmt.Errorf("malformed Content-Range %q", cr)
	}
	tail := strings.TrimSpace(cr[i+1:])
	if tail == "*" {
		return 0, errors.New("origin will not disclose total length")
	}
	return strconv.ParseInt(tail, 10, 64)
}

// resolveRange turns a client Range header into absolute bounds. Suffix ranges
// ("bytes=-500") are reported unsupported so the caller relays them verbatim.
func resolveRange(header string, size int64) (start, end int64, isRange, ok bool) {
	if header == "" {
		return 0, size - 1, false, true
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false, false // multi-range: not worth accelerating
	}
	lo, hi, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found || lo == "" {
		return 0, 0, false, false
	}
	start, err := strconv.ParseInt(lo, 10, 64)
	if err != nil {
		return 0, 0, false, false
	}
	end = size - 1
	if hi != "" {
		if end, err = strconv.ParseInt(hi, 10, 64); err != nil {
			return 0, 0, false, false
		}
	}
	if end > size-1 {
		end = size - 1
	}
	if start > end || start < 0 {
		return 0, 0, false, false
	}
	return start, end, true, true
}

func writeError(out net.Conn, code int, err error) {
	body := err.Error()
	fmt.Fprintf(out, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}
