// Package profile describes simulated device types (the catalog) and which
// instances of them run on a given Pi (the fleet).
package profile

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed catalog.json
var builtinCatalog []byte

// Duration is a time.Duration that reads from JSON as "90s", "5m", "1h".
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"5m\": %s", b)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }
func (d Duration) D() time.Duration             { return time.Duration(d) }

// Profile is one device type: how it identifies itself on the LAN and what
// it talks to.
type Profile struct {
	Type     string `json:"-"`        // catalog key
	Vendor   string `json:"vendor"`   // ground truth
	Model    string `json:"model"`    // ground truth
	Class    string `json:"class"`    // ground truth, the sensor type name
	OUI      string `json:"oui"`      // 6 hex digits, a real IEEE MA-L block of Vendor
	Hostname string `json:"hostname"` // template, see Expand
	TLS      string `json:"tls"`      // ClientHello flavor: embedded, mbedtls, modern, mqtt

	DHCP   DHCP       `json:"dhcp"`
	MDNS   *MDNS      `json:"mdns,omitempty"`
	SSDP   *SSDP      `json:"ssdp,omitempty"`
	WSD    *WSD       `json:"wsd,omitempty"`
	NTP    *NTP       `json:"ntp,omitempty"`
	Cloud  []Endpoint `json:"cloud,omitempty"`
	Upload *Upload    `json:"upload,omitempty"`
	Stream *Stream    `json:"stream,omitempty"`
	Listen []int      `json:"listen,omitempty"` // TCP ports the device serves on the LAN
}

type DHCP struct {
	VendorClass string `json:"vendor_class,omitempty"` // option 60
	PRL         []int  `json:"prl"`                    // option 55, in order
	MaxSize     int    `json:"max_size,omitempty"`     // option 57
	ClientID    bool   `json:"client_id,omitempty"`    // option 61 (type 1 + MAC)
}

type MDNS struct {
	Instance string     `json:"instance"` // template
	Services []Service  `json:"services,omitempty"`
	Queries  []string   `json:"queries,omitempty"` // service types this device browses for
	Every    Duration   `json:"every"`
}

type Service struct {
	Type string   `json:"type"` // "_hue._tcp"
	Port int      `json:"port"`
	TXT  []string `json:"txt,omitempty"` // templates
}

type SSDP struct {
	Server       string   `json:"server"`
	Types        []string `json:"types"` // NT values besides upnp:rootdevice
	Port         int      `json:"port"`  // description.xml port
	FriendlyName string   `json:"friendly_name"`
	Manufacturer string   `json:"manufacturer"`
	ModelName    string   `json:"model_name"`
	Search       []string `json:"search,omitempty"` // ST values this device M-SEARCHes for
	Every        Duration `json:"every"`
}

type WSD struct {
	Types  string   `json:"types"`  // "dn:NetworkVideoTransmitter"
	Scopes string   `json:"scopes"` // space separated
	Every  Duration `json:"every"`
}

type NTP struct {
	Server string   `json:"server"`
	Every  Duration `json:"every"`
}

// Endpoint is one cloud destination contacted on a schedule.
//
//	proto "dns":  name lookup only
//	proto "tls":  lookup, TCP, TLS handshake (SNI = Host); Persist holds the
//	              connection open until the server drops it
//	proto "http": plaintext GET Path with UA
//	proto "udp":  lookup and one small datagram (some devices' keepalives)
type Endpoint struct {
	Host    string   `json:"host"`
	Port    int      `json:"port,omitempty"`
	Proto   string   `json:"proto"`
	Every   Duration `json:"every"`
	Persist bool     `json:"persist,omitempty"`
	Path    string   `json:"path,omitempty"`
	UA      string   `json:"ua,omitempty"`
}

// Upload models event-driven uploads (camera clips, logs) sent to the sink.
type Upload struct {
	PerHour float64 `json:"per_hour"` // mean events per hour (Poisson)
	MinKB   int     `json:"min_kb"`
	MaxKB   int     `json:"max_kb"`
	Hours   string  `json:"hours,omitempty"` // "7-23" local time; empty = all day
}

// Stream models viewing sessions: a download from the sink at Mbps.
type Stream struct {
	PerDay  float64  `json:"per_day"`
	MinMin  int      `json:"min_minutes"`
	MaxMin  int      `json:"max_minutes"`
	Mbps    float64  `json:"mbps"`
	Hours   string   `json:"hours,omitempty"`
	UA      string   `json:"ua,omitempty"`
	Domains []string `json:"domains,omitempty"` // looked up and TLS-touched at session start
}

// Catalog maps type name to profile.
type Catalog map[string]*Profile

// LoadCatalog returns the built-in catalog, with entries from extra (a JSON
// file of the same shape, may be empty) added or replacing built-ins.
func LoadCatalog(extra string) (Catalog, error) {
	c := Catalog{}
	if err := c.merge(builtinCatalog, "built-in catalog"); err != nil {
		return nil, err
	}
	if extra != "" {
		b, err := os.ReadFile(extra)
		if err != nil {
			return nil, err
		}
		if err := c.merge(b, extra); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c Catalog) merge(b []byte, src string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	for k, rm := range raw {
		if strings.HasPrefix(k, "_") { // "_comment"
			continue
		}
		p := &Profile{}
		dec := json.NewDecoder(bytes.NewReader(rm))
		dec.DisallowUnknownFields()
		if err := dec.Decode(p); err != nil {
			return fmt.Errorf("%s: %s: %w", src, k, err)
		}
		p.Type = k
		if err := p.validate(); err != nil {
			return fmt.Errorf("%s: %s: %w", src, k, err)
		}
		c[k] = p
	}
	return nil
}

func (p *Profile) validate() error {
	if len(p.OUI) != 6 {
		return fmt.Errorf("oui must be 6 hex digits")
	}
	if _, err := strconv.ParseUint(p.OUI, 16, 32); err != nil {
		return fmt.Errorf("oui: %w", err)
	}
	if b, _ := strconv.ParseUint(p.OUI[:2], 16, 8); b&3 != 0 {
		return fmt.Errorf("oui %s is multicast or locally administered", p.OUI)
	}
	if p.Hostname == "" || len(p.DHCP.PRL) == 0 {
		return fmt.Errorf("hostname and dhcp.prl are required")
	}
	switch p.TLS {
	case "", "embedded", "mbedtls", "modern", "mqtt":
	default:
		return fmt.Errorf("unknown tls flavor %q", p.TLS)
	}
	for _, e := range p.Cloud {
		switch e.Proto {
		case "dns", "tls", "http", "udp":
		default:
			return fmt.Errorf("cloud %s: unknown proto %q", e.Host, e.Proto)
		}
		if e.Every.D() <= 0 {
			return fmt.Errorf("cloud %s: every is required", e.Host)
		}
	}
	return nil
}

func (c Catalog) Names() []string {
	var n []string
	for k := range c {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Fleet is the set of devices one Pi runs.
type Fleet struct {
	Pi       string     `json:"pi"`       // name, also seeds MAC addresses
	Parent   string     `json:"parent"`   // wired interface, "eth0"
	Sink     string     `json:"sink"`     // host:port of the sink
	Control  string     `json:"control"`  // control API listen address, ":7070"
	Speed    float64    `json:"speed"`    // multiplies upload/stream event rates; 1 = realistic
	Profiles string     `json:"profiles"` // optional extra catalog file
	Devices  []Instance `json:"devices"`
	// ScanPeers are the control addresses (host:port) of other Pis whose
	// simulated devices the scan scenario targets. Devices on one Pi share a
	// NIC, so traffic between them is switched inside the Pi and never
	// reaches the router; scanning another Pi's devices crosses it.
	ScanPeers []string `json:"scan_peers,omitempty"`
}

type Instance struct {
	Name string `json:"name"` // unique on this Pi, <= 32 chars
	Type string `json:"type"`
	MAC  string `json:"mac,omitempty"` // override; default derived from OUI + hash(pi/name)
}

func LoadFleet(path string) (*Fleet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &Fleet{Parent: "eth0", Control: ":7070", Speed: 1}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Pi == "" || f.Sink == "" {
		return nil, fmt.Errorf("%s: pi and sink are required", path)
	}
	if _, _, err := net.SplitHostPort(f.Sink); err != nil {
		return nil, fmt.Errorf("%s: sink must be host:port: %w", path, err)
	}
	seen := map[string]bool{}
	for _, d := range f.Devices {
		if d.Name == "" || len(d.Name) > 32 || strings.ContainsAny(d.Name, "/ ") {
			return nil, fmt.Errorf("%s: bad device name %q", path, d.Name)
		}
		if seen[d.Name] {
			return nil, fmt.Errorf("%s: duplicate device %q", path, d.Name)
		}
		seen[d.Name] = true
	}
	return f, nil
}

// Device is a resolved instance: profile plus its identity on this Pi.
type Device struct {
	Name     string
	Index    int
	Profile  *Profile
	MAC      net.HardwareAddr
	Hostname string
	Serial   string // 12 hex digits, stable per instance; used in templates and UUIDs
}

// Resolve matches the fleet's instances to catalog profiles.
func (f *Fleet) Resolve(c Catalog) ([]*Device, error) {
	var out []*Device
	for i, inst := range f.Devices {
		p := c[inst.Type]
		if p == nil {
			return nil, fmt.Errorf("device %s: unknown type %q (have: %s)", inst.Name, inst.Type, strings.Join(c.Names(), ", "))
		}
		sum := sha256.Sum256([]byte(f.Pi + "/" + inst.Name))
		var mac net.HardwareAddr
		if inst.MAC != "" {
			m, err := net.ParseMAC(inst.MAC)
			if err != nil {
				return nil, fmt.Errorf("device %s: %w", inst.Name, err)
			}
			mac = m
		} else {
			o, _ := strconv.ParseUint(p.OUI, 16, 32)
			mac = net.HardwareAddr{byte(o >> 16), byte(o >> 8), byte(o), sum[0], sum[1], sum[2]}
		}
		d := &Device{Name: inst.Name, Index: i, Profile: p, MAC: mac, Serial: fmt.Sprintf("%X", sum[3:9])}
		d.Hostname = d.Expand(p.Hostname)
		out = append(out, d)
	}
	return out, nil
}

// Expand fills a template:
//
//	{mac3}  last 3 MAC bytes, uppercase hex   {mac3l} lowercase
//	{mac6}  whole MAC, uppercase hex          {mac6l} lowercase
//	{mac}   aa:bb:cc:dd:ee:ff                 {serial} 12 hex digits
//	{name}  instance name                     {uuid}   stable UUID
//	{room}  stable room name ("Kitchen")
func (d *Device) Expand(s string) string {
	m := d.MAC
	r := strings.NewReplacer(
		"{mac3}", fmt.Sprintf("%02X%02X%02X", m[3], m[4], m[5]),
		"{mac3l}", fmt.Sprintf("%02x%02x%02x", m[3], m[4], m[5]),
		"{mac6}", strings.ToUpper(strings.ReplaceAll(m.String(), ":", "")),
		"{mac6l}", strings.ReplaceAll(m.String(), ":", ""),
		"{mac}", m.String(),
		"{serial}", d.Serial,
		"{name}", d.Name,
		"{uuid}", d.UUID(),
		"{room}", d.Room(),
	)
	return r.Replace(s)
}

var rooms = []string{"Kitchen", "Living Room", "Bedroom", "Office", "Den", "Basement", "Garage", "Nursery"}

// Room is a stable, realistic room name for user-facing labels (a speaker's
// "Kitchen speaker"), so instance names like "nest-mini-1" never leak onto
// the network.
func (d *Device) Room() string {
	s := sha256.Sum256([]byte("room/" + d.Serial))
	return rooms[int(s[0])%len(rooms)]
}

// UUID is a stable version-4-shaped UUID for UPnP/WSD identities.
func (d *Device) UUID() string {
	s := sha256.Sum256([]byte("uuid/" + d.Serial + d.MAC.String()))
	s[6] = s[6]&0x0f | 0x40
	s[8] = s[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", s[0:4], s[4:6], s[6:8], s[8:10], s[10:16])
}

// LinkName is the macvlan's name in the host namespace (<= 15 chars).
func (d *Device) LinkName() string { return fmt.Sprintf("zs%d", d.Index) }

// InHours reports whether hour h (0-23) is inside a window like "7-23" or
// "22-6" (wraps midnight). Empty means always.
// HoursSpan is how many hours of the day a window like "18-23" covers (24
// for an empty or unparsable window), so a per-day rate can be spread over
// the active hours only.
func HoursSpan(window string) int {
	n := 0
	for h := 0; h < 24; h++ {
		if InHours(window, h) {
			n++
		}
	}
	if n == 0 {
		return 24
	}
	return n
}

func InHours(window string, h int) bool {
	if window == "" {
		return true
	}
	a, b, ok := strings.Cut(window, "-")
	if !ok {
		return true
	}
	from, err1 := strconv.Atoi(a)
	to, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return true
	}
	if from <= to {
		return h >= from && h < to
	}
	return h >= from || h < to
}
