// Command iotsim is the IoT simulation engine. It has these modes:
//
//	iotsim run     <fleet.json>   build namespaces + interfaces and run the fleet (root)
//	iotsim worker  ...            internal: run one device inside its namespace
//	iotsim profiles               list the device catalog (optionally validate OUIs against the API)
//	iotsim ctl     ...            talk to a running supervisor's control API
//	iotsim sink    ...            run the traffic sink (on any host)
//
// Build for a Pi:  GOOS=linux GOARCH=arm64 go build ./cmd/iotsim
// Build for macOS:   GOOS=darwin GOARCH=arm64 go build ./cmd/iotsim   (sink + ctl only)
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(ctx, os.Args[2:])
	case "worker":
		err = cmdWorker(ctx, os.Args[2:])
	case "profiles":
		err = cmdProfiles(ctx, os.Args[2:])
	case "ctl":
		err = cmdCtl(ctx, os.Args[2:])
	case "sink":
		err = cmdSink(ctx, os.Args[2:])
	case "drift":
		err = cmdDrift(ctx, os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `iotsim - IoT device simulation engine

  iotsim run <fleet.json> [--control :7070] [--profiles extra.json]
      Build a namespace + macvlan per device and run the fleet. Needs root
      and iproute2. Runs on a Raspberry Pi wired to the router.

  iotsim profiles [--profiles extra.json] [--api URL]
      List the device catalog. With --api, check each OUI against the
      the enrichment API /v1/oui endpoint and print the vendor it resolves to.

  iotsim ctl [--control host:7070] <status|inject> ...
      status [name]                 show the fleet or one device
      inject <scenario> [name]      inject scan|beacon|exfil|newdest
                                    (omit name to hit every device)

  iotsim drift --summaries DIR [--control host:7070,host:7070]
      Flag windows where a device departs from its own baseline, and
      score the flags against the injection log on the Pis.

  iotsim sink [--listen :9000] [--log sink.log]
      Run the traffic sink on any host: accept uploads, serve streams,
      log per-device byte counts.
`)
	os.Exit(2)
}
