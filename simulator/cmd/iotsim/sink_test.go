//go:build linux

package main

import (
	"io"
	"log"
	"net"
	"testing"
	"time"

	"iotsim/internal/device"
)

// TestSinkProtocol drives the real sink with the device's own header
// encoder, so a format drift between the two sides fails here.
func TestSinkProtocol(t *testing.T) {
	s := &sink{dev: map[string]*counts{}, log: log.New(io.Discard, "", 0)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()

	const up, down = 5 << 20, 3 << 20

	// Upload: header then payload.
	c, _ := net.Dial("tcp", ln.Addr().String())
	c.Write(device.SinkHeader(device.SinkUpload, "hik-cam-1", up))
	c.Write(make([]byte, up))
	c.Close()

	// Stream: header, then read what the sink sends back.
	c, _ = net.Dial("tcp", ln.Addr().String())
	c.Write(device.SinkHeader(device.SinkStream, "roku", down))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, _ := io.Copy(io.Discard, c)
	c.Close()
	if got != down {
		t.Errorf("stream delivered %d bytes, want %d", got, down)
	}

	// Let the sink record the upload.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		h := s.dev["hik-cam-1"]
		done := h != nil && h.up == up && s.dev["roku"] != nil
		s.mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.dev["hik-cam-1"]; h == nil || h.up != up || h.ups != 1 {
		t.Errorf("upload not counted: %+v", h)
	}
	if r := s.dev["roku"]; r == nil || r.down != down || r.streams != 1 {
		t.Errorf("stream not counted: %+v", r)
	}
}
