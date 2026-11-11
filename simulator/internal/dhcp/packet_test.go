package dhcp

import (
	"net"
	"testing"
)

func TestBuildParseRoundTrip(t *testing.T) {
	mac, _ := net.ParseMAC("08:54:11:aa:bb:cc")
	p := &Params{
		Hostname:    "HIKVISION-AABBCC",
		VendorClass: "udhcp 1.31.1",
		PRL:         []int{1, 3, 6, 15, 28, 33, 42},
		MaxSize:     576,
		ClientID:    true,
	}
	raw := build(Discover, 0x12345678, 0, mac, p, nil, net.IPv4(192, 168, 50, 77), nil)

	m, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Type() != Discover {
		t.Fatalf("type = %d, want Discover", m.Type())
	}
	if m.XID != 0x12345678 {
		t.Fatalf("xid = %x", m.XID)
	}
	if got := string(m.Options[optHostname]); got != p.Hostname {
		t.Errorf("hostname = %q", got)
	}
	if got := string(m.Options[optVendor]); got != p.VendorClass {
		t.Errorf("vendor class = %q", got)
	}
	prl := m.Options[optPRL]
	if len(prl) != len(p.PRL) {
		t.Fatalf("prl len = %d, want %d", len(prl), len(p.PRL))
	}
	for i, c := range p.PRL {
		if int(prl[i]) != c {
			t.Errorf("prl[%d] = %d, want %d (order must be preserved)", i, prl[i], c)
		}
	}
	cid := m.Options[optClientID]
	if len(cid) != 7 || cid[0] != 1 || net.HardwareAddr(cid[1:]).String() != mac.String() {
		t.Errorf("client id = %x", cid)
	}
	if req := m.Options[optRequested]; net.IP(req).To4().String() != "192.168.50.77" {
		t.Errorf("requested ip = %v", net.IP(req))
	}
}

func TestUDPWrapAndExtract(t *testing.T) {
	mac, _ := net.ParseMAC("2c:aa:8e:11:22:33")
	payload := build(Discover, 1, 0, mac, &Params{Hostname: "wyze", PRL: []int{1}}, nil, nil, nil)
	frame := udpIP(net.IPv4zero, net.IPv4bcast, payload)

	// Checksums: recompute the IP header checksum over the header; must be 0.
	if c := checksum(frame[:20], 0); c != 0 {
		t.Errorf("IP header checksum invalid: %x", c)
	}
	// The embedded payload (after 20-byte IP + 8-byte UDP) must parse.
	if _, err := parse(frame[28:]); err != nil {
		t.Fatalf("parse embedded payload: %v", err)
	}

	// dhcpFromIP extracts server->client replies (dst port 68). Build one.
	reply := udpIPPorts(net.IPv4(192, 168, 50, 1), net.IPv4(192, 168, 50, 77), 67, 68, payload)
	got := dhcpFromIP(reply)
	if got == nil {
		t.Fatal("dhcpFromIP returned nil for a reply to port 68")
	}
	if _, err := parse(got); err != nil {
		t.Fatalf("parse extracted: %v", err)
	}
}

func TestLeaseDefaults(t *testing.T) {
	m := &Message{
		YIAddr:  net.IPv4(192, 168, 50, 77),
		Options: map[byte][]byte{optMsgType: {Ack}},
	}
	l := leaseFrom(m)
	if l.Duration == 0 || l.T1 == 0 || l.T1 >= l.Duration {
		t.Errorf("bad lease timers: dur=%v t1=%v", l.Duration, l.T1)
	}
	if ones, _ := l.Mask.Size(); ones == 0 {
		t.Errorf("mask not defaulted")
	}
}
