// Package device runs one simulated IoT device: it holds a DHCP lease, sends
// the device's discovery announcements, makes its scheduled cloud contacts,
// and generates its upload/stream traffic to the sink. Each device runs
// inside its own network namespace on its own macvlan interface, so from the
// router's view it is a distinct host with its own MAC and IP.
package device

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"iotsim/internal/dhcp"
	"iotsim/internal/disc"
	"iotsim/internal/profile"
)

// Device is a resolved profile instance plus its live runtime state.
type Device struct {
	*profile.Device
	Sink  string  // host:port of the sink host
	Speed float64 // event-rate multiplier

	iface      *net.Interface
	dialer     *net.Dialer
	resolver   *net.Resolver
	httpClient *http.Client

	mu     sync.RWMutex
	lease  *dhcp.Lease
	state  string // "booting","idle","active","offline"
	stats  Stats
	logf   func(string, ...any)
	scen   chan scenario
	ssdp   *disc.SSDP
}

// Stats is a device's running counters, reported over the control API.
type Stats struct {
	TxBytes   uint64    `json:"tx_bytes"`
	RxBytes   uint64    `json:"rx_bytes"`
	Uploads   uint64    `json:"uploads"`
	Streams   uint64    `json:"streams"`
	CloudHits uint64    `json:"cloud_hits"`
	LastEvent time.Time `json:"last_event"`
}

// Status is the control-API view of a device.
type Status struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Vendor   string `json:"vendor"`
	Model    string `json:"model"`
	Class    string `json:"class"`
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	State    string `json:"state"`
	Stats    Stats  `json:"stats"`
}

// New binds a device to the macvlan interface that already exists in this
// namespace (the supervisor created it). logf is the per-device logger.
func New(pd *profile.Device, iface *net.Interface, sink string, speed float64, logf func(string, ...any)) *Device {
	d := &Device{
		Device: pd,
		Sink:   sink,
		Speed:  speed,
		iface:  iface,
		state:  "booting",
		logf:   logf,
		scen:   make(chan scenario, 4),
	}
	if d.Speed <= 0 {
		d.Speed = 1
	}
	return d
}

// setupNet builds a dialer that binds the device's own address, plus a
// resolver and HTTP client that use it. Called once the lease is up.
func (d *Device) setupNet() {
	// The namespace has one interface and one address, so no source binding
	// is needed. A fixed TCP LocalAddr broke every UDP dial and would go
	// stale if a renew changed the address.
	d.resolver = d.resolverFor()
	d.dialer = &net.Dialer{Timeout: 10 * time.Second, Resolver: d.resolver}
	d.httpClient = &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DialContext:       d.dialer.DialContext,
			DisableKeepAlives: true,
			Proxy:             nil,
		},
	}
}

func (d *Device) resolverFor() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dd := &net.Dialer{Timeout: 5 * time.Second}
			// Resolve against the DNS server the DHCP lease gave us.
			if s := d.dnsServer(); s != "" {
				address = s
			}
			return dd.DialContext(ctx, network, address)
		},
	}
}

func (d *Device) dnsServer() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.lease != nil && len(d.lease.DNS) > 0 {
		return net.JoinHostPort(d.lease.DNS[0].String(), "53")
	}
	return ""
}

// IP is the current leased address, or nil.
func (d *Device) IP() net.IP {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.lease == nil {
		return nil
	}
	return d.lease.IP
}

func (d *Device) setState(s string) {
	d.mu.Lock()
	d.state = s
	d.mu.Unlock()
}

func (d *Device) addTx(n uint64) { d.mu.Lock(); d.stats.TxBytes += n; d.stats.LastEvent = time.Now(); d.mu.Unlock() }
func (d *Device) addRx(n uint64) { d.mu.Lock(); d.stats.RxBytes += n; d.mu.Unlock() }

func (d *Device) Status() Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ip := "-"
	if d.lease != nil {
		ip = d.lease.IP.String()
	}
	return Status{
		Name: d.Name, Type: d.Profile.Type, Vendor: d.Profile.Vendor, Model: d.Profile.Model,
		Class: d.Profile.Class, MAC: d.MAC.String(), IP: ip, Hostname: d.Hostname,
		State: d.state, Stats: d.stats,
	}
}

// jitter returns d scaled by +/-15%.
func (d *Device) jitter(x time.Duration) time.Duration { return disc.Jitter(x) }

// keepalive is a small opaque payload for persistent channels and UDP
// pings: random bytes, since we only need the flow to exist.
func (d *Device) keepalive() []byte {
	n := 8 + rand.IntN(56)
	return fill(make([]byte, n))
}

// fill writes pseudo-random bytes into b (math/rand/v2 has no Read). The
// content is irrelevant; we only need bytes on the wire.
func fill(b []byte) []byte {
	for i := 0; i < len(b); i += 8 {
		var v [8]byte
		binary.LittleEndian.PutUint64(v[:], rand.Uint64())
		copy(b[i:], v[:])
	}
	return b
}

// Run holds the lease and launches every behavior; it returns when ctx ends,
// releasing the lease.
func (d *Device) Run(ctx context.Context) error {
	p := d.Profile
	cl := &dhcp.Client{
		Iface: d.iface,
		Params: dhcp.Params{
			Hostname:    d.Hostname,
			VendorClass: d.Expand(p.DHCP.VendorClass),
			PRL:         p.DHCP.PRL,
			MaxSize:     p.DHCP.MaxSize,
			ClientID:    p.DHCP.ClientID,
		},
		Log: d.logf,
	}
	if err := linkUp(d.iface); err != nil {
		return fmt.Errorf("bring up %s: %w", d.iface.Name, err)
	}
	lease, err := cl.Acquire(ctx, nil)
	if err != nil {
		d.setState("offline")
		return err
	}
	if err := assignIP(d.iface, lease); err != nil {
		d.logf("assign %s: %v", lease.IP, err)
	}
	d.mu.Lock()
	d.lease = lease
	d.mu.Unlock()
	d.setupNet()
	d.setState("idle")
	d.logf("up: %s as %s (%s)", lease.IP, d.Hostname, p.Vendor+" "+p.Model)

	d.buildSSDP()
	var wg sync.WaitGroup
	run := func(f func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); f(ctx) }()
	}

	run(func(c context.Context) { d.renewLoop(c, cl) })
	run(d.discovery)
	run(d.cloudLoop)
	if p.NTP != nil {
		run(d.ntpLoop)
	}
	run(d.serveLAN)
	run(d.scenarioLoop)
	if p.Upload != nil {
		run(d.uploadLoop)
	}
	if p.Stream != nil {
		run(d.streamLoop)
	}

	<-ctx.Done()
	d.setState("offline")
	if l := d.currentLease(); l != nil {
		cl.Release(l)
	}
	wg.Wait()
	return nil
}

func (d *Device) currentLease() *dhcp.Lease {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lease
}

func (d *Device) renewLoop(ctx context.Context, cl *dhcp.Client) {
	for {
		l := d.currentLease()
		if l == nil {
			return
		}
		wait := time.Until(l.Acquired.Add(l.T1))
		if wait < time.Second {
			wait = time.Second
		}
		if !disc.Sleep(ctx, wait) {
			return
		}
		nl, err := cl.Renew(ctx, l)
		if err == dhcp.ErrNak {
			nl, err = cl.Acquire(ctx, nil)
		}
		if err != nil {
			d.logf("renew: %v", err)
			if !disc.Sleep(ctx, 30*time.Second) {
				return
			}
			continue
		}
		if err := assignIP(d.iface, nl); err != nil {
			d.logf("reassign: %v", err)
		}
		d.mu.Lock()
		d.lease = nl
		d.mu.Unlock()
	}
}

func (d *Device) cloudLoop(ctx context.Context) {
	for _, e := range d.Profile.Cloud {
		e := e
		go func() {
			// Stagger first contact so a reboot doesn't burst.
			if !disc.Sleep(ctx, time.Duration(rand.Int64N(int64(e.Every.D())/2+1))) {
				return
			}
			for {
				d.contact(ctx, e)
				d.mu.Lock()
				d.stats.CloudHits++
				d.mu.Unlock()
				if !disc.Sleep(ctx, d.jitter(e.Every.D())) {
					return
				}
			}
		}()
	}
	<-ctx.Done()
}
