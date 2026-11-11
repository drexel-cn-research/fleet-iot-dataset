package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// The sink is the traffic target on the sink host. It speaks the tiny protocol in
// internal/device/traffic.go: a 32-byte header then a transfer. Uploads are
// read and discarded; streams are served random bytes. It keeps per-device
// byte counts and prints a table on each interval and on exit.
//
// This is the only part that builds and runs on macOS; everything else is
// Linux-only (it needs namespaces).

const sinkMagic = "ZSNK"

type counts struct {
	up, down     uint64
	ups, streams uint64
	last         time.Time
}

type sink struct {
	mu  sync.Mutex
	dev map[string]*counts
	log *log.Logger
}

func cmdSink(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sink", flag.ExitOnError)
	listen := fs.String("listen", ":9000", "listen address")
	logPath := fs.String("log", "", "append a byte-count log to this file (also stdout)")
	every := fs.Duration("report", 60*time.Second, "how often to print the totals table")
	fs.Parse(args)

	out := io.Writer(os.Stdout)
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		out = io.MultiWriter(os.Stdout, f)
	}
	s := &sink{dev: map[string]*counts{}, log: log.New(out, "", log.LstdFlags)}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	s.log.Printf("sink listening on %s", *listen)

	go func() {
		t := time.NewTicker(*every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.report()
			}
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.report()
				return nil
			default:
				continue
			}
		}
		go s.serve(c)
	}
}

func (s *sink) serve(c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	h := make([]byte, 32)
	if _, err := io.ReadFull(c, h); err != nil || string(h[:4]) != sinkMagic {
		return
	}
	kind := h[4]
	namelen := binary.BigEndian.Uint16(h[8:])
	n := binary.BigEndian.Uint64(h[16:])
	if namelen == 0 || namelen > 64 {
		return
	}
	nameb := make([]byte, namelen)
	if _, err := io.ReadFull(c, nameb); err != nil {
		return
	}
	name := string(nameb)

	switch kind {
	case 1: // upload: read and discard n bytes
		c.SetReadDeadline(time.Now().Add(10 * time.Minute))
		got, _ := io.Copy(io.Discard, io.LimitReader(c, int64(n)))
		s.add(name, uint64(got), 0, 1, 0)
	case 2: // stream: serve n bytes
		c.SetWriteDeadline(time.Now().Add(2 * time.Hour))
		sent := serveRandom(c, n)
		s.add(name, 0, sent, 0, 1)
	}
}

func serveRandom(w io.Writer, n uint64) uint64 {
	buf := make([]byte, 64*1024)
	rand.Read(buf)
	var sent uint64
	for sent < n {
		chunk := uint64(len(buf))
		if rem := n - sent; rem < chunk {
			chunk = rem
		}
		m, err := w.Write(buf[:chunk])
		sent += uint64(m)
		if err != nil {
			break
		}
	}
	return sent
}

func (s *sink) add(name string, up, down, ups, streams uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.dev[name]
	if c == nil {
		c = &counts{}
		s.dev[name] = c
	}
	c.up += up
	c.down += down
	c.ups += ups
	c.streams += streams
	c.last = time.Now()
}

func (s *sink) report() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.dev) == 0 {
		return
	}
	s.log.Printf("---- sink totals (%d devices) ----", len(s.dev))
	for name, c := range s.dev {
		s.log.Printf("  %-18s up=%-10s down=%-10s uploads=%-4d streams=%-3d last=%s ago",
			name, hbytes(c.up), hbytes(c.down), c.ups, c.streams, time.Since(c.last).Round(time.Second))
	}
}

var _ = fmt.Sprint
