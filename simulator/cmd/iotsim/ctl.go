package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func cmdCtl(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ctl", flag.ExitOnError)
	control := fs.String("control", "http://localhost:7070", "supervisor control API base URL")
	fs.Parse(args)
	base := *control
	if !strings.HasPrefix(base, "http") {
		base = "http://" + base
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: iotsim ctl status [name] | inject <scenario> [name]")
	}
	switch rest[0] {
	case "status":
		return ctlStatus(ctx, base, rest[1:])
	case "inject":
		return ctlInject(ctx, base, rest[1:])
	default:
		return fmt.Errorf("unknown ctl command %q", rest[0])
	}
}

func ctlStatus(ctx context.Context, base string, args []string) error {
	path := "/"
	if len(args) == 1 {
		path = "/devices/" + args[0]
	}
	body, err := get(ctx, base+path)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		fmt.Println(string(body))
		return nil
	}
	var ov struct {
		Pi      string `json:"pi"`
		Sink    string `json:"sink"`
		Devices []struct {
			Name, Vendor, Model, Class, IP, State string
			// Tags required: encoding/json matches names case-insensitively
			// but not across underscores, so these all read as zero before.
			Stats struct {
				TxBytes   uint64 `json:"tx_bytes"`
				RxBytes   uint64 `json:"rx_bytes"`
				Uploads   uint64 `json:"uploads"`
				Streams   uint64 `json:"streams"`
				CloudHits uint64 `json:"cloud_hits"`
			} `json:"stats"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(body, &ov); err != nil {
		fmt.Println(string(body))
		return nil
	}
	fmt.Printf("fleet %s  sink %s\n", ov.Pi, ov.Sink)
	fmt.Printf("%-16s %-16s %-17s %-8s %10s %10s %6s %6s\n", "DEVICE", "IP", "CLASS", "STATE", "TX", "RX", "UPL", "CLOUD")
	for _, d := range ov.Devices {
		fmt.Printf("%-16s %-16s %-17s %-8s %10s %10s %6d %6d\n",
			d.Name, d.IP, d.Class, d.State, hbytes(d.Stats.TxBytes), hbytes(d.Stats.RxBytes),
			d.Stats.Uploads, d.Stats.CloudHits)
	}
	return nil
}

func ctlInject(ctx context.Context, base string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: iotsim ctl inject <scenario> [name]")
	}
	req := map[string]string{"scenario": args[0]}
	body, _ := json.Marshal(req)
	path := "/inject"
	if len(args) >= 2 {
		path = "/devices/" + args[1] + "/inject"
	}
	out, err := post(ctx, base+path, body)
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func get(ctx context.Context, url string) ([]byte, error)  { return do(ctx, "GET", url, nil) }
func post(ctx context.Context, url string, b []byte) ([]byte, error) { return do(ctx, "POST", url, b) }

func do(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func hbytes(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := uint64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}
