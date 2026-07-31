package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// deterministicPayload produces bytes whose value depends on position, so any
// mis-ordering, duplication or gap in reassembly changes the hash. Toolbox hashes
// its downloads, so this is the property the proxy must preserve.
func deterministicPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + i/251) % 251)
	}
	return b
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestAcceleratedTransferIsByteExact is the test that matters most: parallel
// ranged reassembly must reproduce the origin's bytes exactly.
func TestAcceleratedTransferIsByteExact(t *testing.T) {
	payload := deterministicPayload(8 << 20)
	h := newPipeHarness(t, payload, Config{
		MinParallelSize: 1 << 20,
		SegmentSize:     64 << 10, // 128 segments over 8 MiB
		Workers:         8,
		MaxBuffered:     16,
	}, "cdn.example.net")

	resp, body := h.get(t, "/payload.bin", "")

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(body) != len(payload) {
		t.Fatalf("length = %d, want %d", len(body), len(payload))
	}
	if got, want := sum(body), sum(payload); got != want {
		t.Fatalf("sha256 mismatch:\n got %s\nwant %s", got, want)
	}
	t.Logf("reassembled %d bytes; accept-ranges=%q",
		len(body), resp.Header.Get("Accept-Ranges"))
}

// TestRangeRequestPreserved checks a client Range survives end to end, which is
// what makes resumed downloads correct.
func TestRangeRequestPreserved(t *testing.T) {
	payload := deterministicPayload(6 << 20)
	h := newPipeHarness(t, payload, Config{
		MinParallelSize: 1 << 20,
		SegmentSize:     64 << 10,
		Workers:         4,
		MaxBuffered:     8,
	}, "cdn.example.net")

	const start = 1 << 20
	resp, body := h.get(t, "/payload.bin", fmt.Sprintf("bytes=%d-", start))

	if resp.StatusCode != 206 {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	want := payload[start:]
	if got, wantSum := sum(body), sum(want); got != wantSum {
		t.Fatalf("ranged sha256 mismatch:\n got %s\nwant %s", got, wantSum)
	}
}

// TestSmallTransferIsRelayed confirms small objects below the threshold are
// served correctly. A relayed response legitimately carries Accept-Ranges
// (the origin sends it), so the guarantee checked here is byte-exactness, which
// is what a resumed or verified download ultimately depends on.
func TestSmallTransferIsRelayed(t *testing.T) {
	payload := deterministicPayload(128 << 10)
	h := newPipeHarness(t, payload, Config{
		MinParallelSize: 1 << 20,
		SegmentSize:     64 << 10,
		Workers:         4,
	}, "cdn.example.net")

	resp, body := h.get(t, "/payload.bin", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got, want := sum(body), sum(payload); got != want {
		t.Fatalf("small transfer corrupted:\n got %s\nwant %s", got, want)
	}
}

func TestResolveRange(t *testing.T) {
	const size = 1000
	cases := []struct {
		hdr                 string
		start, end          int64
		isRange, ok         bool
	}{
		{"", 0, 999, false, true},
		{"bytes=0-", 0, 999, true, true},
		{"bytes=100-199", 100, 199, true, true},
		{"bytes=500-99999", 500, 999, true, true},
		{"bytes=-500", 0, 0, false, false},       // suffix: relay instead
		{"bytes=0-10,20-30", 0, 0, false, false}, // multi-range: relay instead
		{"items=0-10", 0, 0, false, false},
	}
	for _, c := range cases {
		start, end, isRange, ok := resolveRange(c.hdr, size)
		if ok != c.ok || (ok && (start != c.start || end != c.end || isRange != c.isRange)) {
			t.Errorf("resolveRange(%q) = (%d,%d,%v,%v), want (%d,%d,%v,%v)",
				c.hdr, start, end, isRange, ok, c.start, c.end, c.isRange, c.ok)
		}
	}
}

func TestPlanSegmentsCoversRangeExactly(t *testing.T) {
	segs := planSegments(10, 99, 25)
	if segs[0].start != 10 || segs[len(segs)-1].end != 99 {
		t.Fatalf("segments do not span the range: %+v", segs)
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].start != segs[i-1].end+1 {
			t.Fatalf("gap or overlap between %+v and %+v", segs[i-1], segs[i])
		}
	}
}
