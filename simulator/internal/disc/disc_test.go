package disc

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestMDNSResponseParses(t *testing.T) {
	m := &MDNS{
		Host:     "Philips-hue",
		Instance: "Philips Hue - 1A2B3C",
		Services: []Service{{Type: "_hue._tcp", Port: 443, TXT: []string{"bridgeid=001788fffe1a2b3c", "modelid=BSB002"}}},
	}
	pkt := m.response()
	if binary.BigEndian.Uint16(pkt[2:]) != 0x8400 {
		t.Errorf("flags not an authoritative response")
	}
	ancount := binary.BigEndian.Uint16(pkt[6:])
	if ancount == 0 {
		t.Fatal("no answer records")
	}
	// The instance name must appear (DNS-encoded labels contain the text).
	if !strings.Contains(string(pkt), "_hue") || !strings.Contains(string(pkt), "modelid=BSB002") {
		t.Errorf("service type or TXT missing from response")
	}
}

func TestQuestionsRoundTrip(t *testing.T) {
	q := mdnsQuery([]string{"_googlecast._tcp", "_hue._tcp"})
	names := questions(q)
	if len(names) != 2 {
		t.Fatalf("got %d questions: %v", len(names), names)
	}
	if names[0] != "_googlecast._tcp.local" {
		t.Errorf("name[0] = %q", names[0])
	}
}

func TestNameCompressionPointer(t *testing.T) {
	// A pointer at offset 12 jumping to a name written at offset 20.
	b := make([]byte, 40)
	copy(b[20:], encName("_hue._tcp.local"))
	b[12] = 0xc0
	b[13] = 20
	name, next, ok := readName(b, 12)
	if !ok || name != "_hue._tcp.local" {
		t.Fatalf("name=%q ok=%v", name, ok)
	}
	if next != 14 {
		t.Errorf("next = %d, want 14 (past the 2-byte pointer)", next)
	}
}
