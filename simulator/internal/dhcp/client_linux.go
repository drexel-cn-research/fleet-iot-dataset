package dhcp

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"syscall"
	"time"
)

// Client runs DHCP on one interface over a raw packet socket, so it works
// before the interface has an address and sees replies whether the server
// broadcasts or unicasts them.
type Client struct {
	Iface  *net.Interface
	Params Params
	Log    func(format string, args ...any)
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

type rawConn struct{ fd int; ifindex int }

func (c *Client) open() (*rawConn, error) {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, int(htons(syscall.ETH_P_IP)))
	if err != nil {
		return nil, fmt.Errorf("packet socket: %w", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: c.Iface.Index}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind %s: %w", c.Iface.Name, err)
	}
	tv := syscall.NsecToTimeval(int64(250 * time.Millisecond))
	syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	return &rawConn{fd: fd, ifindex: c.Iface.Index}, nil
}

func (r *rawConn) close() { syscall.Close(r.fd) }

func (r *rawConn) send(src, dst net.IP, payload []byte) error {
	to := &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_IP), Ifindex: r.ifindex, Halen: 6}
	copy(to.Addr[:], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	return syscall.Sendto(r.fd, udpIP(src, dst, payload), 0, to)
}

// await reads until a reply for xid of one of the wanted types arrives or
// the deadline passes.
func (r *rawConn) await(ctx context.Context, xid uint32, mac net.HardwareAddr, until time.Time, want ...byte) (*Message, error) {
	buf := make([]byte, 2048)
	for time.Now().Before(until) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, _, err := syscall.Recvfrom(r.fd, buf, 0)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return nil, err
		}
		d := dhcpFromIP(buf[:n])
		if d == nil {
			continue
		}
		m, err := parse(d)
		if err != nil || m.Op != 2 || m.XID != xid || m.CHAddr.String() != mac.String() {
			continue
		}
		for _, w := range want {
			if m.Type() == w {
				return m, nil
			}
		}
	}
	return nil, errTimeout
}

var (
	errTimeout = errors.New("no reply")
	ErrNak     = errors.New("server sent NAK")
)

// Acquire gets a lease: DISCOVER, OFFER, REQUEST, ACK. prev, if set, is
// asked for again (option 50) so a restarted device keeps its address.
func (c *Client) Acquire(ctx context.Context, prev net.IP) (*Lease, error) {
	r, err := c.open()
	if err != nil {
		return nil, err
	}
	defer r.close()
	mac := c.Iface.HardwareAddr
	start := time.Now()
	wait := 2 * time.Second
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		xid := rand.Uint32()
		secs := uint16(time.Since(start).Seconds())
		if err := r.send(net.IPv4zero, net.IPv4bcast, build(Discover, xid, secs, mac, &c.Params, nil, prev, nil)); err != nil {
			return nil, fmt.Errorf("send discover: %w", err)
		}
		offer, err := r.await(ctx, xid, mac, time.Now().Add(wait), Offer)
		if err == errTimeout {
			c.logf("dhcp: no offer (attempt %d)", attempt+1)
			wait = min(wait*2, 16*time.Second)
			continue
		}
		if err != nil {
			return nil, err
		}
		server := ip4(offer.Options[optServerID])
		req := build(Request, xid, uint16(time.Since(start).Seconds()), mac, &c.Params, nil, offer.YIAddr, server)
		for try := 0; try < 3; try++ {
			if err := r.send(net.IPv4zero, net.IPv4bcast, req); err != nil {
				return nil, fmt.Errorf("send request: %w", err)
			}
			ack, err := r.await(ctx, xid, mac, time.Now().Add(3*time.Second), Ack, Nak)
			if err == errTimeout {
				continue
			}
			if err != nil {
				return nil, err
			}
			if ack.Type() == Nak {
				prev = nil
				break
			}
			return leaseFrom(ack), nil
		}
	}
}

// Renew extends l by broadcasting a REQUEST with ciaddr set (the rebinding
// form, which any server on the segment answers). On NAK the caller must
// drop the address and Acquire again.
func (c *Client) Renew(ctx context.Context, l *Lease) (*Lease, error) {
	r, err := c.open()
	if err != nil {
		return nil, err
	}
	defer r.close()
	mac := c.Iface.HardwareAddr
	for try := 0; try < 3; try++ {
		xid := rand.Uint32()
		if err := r.send(l.IP, net.IPv4bcast, build(Request, xid, 0, mac, &c.Params, l.IP, nil, nil)); err != nil {
			return nil, err
		}
		m, err := r.await(ctx, xid, mac, time.Now().Add(3*time.Second), Ack, Nak)
		if err == errTimeout {
			continue
		}
		if err != nil {
			return nil, err
		}
		if m.Type() == Nak {
			return nil, ErrNak
		}
		return leaseFrom(m), nil
	}
	return nil, errTimeout
}

// Release gives the address back so the router's lease table stays tidy
// when the simulator stops.
func (c *Client) Release(l *Lease) error {
	r, err := c.open()
	if err != nil {
		return err
	}
	defer r.close()
	return r.send(l.IP, l.Server, build(Release, rand.Uint32(), 0, c.Iface.HardwareAddr, &c.Params, l.IP, nil, l.Server))
}

func (c *Client) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}
