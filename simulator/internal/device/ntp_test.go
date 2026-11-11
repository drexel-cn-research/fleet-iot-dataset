package device

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"iotsim/internal/profile"
)

// TestNTPQuery checks the SNTP request format against a local responder.
// Port 123 needs root; skips otherwise.
func TestNTPQuery(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("binding :123 needs root")
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:123")
	if err != nil {
		t.Skipf("cannot bind :123: %v", err)
	}
	defer pc.Close()
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 128)
		pc.SetDeadline(time.Now().Add(3 * time.Second))
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			got <- nil
			return
		}
		reply := make([]byte, 48)
		reply[0] = 0x24 // version 4, mode 4 (server)
		pc.WriteTo(reply, from)
		got <- buf[:n]
	}()
	d := &Device{
		Device: &profile.Device{Profile: &profile.Profile{}},
		dialer: &net.Dialer{Timeout: time.Second},
		logf:   func(f string, a ...any) { t.Logf(f, a...) },
	}
	d.ntp(context.Background(), "127.0.0.1")
	req := <-got
	if len(req) != 48 || req[0] != 0x23 {
		t.Fatalf("bad SNTP request: len=%d first=%#x", len(req), req[0])
	}
}
