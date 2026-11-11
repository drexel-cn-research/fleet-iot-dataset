package device

import (
	"net"

	"github.com/vishvananda/netlink"
	"iotsim/internal/dhcp"
)

// linkUp brings the device's interface up so DHCP can send. The supervisor
// leaves it down after moving it into the namespace.
func linkUp(iface *net.Interface) error {
	link, err := netlink.LinkByIndex(iface.Index)
	if err != nil {
		return err
	}
	return netlink.LinkSetUp(link)
}

// assignIP puts the leased address on the interface and installs a default
// route via the lease's router, replacing any previous address this device
// held. Called inside the device's own namespace, so it only affects this
// device.
func assignIP(iface *net.Interface, l *dhcp.Lease) error {
	link, err := netlink.LinkByIndex(iface.Index)
	if err != nil {
		return err
	}
	ones, _ := l.Mask.Size()
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: l.IP, Mask: l.Mask}}

	// Remove stale v4 addresses, add the new one.
	if existing, err := netlink.AddrList(link, netlink.FAMILY_V4); err == nil {
		for _, a := range existing {
			if !a.IP.Equal(l.IP) {
				netlink.AddrDel(link, &a)
			}
		}
	}
	if err := netlink.AddrReplace(link, addr); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}
	if l.Router != nil {
		_ = ones
		netlink.RouteReplace(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Gw:        l.Router,
			Dst:       nil, // default route
		})
	}
	return nil
}
