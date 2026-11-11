// Package dhcp is a small DHCPv4 client that lets each simulated device send
// exactly the options its real counterpart sends (hostname, vendor class and
// the parameter request list, in order), which is what fingerprinting reads.
package dhcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	Discover = 1
	Offer    = 2
	Request  = 3
	Decline  = 4
	Ack      = 5
	Nak      = 6
	Release  = 7
)

const (
	optSubnet     = 1
	optRouter     = 3
	optDNS        = 6
	optHostname   = 12
	optRequested  = 50
	optLease      = 51
	optMsgType    = 53
	optServerID   = 54
	optPRL        = 55
	optMaxSize    = 57
	optT1         = 58
	optVendor     = 60
	optClientID   = 61
	optEnd        = 255
	magic         = 0x63825363
	headerLen     = 236
	broadcastFlag = 0x8000
)

// Params is what the device announces about itself.
type Params struct {
	Hostname    string
	VendorClass string
	PRL         []int
	MaxSize     int
	ClientID    bool
}

// Message is a parsed DHCP packet.
type Message struct {
	Op      byte
	XID     uint32
	Secs    uint16
	Flags   uint16
	CIAddr  net.IP
	YIAddr  net.IP
	SIAddr  net.IP
	CHAddr  net.HardwareAddr
	Options map[byte][]byte
}

func (m *Message) Type() byte {
	if v := m.Options[optMsgType]; len(v) == 1 {
		return v[0]
	}
	return 0
}

// Lease is an accepted address.
type Lease struct {
	IP       net.IP
	Mask     net.IPMask
	Router   net.IP
	DNS      []net.IP
	Server   net.IP
	Duration time.Duration
	T1       time.Duration
	Acquired time.Time
}

func (l *Lease) String() string {
	ones, _ := l.Mask.Size()
	return fmt.Sprintf("%s/%d gw %s dns %v lease %s", l.IP, ones, l.Router, l.DNS, l.Duration)
}

// build encodes a client message. reqIP and server may be nil.
func build(kind byte, xid uint32, secs uint16, mac net.HardwareAddr, p *Params, ciaddr, reqIP, server net.IP) []byte {
	b := make([]byte, headerLen, 548)
	b[0] = 1 // BOOTREQUEST
	b[1] = 1 // Ethernet
	b[2] = 6
	binary.BigEndian.PutUint32(b[4:], xid)
	binary.BigEndian.PutUint16(b[8:], secs)
	if ciaddr != nil {
		copy(b[12:16], ciaddr.To4())
	}
	copy(b[28:34], mac)
	b = binary.BigEndian.AppendUint32(b, magic)

	opt := func(code byte, v []byte) {
		if len(v) > 255 {
			v = v[:255]
		}
		b = append(b, code, byte(len(v)))
		b = append(b, v...)
	}
	opt(optMsgType, []byte{kind})
	if p.ClientID {
		opt(optClientID, append([]byte{1}, mac...))
	}
	if reqIP != nil {
		opt(optRequested, reqIP.To4())
	}
	if server != nil {
		opt(optServerID, server.To4())
	}
	if kind != Release && kind != Decline {
		if p.MaxSize > 0 {
			opt(optMaxSize, binary.BigEndian.AppendUint16(nil, uint16(p.MaxSize)))
		}
		if p.VendorClass != "" {
			opt(optVendor, []byte(p.VendorClass))
		}
		if p.Hostname != "" {
			opt(optHostname, []byte(p.Hostname))
		}
		if len(p.PRL) > 0 {
			prl := make([]byte, len(p.PRL))
			for i, c := range p.PRL {
				prl[i] = byte(c)
			}
			opt(optPRL, prl)
		}
	}
	b = append(b, optEnd)
	for len(b) < 300 { // BOOTP minimum; many servers expect it
		b = append(b, 0)
	}
	return b
}

func parse(b []byte) (*Message, error) {
	if len(b) < headerLen+4 {
		return nil, errors.New("short packet")
	}
	if binary.BigEndian.Uint32(b[headerLen:]) != magic {
		return nil, errors.New("no DHCP magic cookie")
	}
	m := &Message{
		Op:      b[0],
		XID:     binary.BigEndian.Uint32(b[4:]),
		Secs:    binary.BigEndian.Uint16(b[8:]),
		Flags:   binary.BigEndian.Uint16(b[10:]),
		CIAddr:  net.IP(append([]byte(nil), b[12:16]...)),
		YIAddr:  net.IP(append([]byte(nil), b[16:20]...)),
		SIAddr:  net.IP(append([]byte(nil), b[20:24]...)),
		CHAddr:  net.HardwareAddr(append([]byte(nil), b[28:34]...)),
		Options: map[byte][]byte{},
	}
	o := b[headerLen+4:]
	for len(o) > 0 {
		code := o[0]
		if code == optEnd {
			break
		}
		if code == 0 {
			o = o[1:]
			continue
		}
		if len(o) < 2 || len(o) < 2+int(o[1]) {
			return nil, errors.New("truncated option")
		}
		m.Options[code] = append(m.Options[code], o[2:2+int(o[1])]...) // concatenate repeats (RFC 3396)
		o = o[2+int(o[1]):]
	}
	return m, nil
}

func ip4(v []byte) net.IP {
	if len(v) < 4 {
		return nil
	}
	return net.IPv4(v[0], v[1], v[2], v[3]).To4()
}

func secs(v []byte) time.Duration {
	if len(v) != 4 {
		return 0
	}
	return time.Duration(binary.BigEndian.Uint32(v)) * time.Second
}

func leaseFrom(m *Message) *Lease {
	l := &Lease{
		IP:       m.YIAddr.To4(),
		Server:   ip4(m.Options[optServerID]),
		Duration: secs(m.Options[optLease]),
		T1:       secs(m.Options[optT1]),
		Acquired: time.Now(),
	}
	if v := m.Options[optSubnet]; len(v) == 4 {
		l.Mask = net.IPMask(append([]byte(nil), v...))
	} else {
		l.Mask = l.IP.DefaultMask()
	}
	l.Router = ip4(m.Options[optRouter])
	for v := m.Options[optDNS]; len(v) >= 4; v = v[4:] {
		l.DNS = append(l.DNS, ip4(v))
	}
	if l.Duration == 0 {
		l.Duration = time.Hour
	}
	if l.T1 == 0 || l.T1 >= l.Duration {
		l.T1 = l.Duration / 2
	}
	if l.Server == nil {
		l.Server = m.SIAddr
	}
	return l
}

// udpIP wraps a DHCP payload in IPv4 and UDP headers (68 -> 67).
func udpIP(src, dst net.IP, payload []byte) []byte {
	return udpIPPorts(src, dst, 68, 67, payload)
}

// udpIPPorts wraps payload in IPv4 + UDP with explicit ports.
func udpIPPorts(src, dst net.IP, sport, dport uint16, payload []byte) []byte {
	const ipLen, udpLen = 20, 8
	total := ipLen + udpLen + len(payload)
	p := make([]byte, total)
	p[0] = 0x45
	p[1] = 0x10 // low delay, as busybox udhcpc sets
	binary.BigEndian.PutUint16(p[2:], uint16(total))
	p[8] = 64
	p[9] = 17
	copy(p[12:16], src.To4())
	copy(p[16:20], dst.To4())
	binary.BigEndian.PutUint16(p[10:], checksum(p[:ipLen], 0))

	u := p[ipLen:]
	binary.BigEndian.PutUint16(u[0:], sport)
	binary.BigEndian.PutUint16(u[2:], dport)
	binary.BigEndian.PutUint16(u[4:], uint16(udpLen+len(payload)))
	copy(u[udpLen:], payload)
	// pseudo-header sum
	var ps uint32
	for i := 12; i < 20; i += 2 {
		ps += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	ps += 17 + uint32(udpLen+len(payload))
	c := checksum(u, ps)
	if c == 0 {
		c = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:], c)
	return p
}

func checksum(b []byte, sum uint32) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// dhcpFromIP returns the DHCP payload of an IPv4/UDP packet to port 68, or nil.
func dhcpFromIP(p []byte) []byte {
	if len(p) < 28 || p[0]>>4 != 4 || p[9] != 17 {
		return nil
	}
	ihl := int(p[0]&0x0f) * 4
	if len(p) < ihl+8 {
		return nil
	}
	u := p[ihl:]
	if binary.BigEndian.Uint16(u[2:]) != 68 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(u[4:]))
	if n < 8 || n > len(u) {
		return nil
	}
	return u[8:n]
}
