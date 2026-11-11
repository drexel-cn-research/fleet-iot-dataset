package device

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"time"

	"iotsim/internal/disc"
	"iotsim/internal/profile"
)

// The sink protocol is deliberately trivial so the sink side is simple: the
// device opens a TCP connection and sends a 32-byte header, then the payload.
//
//	magic  [4]  "ZSNK"
//	kind   [1]  1=upload (device->sink), 2=stream (sink->device)
//	_      [3]  padding
//	namelen[2]  device name length
//	bytes  [8]  payload length to transfer
//	name   [namelen]
//
// For an upload the device then writes `bytes` bytes. For a stream the sink
// writes `bytes` bytes back and the device reads them.
const sinkMagic = "ZSNK"

// Sink transfer kinds.
const (
	SinkUpload = 1
	SinkStream = 2
)

// SinkHeader encodes the header that opens every sink connection.
func SinkHeader(kind byte, name string, n uint64) []byte {
	h := make([]byte, 32, 32+len(name))
	copy(h, sinkMagic)
	h[4] = kind
	binary.BigEndian.PutUint16(h[8:], uint16(len(name)))
	binary.BigEndian.PutUint64(h[16:], n)
	return append(h, name...)
}

func (d *Device) dialSink(ctx context.Context, kind byte, n uint64) (net.Conn, error) {
	c, err := d.dialer.DialContext(ctx, "tcp", d.Sink)
	if err != nil {
		return nil, err
	}
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(SinkHeader(kind, d.Name, n)); err != nil {
		c.Close()
		return nil, err
	}
	// The deadline guards the header only; clear it so long transfers
	// (big clips over a Wi-Fi-attached sink) aren't cut off.
	c.SetWriteDeadline(time.Time{})
	return c, nil
}

// uploadLoop sends event-driven uploads (camera clips, logs) at a Poisson
// rate, within the profile's active hours.
func (d *Device) uploadLoop(ctx context.Context) {
	u := d.Profile.Upload
	for {
		rate := u.PerHour * d.Speed
		if !disc.Sleep(ctx, poisson(rate)) {
			return
		}
		if !profile.InHours(u.Hours, time.Now().Hour()) {
			continue
		}
		size := uint64(u.MinKB+rand.IntN(max(1, u.MaxKB-u.MinKB))) * 1024
		d.sendUpload(ctx, size)
	}
}

func (d *Device) sendUpload(ctx context.Context, size uint64) {
	d.setState("active")
	defer d.setState("idle")
	c, err := d.dialSink(ctx, 1, size)
	if err != nil {
		d.logf("upload: %v", err)
		return
	}
	defer c.Close()
	n, err := copyN(ctx, c, size, 32*1024)
	d.addTx(n)
	d.mu.Lock()
	d.stats.Uploads++
	d.mu.Unlock()
	if err != nil {
		d.logf("upload (%d/%d): %v", n, size, err)
	}
}

// streamLoop runs viewing sessions: at session start it touches the stream's
// vendor domains (real DNS/TLS), then downloads from the sink at the
// profile's bitrate for the session length.
func (d *Device) streamLoop(ctx context.Context) {
	s := d.Profile.Stream
	// Sessions only start inside the active hours, so spread the daily count
	// over those hours. (Spreading it over 24 and dropping the draws outside
	// the window cut a 2-per-day, 5-hour TV down to ~0.4 sessions a day.)
	span := float64(profile.HoursSpan(s.Hours))
	for {
		rate := s.PerDay / span * d.Speed
		if !disc.Sleep(ctx, poisson(rate)) {
			return
		}
		if !profile.InHours(s.Hours, time.Now().Hour()) {
			continue
		}
		for _, host := range s.Domains {
			d.tlsTouch(ctx, profile.Endpoint{Host: host, Port: 443, Proto: "tls"}, d.Expand(host))
		}
		mins := s.MinMin + rand.IntN(max(1, s.MaxMin-s.MinMin))
		size := uint64(s.Mbps * 1e6 / 8 * float64(mins) * 60)
		d.recvStream(ctx, size)
	}
}

func (d *Device) recvStream(ctx context.Context, size uint64) {
	d.setState("active")
	defer d.setState("idle")
	c, err := d.dialSink(ctx, 2, size)
	if err != nil {
		d.logf("stream: %v", err)
		return
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Hour))
	n, err := io.CopyN(io.Discard, newThrottled(ctx, c, d.Profile.Stream.Mbps), int64(size))
	d.addRx(uint64(n))
	d.mu.Lock()
	d.stats.Streams++
	d.mu.Unlock()
	if err != nil && err != io.EOF {
		d.logf("stream (%d/%d): %v", n, size, err)
	}
}

// copyN writes size bytes of filler to w in chunks, stopping on ctx.
func copyN(ctx context.Context, w io.Writer, size uint64, chunk int) (uint64, error) {
	buf := fill(make([]byte, chunk))
	var sent uint64
	for sent < size {
		select {
		case <-ctx.Done():
			return sent, ctx.Err()
		default:
		}
		n := chunk
		if rem := size - sent; rem < uint64(chunk) {
			n = int(rem)
		}
		m, err := w.Write(buf[:n])
		sent += uint64(m)
		if err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// poisson returns the wait until the next event for a mean rate in events
// per hour. A zero or negative rate waits ~forever.
func poisson(perHour float64) time.Duration {
	if perHour <= 0 {
		return 24 * time.Hour
	}
	u := rand.Float64()
	if u < 1e-9 {
		u = 1e-9
	}
	hours := -math.Log(u) / perHour
	return time.Duration(hours * float64(time.Hour))
}

// throttled paces reads to mbps. It sleeps until the bytes read so far are
// due at that rate, rather than a fixed pause per Read: a TCP Read often
// returns far less than the buffer, and a fixed pause then held a 6 Mbps
// stream to ~1.3 Mbps.
type throttled struct {
	ctx   context.Context
	r     io.Reader
	rate  float64 // bytes per second
	start time.Time
	n     float64
}

func newThrottled(ctx context.Context, r io.Reader, mbps float64) *throttled {
	return &throttled{ctx: ctx, r: r, rate: mbps * 1e6 / 8}
}

func (t *throttled) Read(p []byte) (int, error) {
	if err := t.ctx.Err(); err != nil {
		return 0, err
	}
	if t.start.IsZero() {
		t.start = time.Now()
	}
	// At most ~50ms of data per Read, so pacing stays smooth.
	if lim := max(1, int(t.rate*0.05)); len(p) > lim {
		p = p[:lim]
	}
	n, err := t.r.Read(p)
	t.n += float64(n)
	due := t.start.Add(time.Duration(t.n / t.rate * float64(time.Second)))
	if d := time.Until(due); d > 0 {
		select {
		case <-time.After(d):
		case <-t.ctx.Done():
			return n, t.ctx.Err()
		}
	}
	return n, err
}
