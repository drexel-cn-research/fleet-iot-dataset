package device

import (
	"bytes"
	"net"
	"testing"
	"time"

	"iotsim/internal/profile"
)

// captureHello returns the first TLS record a device of the given flavor
// sends, i.e. its ClientHello.
func captureHello(t *testing.T, flavor string) []byte {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 4096)
		n, _ := c.Read(buf)
		got <- buf[:n]
	}()
	d := &Device{Device: &profile.Device{Profile: &profile.Profile{TLS: flavor}}}
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	conn := d.tlsClient(raw, "example.com")
	conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	go conn.Handshake() // fails when the listener hangs up; we only want the hello
	return <-got
}

func TestFlavorsDiffer(t *testing.T) {
	tls13Cipher := []byte{0x13, 0x01} // TLS_AES_128_GCM_SHA256
	modern := captureHello(t, "modern")
	embedded := captureHello(t, "embedded")
	mbed := captureHello(t, "mbedtls")
	if len(modern) == 0 || len(embedded) == 0 || len(mbed) == 0 {
		t.Fatal("did not capture a ClientHello")
	}
	if !bytes.Contains(modern, tls13Cipher) {
		t.Error("modern flavor should offer TLS 1.3 suites")
	}
	if bytes.Contains(embedded, tls13Cipher) || bytes.Contains(mbed, tls13Cipher) {
		t.Error("embedded/mbedtls flavors must not offer TLS 1.3 suites")
	}
	if !bytes.Contains(modern, []byte("h2")) {
		t.Error("modern flavor should advertise h2 ALPN")
	}
	if bytes.Equal(embedded, mbed) {
		t.Error("embedded and mbedtls hellos are identical")
	}
	t.Logf("hello sizes: modern=%d embedded=%d mbedtls=%d", len(modern), len(embedded), len(mbed))
}
