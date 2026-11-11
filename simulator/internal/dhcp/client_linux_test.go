package dhcp

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

// TestAcquireOverVeth stands up a veth pair, runs a minimal DHCP server on
// one end, and has the real client acquire a lease on the other. Needs root.
func TestAcquireOverVeth(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root for veth + raw sockets")
	}
	cli, srv, cleanup := vethPair(t)
	defer cleanup()

	// Minimal server: offers 192.168.77.50, gw .1, dns .1, 1h lease.
	offered := net.IPv4(192, 168, 77, 50).To4()
	go serveDHCP(t, srv, offered)

	c := &Client{
		Iface:  cli,
		Params: Params{Hostname: "HIK-TEST", VendorClass: "udhcp 1.31.1", PRL: []int{1, 3, 6, 15}, ClientID: true},
		Log:    func(f string, a ...any) { t.Logf("client: "+f, a...) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	lease, err := c.Acquire(ctx, nil)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !lease.IP.Equal(offered) {
		t.Errorf("leased %s, want %s", lease.IP, offered)
	}
	if lease.Router == nil || !lease.Router.Equal(net.IPv4(192, 168, 77, 1).To4()) {
		t.Errorf("router = %v", lease.Router)
	}
	if lease.Duration != time.Hour {
		t.Errorf("duration = %v", lease.Duration)
	}
	t.Logf("acquired %s", lease)
}

func vethPair(t *testing.T) (client, server *net.Interface, cleanup func()) {
	t.Helper()
	la := netlink.NewLinkAttrs()
	la.Name = "zsvethA"
	veth := &netlink.Veth{LinkAttrs: la, PeerName: "zsvethB"}
	netlink.LinkDel(veth) // clear any leftover
	if err := netlink.LinkAdd(veth); err != nil {
		t.Skipf("cannot create veth (needs CAP_NET_ADMIN): %v", err)
	}
	a, _ := netlink.LinkByName("zsvethA")
	b, _ := netlink.LinkByName("zsvethB")
	netlink.LinkSetUp(a)
	netlink.LinkSetUp(b)
	ci, _ := net.InterfaceByName("zsvethA")
	si, _ := net.InterfaceByName("zsvethB")
	return ci, si, func() { netlink.LinkDel(veth) }
}

// serveDHCP answers one DISCOVER with an OFFER and one REQUEST with an ACK on
// iface, using a raw packet socket (mirrors the client's transport).
func serveDHCP(t *testing.T, iface *net.Interface, yiaddr net.IP) {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, int(htons(syscall.ETH_P_IP)))
	if err != nil {
		t.Logf("server socket: %v", err)
		return
	}
	defer syscall.Close(fd)
	syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: iface.Index})
	tv := syscall.NsecToTimeval(int64(250 * time.Millisecond))
	syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

	server := net.IPv4(192, 168, 77, 1).To4()
	send := func(to net.HardwareAddr, kind byte, xid uint32, mac net.HardwareAddr) {
		b := make([]byte, headerLen)
		b[0] = 2
		b[1] = 1
		b[2] = 6
		binary.BigEndian.PutUint32(b[4:], xid)
		copy(b[16:20], yiaddr)      // yiaddr
		copy(b[20:24], server)      // siaddr
		copy(b[28:34], mac)
		b = binary.BigEndian.AppendUint32(b, magic)
		opt := func(c byte, v []byte) { b = append(b, c, byte(len(v))); b = append(b, v...) }
		opt(optMsgType, []byte{kind})
		opt(optServerID, server)
		opt(optLease, binary.BigEndian.AppendUint32(nil, 3600))
		opt(optSubnet, []byte{255, 255, 255, 0})
		opt(optRouter, server)
		opt(optDNS, server)
		b = append(b, optEnd)
		frame := udpIPPorts(server, net.IPv4bcast, 67, 68, b)
		sa := &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: iface.Index, Halen: 6}
		copy(sa.Addr[:], to)
		syscall.Sendto(fd, frame, 0, sa)
	}

	buf := make([]byte, 2048)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			continue
		}
		d := dhcpToServer(buf[:n])
		if d == nil {
			continue
		}
		m, err := parse(d)
		if err != nil || m.Op != 1 {
			continue
		}
		switch m.Type() {
		case Discover:
			send(m.CHAddr, Offer, m.XID, m.CHAddr)
		case Request:
			send(m.CHAddr, Ack, m.XID, m.CHAddr)
			return
		}
	}
}

// dhcpToServer extracts a DHCP payload destined for port 67 (client->server).
func dhcpToServer(p []byte) []byte {
	if len(p) < 28 || p[0]>>4 != 4 || p[9] != 17 {
		return nil
	}
	ihl := int(p[0]&0x0f) * 4
	if len(p) < ihl+8 {
		return nil
	}
	u := p[ihl:]
	if binary.BigEndian.Uint16(u[2:]) != 67 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(u[4:]))
	if n < 8 || n > len(u) {
		return nil
	}
	return u[8:n]
}
