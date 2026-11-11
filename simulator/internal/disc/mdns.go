// Package disc sends the LAN discovery traffic devices use to announce
// themselves: mDNS/DNS-SD, SSDP (UPnP) and WS-Discovery.
package disc

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"time"
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

const (
	typeA   = 1
	typePTR = 12
	typeTXT = 16
	typeSRV = 33
	classIN = 1
	flush   = 0x8000
)

// Service is one DNS-SD service a device advertises.
type Service struct {
	Type string // "_hue._tcp"
	Port int
	TXT  []string
}

type MDNS struct {
	Iface    *net.Interface
	IP       func() net.IP
	Host     string // "Philips-hue" (".local" added)
	Instance string // "Philips Hue - 1A2B3C"
	Services []Service
	Queries  []string
	Every    time.Duration
	Log      func(string, ...any)
}

func (m *MDNS) Run(ctx context.Context) error {
	conn, err := net.ListenMulticastUDP("udp4", m.Iface, mdnsGroup)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	var mu sync.Mutex
	var last time.Time
	announce := func() {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < time.Second {
			return
		}
		last = time.Now()
		if len(m.Services) > 0 {
			conn.WriteToUDP(m.response(), mdnsGroup)
		}
	}
	query := func() {
		if len(m.Queries) > 0 {
			conn.WriteToUDP(mdnsQuery(m.Queries), mdnsGroup)
		}
	}

	// Answer queries for our names.
	ours := map[string]bool{"_services._dns-sd._udp.local": true, strings.ToLower(m.Host + ".local"): true}
	for _, s := range m.Services {
		ours[strings.ToLower(s.Type+".local")] = true
		ours[strings.ToLower(m.Instance+"."+s.Type+".local")] = true
	}
	go func() {
		buf := make([]byte, 9000)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			for _, q := range questions(buf[:n]) {
				if ours[q] {
					announce()
					break
				}
			}
		}
	}()

	// Probe-style start: announce twice a second apart, as RFC 6762 asks.
	announce()
	query()
	sleep(ctx, 1100*time.Millisecond)
	announce()
	for {
		if !sleep(ctx, Jitter(m.Every)) {
			return nil
		}
		announce()
		query()
	}
}

func (m *MDNS) response() []byte {
	host := m.Host + ".local"
	var rrs []rr
	for _, s := range m.Services {
		inst := m.Instance + "." + s.Type + ".local"
		rrs = append(rrs,
			rr{s.Type + ".local", typePTR, classIN, 4500, encName(inst)},
			rr{"_services._dns-sd._udp.local", typePTR, classIN, 4500, encName(s.Type + ".local")},
			rr{inst, typeSRV, classIN | flush, 120, srv(s.Port, host)},
			rr{inst, typeTXT, classIN | flush, 4500, txt(s.TXT)},
		)
	}
	if m.IP != nil {
		if ip := m.IP(); ip != nil {
			rrs = append(rrs, rr{host, typeA, classIN | flush, 120, ip.To4()})
		}
	}
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[2:], 0x8400) // response, authoritative
	binary.BigEndian.PutUint16(b[6:], uint16(len(rrs)))
	for _, r := range rrs {
		b = append(b, encName(r.name)...)
		b = binary.BigEndian.AppendUint16(b, r.typ)
		b = binary.BigEndian.AppendUint16(b, r.class)
		b = binary.BigEndian.AppendUint32(b, r.ttl)
		b = binary.BigEndian.AppendUint16(b, uint16(len(r.data)))
		b = append(b, r.data...)
	}
	return b
}

type rr struct {
	name       string
	typ, class uint16
	ttl        uint32
	data       []byte
}

func mdnsQuery(types []string) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[4:], uint16(len(types)))
	for _, t := range types {
		b = append(b, encName(t+".local")...)
		b = binary.BigEndian.AppendUint16(b, typePTR)
		b = binary.BigEndian.AppendUint16(b, classIN)
	}
	return b
}

func encName(n string) []byte {
	var b []byte
	for _, l := range strings.Split(strings.TrimSuffix(n, "."), ".") {
		if len(l) > 63 {
			l = l[:63]
		}
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func srv(port int, host string) []byte {
	b := make([]byte, 6)
	binary.BigEndian.PutUint16(b[4:], uint16(port))
	return append(b, encName(host)...)
}

func txt(items []string) []byte {
	if len(items) == 0 {
		return []byte{0}
	}
	var b []byte
	for _, s := range items {
		if len(s) > 255 {
			s = s[:255]
		}
		b = append(b, byte(len(s)))
		b = append(b, s...)
	}
	return b
}

// questions returns the lowercased question names of a DNS query, or nil
// for a response or a malformed packet.
func questions(b []byte) []string {
	if len(b) < 12 || b[2]&0x80 != 0 {
		return nil
	}
	qd := int(binary.BigEndian.Uint16(b[4:]))
	off := 12
	var out []string
	for i := 0; i < qd; i++ {
		name, next, ok := readName(b, off)
		if !ok || next+4 > len(b) {
			return out
		}
		out = append(out, strings.ToLower(name))
		off = next + 4
	}
	return out
}

func readName(b []byte, off int) (string, int, bool) {
	var labels []string
	next := -1
	for hops := 0; hops < 32; hops++ {
		if off >= len(b) {
			return "", 0, false
		}
		l := int(b[off])
		switch {
		case l == 0:
			if next < 0 {
				next = off + 1
			}
			return strings.Join(labels, "."), next, true
		case l&0xc0 == 0xc0:
			if off+1 >= len(b) {
				return "", 0, false
			}
			if next < 0 {
				next = off + 2
			}
			off = int(binary.BigEndian.Uint16(b[off:]) & 0x3fff)
		default:
			if off+1+l > len(b) {
				return "", 0, false
			}
			labels = append(labels, string(b[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, false
}
