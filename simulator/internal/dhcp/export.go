package dhcp

import (
	"encoding/binary"
	"net"
)

// These thin wrappers expose packet internals to integration tests in other
// packages (e.g. supervisor), which need a minimal DHCP server. They are not
// used by the client itself.

// TestParse parses a DHCP payload.
func TestParse(b []byte) (*Message, error) { return parse(b) }

// TestUDP wraps payload in IPv4+UDP with the given ports.
func TestUDP(src, dst net.IP, sport, dport uint16, payload []byte) []byte {
	return udpIPPorts(src, dst, sport, dport, payload)
}

// TestDHCPToServer extracts a DHCP payload addressed to the server port (67).
func TestDHCPToServer(p []byte) []byte {
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
