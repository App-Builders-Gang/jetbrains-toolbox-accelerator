package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// accelerate serves [start,end] by fetching it as many concurrent ranged GETs and
// re-serialising them into a single ordered response.
//
// Correctness requirement: the bytes written to the client must be identical to a
// plain sequential download, because Toolbox verifies a SHA-256 over the result.
// Segments are therefore emitted strictly in order and any segment that cannot be
// fetched completely aborts the transfer rather than leaving a hole.
func (s *Server) accelerate(out net.Conn, req *http.Request, rawURL string,
	info *objectInfo, start, end int64, isRange bool) error {

	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)

	span := end - start + 1
	segs := planSegments(start, end, s.cfg.SegmentSize)
	name := path.Base(strings.SplitN(rawURL, "?", 2)[0])

	s.log.Info("accelerating",
		"file", name,
		"size", humanBytes(span),
		"segments", len(segs),
		"workers", s.cfg.Workers)

	if err := writeAcceleratedHeader(out, info, start, end, span, isRange); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	results := make([]chan []byte, len(segs))
	for i := range results {
		results[i] = make(chan []byte, 1)
	}

	// Bounds how many fetched-but-unwritten segments may exist at once, capping
	// memory at MaxBuffered*SegmentSize regardless of file size.
	slots := make(chan struct{}, s.cfg.MaxBuffered)
	work := make(chan int)

	var (
		errOnce sync.Once
		fetchErr error
	)
	fail := func(err error) {
		errOnce.Do(func() {
			fetchErr = err
			cancel()
		})
	}

	go func() {
		defer close(work)
		for i := range segs {
			select {
			case work <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < s.cfg.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				data, err := s.fetchSegment(ctx, req, rawURL, segs[idx])
				if err != nil {
					if ctx.Err() == nil {
						fail(fmt.Errorf("segment %d [%d-%d]: %w", idx, segs[idx].start, segs[idx].end, err))
					}
					return
				}
				select {
				case results[idx] <- data:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	begun := time.Now()
	var sent int64
	var writeErr error

	for i := range segs {
		select {
		case data := <-results[i]:
			if _, err := out.Write(data); err != nil {
				writeErr = err
				fail(err)
			}
			sent += int64(len(data))
			<-slots // release the buffer slot now that the bytes are gone
			s.touch()
		case <-ctx.Done():
			writeErr = fetchErr
			if writeErr == nil {
				writeErr = ctx.Err()
			}
		}
		if writeErr != nil {
			break
		}
	}

	cancel()
	wg.Wait()

	if writeErr != nil {
		s.log.Warn("transfer aborted",
			"file", name, "sent", humanBytes(sent), "err", writeErr)
		return writeErr
	}

	dur := time.Since(begun)
	s.log.Info("delivered",
		"file", name,
		"size", humanBytes(sent),
		"took", dur.Round(time.Millisecond).String(),
		"rate", humanBytes(int64(float64(sent)/dur.Seconds()))+"/s")
	return nil
}

type segmentSpan struct{ start, end int64 }

func planSegments(start, end, size int64) []segmentSpan {
	var out []segmentSpan
	for pos := start; pos <= end; pos += size {
		last := pos + size - 1
		if last > end {
			last = end
		}
		out = append(out, segmentSpan{pos, last})
	}
	return out
}

// fetchSegment retrieves one byte range, retrying transient failures. A short
// read is treated as a failure: a truncated segment would silently corrupt the
// reassembled file.
func (s *Server) fetchSegment(ctx context.Context, orig *http.Request, rawURL string, seg segmentSpan) ([]byte, error) {
	want := int(seg.end - seg.start + 1)
	var lastErr error

	for attempt := 0; attempt < DefaultSegmentTries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header = orig.Header.Clone()
		stripHopByHop(req.Header)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", seg.start, seg.end))
		req.Header.Set("Accept-Encoding", "identity")

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusPartialContent {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			lastErr = fmt.Errorf("expected 206, got %d", resp.StatusCode)
			continue
		}

		buf := make([]byte, want)
		_, err = io.ReadFull(resp.Body, buf)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("short segment: %w", err)
			continue
		}
		return buf, nil
	}
	return nil, lastErr
}

func writeAcceleratedHeader(out net.Conn, info *objectInfo, start, end, span int64, isRange bool) error {
	status := http.StatusOK
	if isRange {
		status = http.StatusPartialContent
	}

	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))

	// Carry the origin's headers through, minus the ones we are re-deriving.
	skip := map[string]bool{
		"content-length": true, "content-range": true,
		"accept-ranges": true, "connection": true, "content-encoding": true,
	}
	for k, vs := range info.header {
		if isHopByHop(k) || skip[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", span)
	b.WriteString("Accept-Ranges: bytes\r\n")
	if isRange {
		fmt.Fprintf(&b, "Content-Range: bytes %d-%d/%d\r\n", start, end, info.size)
	}
	b.WriteString("Connection: close\r\n\r\n")

	_, err := io.WriteString(out, b.String())
	return err
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
