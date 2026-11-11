package device

import (
	"context"
	"net"
	"net/http"
	"time"

	"iotsim/internal/disc"
)

// discovery launches whichever LAN announcement protocols the profile has.
func (d *Device) discovery(ctx context.Context) {
	p := d.Profile
	ipFn := func() net.IP { return d.IP() }

	if p.MDNS != nil {
		var svcs []disc.Service
		for _, s := range p.MDNS.Services {
			svcs = append(svcs, disc.Service{Type: s.Type, Port: s.Port, TXT: d.expandAll(s.TXT)})
		}
		m := &disc.MDNS{
			Iface: d.iface, IP: ipFn, Host: d.Hostname,
			Instance: d.Expand(p.MDNS.Instance), Services: svcs,
			Queries: p.MDNS.Queries, Every: p.MDNS.Every.D(), Log: d.logf,
		}
		go m.Run(ctx)
	}

	if d.ssdp != nil { // built by buildSSDP before any goroutine starts
		go d.ssdp.Run(ctx)
	}

	if p.WSD != nil {
		xaddr := "/onvif/device_service"
		if p.Class == "printer" {
			xaddr = "/wsd/device"
		}
		w := &disc.WSD{
			Iface: d.iface, IP: ipFn, UUID: d.UUID(),
			Types: p.WSD.Types, Scopes: p.WSD.Scopes, XAddr: xaddr,
			Every: p.WSD.Every.D(), Log: d.logf,
		}
		go w.Run(ctx)
	}

	<-ctx.Done()
}

// buildSSDP prepares the SSDP announcer. Run calls it before launching
// discovery and serveLAN, so serveLAN always sees it (previously the two
// raced and description.xml could go unregistered).
func (d *Device) buildSSDP() {
	p := d.Profile.SSDP
	if p == nil {
		return
	}
	d.ssdp = &disc.SSDP{
		Iface: d.iface, IP: d.IP, UUID: d.UUID(),
		Server: d.Expand(p.Server), Types: p.Types, Port: p.Port,
		FriendlyName: d.Expand(p.FriendlyName), Manufacturer: p.Manufacturer,
		ModelName: p.ModelName, Serial: d.Serial, Search: p.Search,
		Every: p.Every.D(), Log: d.logf,
	}
}

func (d *Device) expandAll(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = d.Expand(s)
	}
	return out
}

// serveLAN opens the TCP ports the device would serve on (HTTP, RTSP, ECP),
// so a LAN scan sees open ports characteristic of the device. The SSDP
// description is served on its port.
func (d *Device) serveLAN(ctx context.Context) {
	mux := http.NewServeMux()
	if d.ssdp != nil {
		mux.HandleFunc("/description.xml", d.ssdp.Description)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", d.Profile.Vendor)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(d.Profile.Model))
	})

	for _, port := range d.Profile.Listen {
		port := port
		// All addresses: the namespace holds only this device's address,
		// and binding it explicitly would break after a lease change.
		ln, err := net.ListenTCP("tcp4", &net.TCPAddr{Port: port})
		if err != nil {
			d.logf("listen :%d: %v", port, err)
			continue
		}
		go func() { <-ctx.Done(); ln.Close() }()
		switch port {
		case 80, 443, 8008, 8060, 8080, 1400, 1914, 3000, 631:
			srv := &http.Server{Handler: mux}
			go srv.Serve(ln)
		default:
			go acceptQuiet(ctx, ln) // open port, no banter
		}
	}
	<-ctx.Done()
}

func acceptQuiet(ctx context.Context, ln *net.TCPListener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			// Read one request, then hang up; holding every connection
			// until shutdown leaked one goroutine and socket per connect.
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 512)
			c.Read(buf)
		}()
	}
}
