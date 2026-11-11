package supervisor

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"iotsim/internal/dhcp"
	"iotsim/internal/profile"
)

// TestDeviceDHCPThroughMacvlan proves the full L2 path: a simulated device on
// a macvlan interface inside its own namespace sends DHCP with its own MAC,
// a server on the far end of a veth sees that distinct host, and the device
// gets a lease. This is the core claim of the simulator: one Pi port, many
// independent-looking hosts. Needs root.
func TestDeviceDHCPThroughMacvlan(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	// veth: A is the device's "uplink" (parent for macvlan); B is the router side.
	la := netlink.NewLinkAttrs()
	la.Name = "zsupA"
	veth := &netlink.Veth{LinkAttrs: la, PeerName: "zsupB"}
	netlink.LinkDel(veth)
	if err := netlink.LinkAdd(veth); err != nil {
		t.Skipf("veth: %v", err)
	}
	defer netlink.LinkDel(veth)
	a, _ := netlink.LinkByName("zsupA")
	b, _ := netlink.LinkByName("zsupB")
	netlink.LinkSetUp(a)
	netlink.LinkSetUp(b)
	srvIf, _ := net.InterfaceByName("zsupB")

	cat, _ := profile.LoadCatalog("")
	fp := writeFleet(t, "zsupA", "wyze-cam-1", "wyze-cam")
	f, _ := profile.LoadFleet(fp)
	devs, _ := f.Resolve(cat)
	d := devs[0]

	sl, err := createSlot("zsupA", d)
	if err != nil {
		t.Fatalf("createSlot: %v", err)
	}
	defer sl.destroy()

	offered := net.IPv4(192, 168, 88, 42).To4()
	go miniDHCPServer(t, srvIf, offered)

	// Acquire from inside the namespace, on a locked thread (no goroutines).
	var lease *dhcp.Lease
	err = sl.Do(func() error {
		link, err := netlink.LinkByName("eth0")
		if err != nil {
			return err
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return err
		}
		iface, err := net.InterfaceByName("eth0")
		if err != nil {
			return err
		}
		c := &dhcp.Client{
			Iface:  iface,
			Params: dhcp.Params{Hostname: d.Hostname, VendorClass: d.Expand(d.Profile.DHCP.VendorClass), PRL: d.Profile.DHCP.PRL, ClientID: true},
			Log:    func(s string, a ...any) { t.Logf("dev: "+s, a...) },
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		lease, err = c.Acquire(ctx, nil)
		return err
	})
	if err != nil {
		t.Fatalf("acquire in ns: %v", err)
	}
	if !lease.IP.Equal(offered) {
		t.Fatalf("leased %s, want %s", lease.IP, offered)
	}
	t.Logf("device %s (%s) got %s via macvlan in its namespace", d.Name, d.MAC, lease.IP)
}

// miniDHCPServer is a copy of the veth test server, kept local to this
// package. It answers one DISCOVER and one REQUEST.
func miniDHCPServer(t *testing.T, iface *net.Interface, yiaddr net.IP) {
	htons := func(v uint16) uint16 { return v<<8 | v>>8 }
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, int(htons(syscall.ETH_P_IP)))
	if err != nil {
		return
	}
	defer syscall.Close(fd)
	syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: iface.Index})
	tv := syscall.NsecToTimeval(int64(250 * time.Millisecond))
	syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	server := net.IPv4(192, 168, 88, 1).To4()

	const magic = 0x63825363
	send := func(to net.HardwareAddr, kind byte, xid uint32) {
		b := make([]byte, 236)
		b[0], b[1], b[2] = 2, 1, 6
		binary.BigEndian.PutUint32(b[4:], xid)
		copy(b[16:20], yiaddr)
		copy(b[20:24], server)
		copy(b[28:34], to)
		b = binary.BigEndian.AppendUint32(b, magic)
		opt := func(c byte, v []byte) { b = append(b, c, byte(len(v))); b = append(b, v...) }
		opt(53, []byte{kind})
		opt(54, server)
		opt(51, binary.BigEndian.AppendUint32(nil, 3600))
		opt(1, []byte{255, 255, 255, 0})
		opt(3, server)
		opt(6, server)
		b = append(b, 255)
		frame := dhcp.TestUDP(server, net.IPv4bcast, 67, 68, b)
		sa := &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: iface.Index, Halen: 6}
		copy(sa.Addr[:], to)
		syscall.Sendto(fd, frame, 0, sa)
	}

	buf := make([]byte, 2048)
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			continue
		}
		d := dhcp.TestDHCPToServer(buf[:n])
		if d == nil {
			continue
		}
		m, err := dhcp.TestParse(d)
		if err != nil {
			continue
		}
		switch m.Type() {
		case dhcp.Discover:
			send(m.CHAddr, dhcp.Offer, m.XID)
		case dhcp.Request:
			send(m.CHAddr, dhcp.Ack, m.XID)
			return
		}
	}
}
