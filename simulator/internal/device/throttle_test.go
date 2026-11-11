package device

import (
	"context"
	"io"
	"testing"
	"time"
)

// smallReads returns at most 1000 bytes per Read, like a TCP socket that
// hands back whatever has arrived.
type smallReads struct{}

func (smallReads) Read(p []byte) (int, error) { return min(len(p), 1000), nil }

func TestThrottledHoldsRate(t *testing.T) {
	const mbps = 8 // 1 MB/s
	tr := newThrottled(context.Background(), smallReads{}, mbps)
	start := time.Now()
	n, err := io.CopyN(io.Discard, tr, 400_000)
	if err != nil {
		t.Fatal(err)
	}
	got := float64(n) * 8 / 1e6 / time.Since(start).Seconds()
	if got < 0.8*mbps || got > 1.1*mbps {
		t.Fatalf("throughput %.2f Mbps, want about %d", got, mbps)
	}
}
