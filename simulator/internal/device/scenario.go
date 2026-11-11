package device

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"iotsim/internal/disc"
	"iotsim/internal/profile"
)

// A scenario is an injected departure from a device's normal behavior, so we
// can check that the sensor's drift detection reacts. Scenarios are coarse
// and self-limiting. Their reach: the LAN-peer scenario touches only the
// other simulated devices on the same Pi; the upload ones go to the sink;
// newdest makes plain TLS handshakes to IANA's documentation domains.
type scenario struct {
	kind string
	arg  string
}

// Scenarios names the available misbehaviors for the control API.
var Scenarios = []string{"scan", "beacon", "exfil", "newdest"}

// Inject queues a scenario; returns an error for an unknown kind.
func (d *Device) Inject(kind, arg string) error {
	for _, k := range Scenarios {
		if k == kind {
			select {
			case d.scen <- scenario{kind, arg}:
				return nil
			default:
				return fmt.Errorf("scenario queue full")
			}
		}
	}
	return fmt.Errorf("unknown scenario %q (have: %v)", kind, Scenarios)
}

func (d *Device) scenarioLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-d.scen:
			d.logf("scenario: %s %s", s.kind, s.arg)
			switch s.kind {
			case "scan":
				d.doScan(ctx, s.arg)
			case "beacon":
				d.doBeacon(ctx)
			case "exfil":
				d.doExfil(ctx)
			case "newdest":
				d.doNewDest(ctx)
			}
		}
	}
}

// doScan connects to a fixed list of LAN peers so the detector sees a jump
// in lan_peers. It only touches the addresses in targets (comma-separated
// IPs): the supervisor fills these with the other simulated devices on the
// same Pi, so it never reaches real hosts on the network.
func (d *Device) doScan(ctx context.Context, targets string) {
	ip := d.IP()
	var hosts []net.IP
	for _, s := range strings.Split(targets, ",") {
		if t := net.ParseIP(strings.TrimSpace(s)); t != nil && !t.Equal(ip) {
			hosts = append(hosts, t)
		}
	}
	if len(hosts) == 0 {
		d.logf("scan: no simulated peers given; nothing to do")
		return
	}
	ports := []int{22, 23, 80, 443, 554, 2323, 7547, 8080, 9999}
	quick := &net.Dialer{Timeout: 500 * time.Millisecond}
	for _, target := range hosts {
		if ctx.Err() != nil {
			return
		}
		for _, p := range ports {
			c, err := quick.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", target, p))
			if err == nil {
				c.Close()
			}
		}
		disc.Sleep(ctx, 10*time.Millisecond)
	}
}

// doBeacon opens a short connection to the sink every 30s for 10 minutes: a
// rigidly periodic pattern, unlike the device's normal jittered cadence,
// which timing-based drift detection should notice.
func (d *Device) doBeacon(ctx context.Context) {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		c, err := d.dialSink(ctx, 1, 256)
		if err == nil {
			copyN(ctx, c, 256, 256)
			c.Close()
			d.addTx(256)
		}
		if !disc.Sleep(ctx, 30*time.Second) {
			return
		}
	}
}

// doExfil sends one large upload well outside the device's normal size and
// hours: a data-theft burst.
func (d *Device) doExfil(ctx context.Context) {
	size := uint64(50+rand.IntN(150)) * 1024 * 1024 // 50-200 MB
	d.sendUpload(ctx, size)
}

// doNewDest makes brief TLS handshakes to names no device profile uses, so
// the detector sees destinations new to this device. The names are IANA's
// reserved documentation domains, which exist for exactly this kind of
// harmless use. (The previous version only reached the sink, which every
// device already talks to, so it raised nothing.)
func (d *Device) doNewDest(ctx context.Context) {
	for _, host := range []string{"example.com", "example.net", "example.org", "www.iana.org"} {
		if ctx.Err() != nil {
			return
		}
		d.tlsTouch(ctx, profile.Endpoint{Host: host, Port: 443, Proto: "tls"}, host)
		disc.Sleep(ctx, 2*time.Second)
	}
}
