package supervisor

import (
	"fmt"
	"net"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"iotsim/internal/profile"
)

// Each simulated device lives on its own macvlan interface in its own network
// namespace. macvlan gives the interface a distinct MAC that the parent NIC
// sends onto the wire, so the router sees N independent hosts on the LAN port.
// The namespace isolates each device's addresses, routes and sockets, so N
// devices can each DHCP, bind :80, and run mDNS without colliding.

// slot is one device's namespace and interface handle.
type slot struct {
	name   string
	nsName string
	handle netns.NsHandle
	link   netlink.Link
}

// createSlot builds the macvlan on parent, moves it into a fresh namespace,
// and sets the device's MAC. The returned slot must be entered with Do() to
// run code inside the namespace, and torn down with destroy().
//
// The caller must hold the OS thread locked across the parent-namespace
// operations; createSlot locks and unlocks internally and restores the
// original namespace before returning.
func createSlot(parent string, d *profile.Device) (*slot, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	orig, err := netns.Get()
	if err != nil {
		return nil, fmt.Errorf("get current ns: %w", err)
	}
	defer orig.Close()
	defer netns.Set(orig)

	pl, err := netlink.LinkByName(parent)
	if err != nil {
		return nil, fmt.Errorf("parent %s: %w", parent, err)
	}

	linkName := d.LinkName()
	// Clean up any leftover from a previous run.
	if old, err := netlink.LinkByName(linkName); err == nil {
		netlink.LinkDel(old)
	}

	mv := &netlink.Macvlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        linkName,
			ParentIndex: pl.Attrs().Index,
			HardwareAddr: net.HardwareAddr(d.MAC),
		},
		Mode: netlink.MACVLAN_MODE_BRIDGE,
	}
	if err := netlink.LinkAdd(mv); err != nil {
		return nil, fmt.Errorf("add macvlan %s: %w", linkName, err)
	}

	nsName := "zs-" + d.Name
	// Remove a stale namespace of the same name.
	netns.DeleteNamed(nsName)
	h, err := netns.NewNamed(nsName) // NewNamed also switches us into it
	if err != nil {
		netlink.LinkDel(mv)
		return nil, fmt.Errorf("new ns %s: %w", nsName, err)
	}
	// We're now in the new ns; go back to parent to move the link.
	netns.Set(orig)

	link, err := netlink.LinkByName(linkName)
	if err != nil {
		h.Close()
		netns.DeleteNamed(nsName)
		netlink.LinkDel(mv)
		return nil, err
	}
	if err := netlink.LinkSetNsFd(link, int(h)); err != nil {
		h.Close()
		netns.DeleteNamed(nsName)
		netlink.LinkDel(link)
		return nil, fmt.Errorf("move %s into ns: %w", linkName, err)
	}

	s := &slot{name: d.Name, nsName: nsName, handle: h}

	// Inside the ns: rename to eth0, set the MAC (preserved across the move
	// but set again to be safe), bring up loopback.
	err = s.Do(func() error {
		l, err := netlink.LinkByName(linkName)
		if err != nil {
			return err
		}
		netlink.LinkSetName(l, "eth0")
		netlink.LinkSetHardwareAddr(l, net.HardwareAddr(d.MAC))
		if lo, err := netlink.LinkByName("lo"); err == nil {
			netlink.LinkSetUp(lo)
		}
		s.link = l
		return nil
	})
	if err != nil {
		s.destroy()
		return nil, err
	}
	return s, nil
}

// Do runs f with the calling goroutine switched into the slot's namespace.
// It locks the OS thread for the duration and restores the previous
// namespace afterward.
func (s *slot) Do(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig, err := netns.Get()
	if err != nil {
		return err
	}
	defer orig.Close()
	if err := netns.Set(s.handle); err != nil {
		return err
	}
	defer netns.Set(orig)
	return f()
}

func (s *slot) destroy() {
	s.Do(func() error {
		if l, err := netlink.LinkByName("eth0"); err == nil {
			netlink.LinkDel(l)
		}
		return nil
	})
	if s.handle.IsOpen() {
		s.handle.Close()
	}
	netns.DeleteNamed(s.nsName)
}

// ifaceInSlot returns the net.Interface for eth0 as seen inside the slot.
func (s *slot) iface() (*net.Interface, error) {
	var out *net.Interface
	err := s.Do(func() error {
		i, err := net.InterfaceByName("eth0")
		out = i
		return err
	})
	return out, err
}
