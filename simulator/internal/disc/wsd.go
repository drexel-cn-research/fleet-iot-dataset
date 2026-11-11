package disc

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"time"
)

var wsdGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 3702}

// WSD sends WS-Discovery Hello messages and answers Probes, the way ONVIF
// cameras and WSD printers announce themselves.
type WSD struct {
	Iface  *net.Interface
	IP     func() net.IP
	UUID   string
	Types  string
	Scopes string
	XAddr  string // path, e.g. "/onvif/device_service"
	Every  time.Duration
	Log    func(string, ...any)
}

const wsdNS = `xmlns:soap="http://www.w3.org/2003/05/soap-envelope" ` +
	`xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" ` +
	`xmlns:wsd="http://schemas.xmlsoap.org/ws/2005/04/discovery" ` +
	`xmlns:dn="http://www.onvif.org/ver10/network/wsdl" ` +
	`xmlns:wprt="http://schemas.microsoft.com/windows/2006/08/wdp/print" ` +
	`xmlns:wscn="http://schemas.microsoft.com/windows/2006/08/wdp/scan"`

func msgID() string {
	return fmt.Sprintf("urn:uuid:%08x-%04x-4%03x-a%03x-%012x", rand.Uint32(), rand.Uint32()&0xffff,
		rand.Uint32()&0xfff, rand.Uint32()&0xfff, rand.Uint64()&0xffffffffffff)
}

func (w *WSD) body(kind string, n int, relatesTo string) []byte {
	action := "Hello"
	to := "urn:schemas-xmlsoap-org:ws:2005:04:discovery"
	if kind == "ProbeMatches" {
		action = "ProbeMatches"
		to = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"
	}
	rel := ""
	if relatesTo != "" {
		rel = "<wsa:RelatesTo>" + relatesTo + "</wsa:RelatesTo>"
	}
	inner := fmt.Sprintf(`<wsa:EndpointReference><wsa:Address>urn:uuid:%s</wsa:Address></wsa:EndpointReference>`+
		`<wsd:Types>%s</wsd:Types><wsd:Scopes>%s</wsd:Scopes><wsd:XAddrs>http://%s%s</wsd:XAddrs>`+
		`<wsd:MetadataVersion>1</wsd:MetadataVersion>`, w.UUID, w.Types, w.Scopes, w.IP(), w.XAddr)
	if kind == "ProbeMatches" {
		inner = "<wsd:ProbeMatch>" + inner + "</wsd:ProbeMatch>"
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+
		`<soap:Envelope %s><soap:Header><wsa:MessageID>%s</wsa:MessageID>%s<wsa:To>%s</wsa:To>`+
		`<wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/%s</wsa:Action>`+
		`<wsd:AppSequence InstanceId="%d" MessageNumber="%d"/></soap:Header>`+
		`<soap:Body><wsd:%s>%s</wsd:%s></soap:Body></soap:Envelope>`,
		wsdNS, msgID(), rel, to, action, time.Now().Unix()/3600, n, kind, inner, kind))
}

func (w *WSD) Run(ctx context.Context) error {
	conn, err := net.ListenMulticastUDP("udp4", w.Iface, wsdGroup)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	n := 1
	go func() {
		buf := make([]byte, 8192)
		for {
			k, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p := buf[:k]
			if !bytes.Contains(p, []byte("/discovery/Probe<")) && !bytes.Contains(p, []byte(":Probe>")) {
				continue
			}
			id := between(p, "MessageID>", "<")
			n++
			conn.WriteToUDP(w.body("ProbeMatches", n, id), from)
		}
	}()
	conn.WriteToUDP(w.body("Hello", n, ""), wsdGroup)
	for {
		if !sleep(ctx, Jitter(w.Every)) {
			return nil
		}
		n++
		conn.WriteToUDP(w.body("Hello", n, ""), wsdGroup)
	}
}

func between(b []byte, a, z string) string {
	i := bytes.Index(b, []byte(a))
	if i < 0 {
		return ""
	}
	rest := b[i+len(a):]
	j := bytes.Index(rest, []byte(z))
	if j < 0 {
		return ""
	}
	return string(rest[:j])
}
