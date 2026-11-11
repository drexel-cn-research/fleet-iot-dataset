package device

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"syscall"
	"time"

	"iotsim/internal/disc"
	"iotsim/internal/profile"
)

// contact performs one scheduled cloud interaction for endpoint e. The
// device binds its own source IP so the router attributes the flow to the
// right MAC. Bulk payloads never go here; these are small keepalive-style
// touches to the real vendor names, giving the sensor real destinations
// and ASNs. All errors are logged and swallowed: a device that can't reach
// its cloud keeps running, as a real one would.
func (d *Device) contact(ctx context.Context, e profile.Endpoint) {
	host := d.Expand(e.Host)
	switch e.Proto {
	case "dns":
		d.resolve(ctx, host)
	case "udp":
		d.udp(ctx, host, e.Port)
	case "http":
		d.http(ctx, e, host)
	case "tls":
		d.tlsTouch(ctx, e, host)
	}
}

// ntpLoop sends an SNTP time query to the profile's NTP server on its
// schedule. (Profiles defined NTP from the start, but nothing sent it.)
func (d *Device) ntpLoop(ctx context.Context) {
	n := d.Profile.NTP
	if !disc.Sleep(ctx, time.Duration(rand.Int64N(int64(n.Every.D())/4+1))) {
		return
	}
	for {
		d.ntp(ctx, d.Expand(n.Server))
		if !disc.Sleep(ctx, d.jitter(n.Every.D())) {
			return
		}
	}
}

func (d *Device) ntp(ctx context.Context, server string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := d.dialer.DialContext(ctx, "udp4", net.JoinHostPort(server, "123"))
	if err != nil {
		d.logf("ntp %s: %v", server, err)
		return
	}
	defer c.Close()
	req := make([]byte, 48)
	req[0] = 0x23 // LI 0, version 4, mode 3 (client)
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(req); err != nil {
		d.logf("ntp %s: %v", server, err)
		return
	}
	buf := make([]byte, 128)
	if _, err := c.Read(buf); err != nil {
		d.logf("ntp %s: no reply: %v", server, err)
	}
}

func (d *Device) resolve(ctx context.Context, host string) {
	if net.ParseIP(host) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := d.resolver.LookupHost(ctx, host); err != nil {
		d.logf("dns %s: %v", host, err)
	}
}

func (d *Device) udp(ctx context.Context, host string, port int) {
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	// SO_BROADCAST lets the same path send LAN broadcasts (Tuya's :6667
	// announcement) as well as unicast pings.
	lc := net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		rc.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
		return serr
	}}
	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		d.logf("udp %s: %v", addr, err)
		return
	}
	defer pc.Close()
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		d.logf("udp %s: %v", addr, err)
		return
	}
	pc.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := pc.WriteTo(d.keepalive(), ua); err != nil {
		d.logf("udp %s: %v", addr, err)
	}
}

func (d *Device) http(ctx context.Context, e profile.Endpoint, host string) {
	port := e.Port
	if port == 0 {
		port = 80
	}
	url := fmt.Sprintf("http://%s:%d%s", host, port, e.Path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}
	if e.UA != "" {
		req.Header.Set("User-Agent", d.Expand(e.UA))
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		d.logf("http %s: %v", url, err)
		return
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
}

// tlsTouch opens a TLS connection with a ClientHello shaped by the profile's
// flavor (see hello.go), then either holds it (persistent control channels)
// or closes it after the handshake (periodic check-ins).
func (d *Device) tlsTouch(ctx context.Context, e profile.Endpoint, host string) {
	port := e.Port
	if port == 0 {
		port = 443
	}
	// The 20s limit covers connect + handshake only; a persistent channel
	// then lives on the device's own context.
	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := d.dialer.DialContext(hctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		d.logf("dial %s: %v", host, err)
		return
	}
	defer raw.Close()
	conn := d.tlsClient(raw, host)
	if err := conn.HandshakeContext(hctx); err != nil {
		d.logf("tls %s: %v", host, err)
		return
	}
	if !e.Persist {
		return
	}
	// Hold the channel open with small writes until it drops or we stop.
	tick := time.NewTicker(d.jitter(30 * time.Second))
	defer tick.Stop()
	held := make(chan struct{})
	defer close(held)
	go func() {
		select {
		case <-ctx.Done():
			raw.Close()
		case <-held:
		}
	}()
	buf := make([]byte, 256)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Write(d.keepalive()); err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(buf); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
		}
	}
}

func (d *Device) tlsClient(raw net.Conn, host string) *tls.Conn {
	cfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // we only need the handshake to look real; we don't trust the data
		MinVersion:         tls.VersionTLS12,
		NextProtos:         d.alpn(),
		CipherSuites:       d.cipherSuites(),
	}
	switch d.Profile.TLS {
	case "modern", "":
		cfg.CipherSuites = nil // Go's full modern set, TLS 1.3 offered
	default:
		// Constrained stacks: TLS 1.2 only. Without this Go still offers
		// 1.3, where CipherSuites is ignored, and every flavor looks alike.
		cfg.MaxVersion = tls.VersionTLS12
	}
	return tls.Client(raw, cfg)
}
