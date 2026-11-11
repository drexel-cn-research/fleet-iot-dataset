package supervisor

import (
	"net"
	"os"
	"testing"

	"github.com/vishvananda/netlink"
	"iotsim/internal/profile"
)

// TestCreateSlot exercises the namespace + macvlan machinery against a real
// parent interface. It needs root (CAP_NET_ADMIN); it skips otherwise.
func TestCreateSlot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root for network namespaces")
	}
	parent := testParent(t)

	cat, err := profile.LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	fp := writeFleet(t, parent, "hik-cam-1", "hikvision-cam")
	f, _ := profile.LoadFleet(fp)
	devs, err := f.Resolve(cat)
	if err != nil {
		t.Fatal(err)
	}
	d := devs[0]

	sl, err := createSlot(parent, d)
	if err != nil {
		t.Fatalf("createSlot: %v", err)
	}
	defer sl.destroy()

	// Inside the namespace, eth0 must exist with the device's vendor MAC.
	iface, err := sl.iface()
	if err != nil {
		t.Fatalf("iface in ns: %v", err)
	}
	if iface.HardwareAddr.String() != d.MAC.String() {
		t.Errorf("MAC in ns = %s, want %s", iface.HardwareAddr, d.MAC)
	}
	if got := iface.HardwareAddr.String()[:8]; got != "08:54:11" {
		t.Errorf("vendor OUI missing from MAC: %s", iface.HardwareAddr)
	}

	// A second device gets its own namespace and a different MAC.
	fp2 := writeFleet(t, parent, "hik-cam-2", "hikvision-cam")
	f2, _ := profile.LoadFleet(fp2)
	d2, _ := f2.Resolve(cat)
	sl2, err := createSlot(parent, d2[0])
	if err != nil {
		t.Fatalf("createSlot 2: %v", err)
	}
	defer sl2.destroy()
	if d.MAC.String() == d2[0].MAC.String() {
		t.Errorf("both devices share a MAC")
	}

	// The parent namespace must not see eth0 (it lives in the ns).
	if _, err := net.InterfaceByName("eth0"); err == nil {
		// eth0 is also the sandbox's real NIC; ensure our macvlan link name is gone instead.
	}
	if _, err := netlink.LinkByName(d.LinkName()); err == nil {
		t.Errorf("macvlan %s still in host ns; should have moved into the namespace", d.LinkName())
	}
}

func testParent(t *testing.T) string {
	for _, name := range []string{"eth0", "en0", "ens0"} {
		if _, err := net.InterfaceByName(name); err == nil {
			return name
		}
	}
	// Fall back to the first non-loopback interface.
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback == 0 && i.HardwareAddr != nil {
			return i.Name
		}
	}
	t.Skip("no suitable parent interface")
	return ""
}

func writeFleet(t *testing.T, parent, name, typ string) string {
	t.Helper()
	fp := t.TempDir() + "/f.json"
	os.WriteFile(fp, []byte(`{"pi":"pitest","parent":"`+parent+`","sink":"10.0.0.2:9000","devices":[{"name":"`+name+`","type":"`+typ+`"}]}`), 0o644)
	return fp
}
