package disc

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"time"
)

var ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}

// SSDP announces a UPnP root device (NOTIFY), answers M-SEARCH, and can
// search for other devices. With no Types it only searches.
type SSDP struct {
	Iface        *net.Interface
	IP           func() net.IP
	UUID         string
	Server       string
	Types        []string
	Port         int // where description.xml is served
	FriendlyName string
	Manufacturer string
	ModelName    string
	Serial       string
	Search       []string
	Every        time.Duration
	Log          func(string, ...any)
}

func (s *SSDP) location() string {
	return fmt.Sprintf("http://%s:%d/description.xml", s.IP(), s.Port)
}

func (s *SSDP) announces() bool { return len(s.Types) > 0 && s.Port > 0 }

// nts lists (NT, USN) pairs for this device.
func (s *SSDP) nts() [][2]string {
	u := "uuid:" + s.UUID
	out := [][2]string{{"upnp:rootdevice", u + "::upnp:rootdevice"}, {u, u}}
	for _, t := range s.Types {
		out = append(out, [2]string{t, u + "::" + t})
	}
	return out
}

func (s *SSDP) Run(ctx context.Context) error {
	conn, err := net.ListenMulticastUDP("udp4", s.Iface, ssdpGroup)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	notify := func() {
		if !s.announces() {
			return
		}
		for _, nt := range s.nts() {
			msg := "NOTIFY * HTTP/1.1\r\n" +
				"HOST: 239.255.255.250:1900\r\n" +
				"CACHE-CONTROL: max-age=1800\r\n" +
				"LOCATION: " + s.location() + "\r\n" +
				"NT: " + nt[0] + "\r\n" +
				"NTS: ssdp:alive\r\n" +
				"SERVER: " + s.Server + "\r\n" +
				"USN: " + nt[1] + "\r\n\r\n"
			conn.WriteToUDP([]byte(msg), ssdpGroup)
		}
	}
	search := func() {
		for _, st := range s.Search {
			msg := "M-SEARCH * HTTP/1.1\r\n" +
				"HOST: 239.255.255.250:1900\r\n" +
				"MAN: \"ssdp:discover\"\r\n" +
				"MX: 3\r\n" +
				"ST: " + st + "\r\n\r\n"
			conn.WriteToUDP([]byte(msg), ssdpGroup)
		}
	}

	if s.announces() {
		go func() {
			buf := make([]byte, 4096)
			for {
				n, from, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				st, ok := msearchST(buf[:n])
				if !ok {
					continue
				}
				for _, nt := range s.nts() {
					if st == "ssdp:all" || strings.EqualFold(st, nt[0]) {
						resp := "HTTP/1.1 200 OK\r\n" +
							"CACHE-CONTROL: max-age=1800\r\n" +
							"EXT:\r\n" +
							"LOCATION: " + s.location() + "\r\n" +
							"SERVER: " + s.Server + "\r\n" +
							"ST: " + nt[0] + "\r\n" +
							"USN: " + nt[1] + "\r\n\r\n"
						conn.WriteToUDP([]byte(resp), from)
					}
				}
			}
		}()
	}

	notify()
	search()
	for {
		if !sleep(ctx, Jitter(s.Every)) {
			return nil
		}
		notify()
		search()
	}
}

func msearchST(b []byte) (string, bool) {
	r := bufio.NewReader(bytes.NewReader(b))
	line, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "M-SEARCH") {
		return "", false
	}
	for {
		l, err := r.ReadString('\n')
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "ST") {
			return strings.TrimSpace(v), true
		}
		if err != nil {
			return "", false
		}
	}
}

// Description serves the UPnP device description at /description.xml.
func (s *SSDP) Description(w http.ResponseWriter, r *http.Request) {
	devType := "urn:schemas-upnp-org:device:Basic:1"
	for _, t := range s.Types {
		if strings.Contains(t, ":device:") {
			devType = t
			break
		}
	}
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("Server", s.Server)
	fmt.Fprintf(w, `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
<specVersion><major>1</major><minor>0</minor></specVersion>
<device>
<deviceType>%s</deviceType>
<friendlyName>%s</friendlyName>
<manufacturer>%s</manufacturer>
<modelName>%s</modelName>
<serialNumber>%s</serialNumber>
<UDN>uuid:%s</UDN>
</device>
</root>
`, html.EscapeString(devType), html.EscapeString(s.FriendlyName), html.EscapeString(s.Manufacturer),
		html.EscapeString(s.ModelName), html.EscapeString(s.Serial), s.UUID)
}
