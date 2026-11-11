//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"iotsim/internal/device"
	"iotsim/internal/profile"
	"iotsim/internal/supervisor"
)

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	control := fs.String("control", "", "control API listen address (default from fleet, else :7070)")
	extra := fs.String("profiles", "", "extra catalog JSON to merge over the built-ins")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: iotsim run <fleet.json>")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("run needs root (creates network namespaces and interfaces)")
	}

	f, err := profile.LoadFleet(fs.Arg(0))
	if err != nil {
		return err
	}
	extraPath := pickProfiles(*extra, f)
	cat, err := profile.LoadCatalog(extraPath)
	if err != nil {
		return err
	}
	logf := func(f string, a ...any) { log.Printf(f, a...) }
	sup, err := supervisor.New(fs.Arg(0), cat, logf)
	if err != nil {
		return err
	}
	sup.Profiles = extraPath

	addr := *control
	if addr == "" {
		addr = sup.Fleet().Control
	}
	if addr == "" {
		addr = ":7070"
	}

	log.Printf("starting fleet %q on %s -> sink %s", sup.Fleet().Pi, sup.Fleet().Parent, sup.Fleet().Sink)
	if err := sup.Start(ctx); err != nil {
		sup.Stop()
		return err
	}

	srv := &http.Server{Addr: addr, Handler: sup.Handler()}
	go func() {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("control API: %v", err)
			return
		}
		log.Printf("control API on %s", addr)
		srv.Serve(ln)
	}()

	<-ctx.Done()
	log.Printf("stopping...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
	sup.Stop()
	log.Printf("stopped")
	return nil
}

// cmdWorker runs one device. It is launched by the supervisor via
// "ip netns exec", so it already runs inside the device's namespace with
// eth0 present. It must not be run by hand.
func cmdWorker(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	fleet := fs.String("fleet", "", "fleet file")
	name := fs.String("device", "", "device name")
	sock := fs.String("sock", "", "control unix socket path")
	extra := fs.String("profiles", "", "extra catalog")
	fs.Parse(args)
	if *fleet == "" || *name == "" || *sock == "" {
		return fmt.Errorf("worker: --fleet, --device and --sock are required")
	}

	f, err := profile.LoadFleet(*fleet)
	if err != nil {
		return err
	}
	cat, err := profile.LoadCatalog(pickProfiles(*extra, f))
	if err != nil {
		return err
	}
	devs, err := f.Resolve(cat)
	if err != nil {
		return err
	}
	var pd *profile.Device
	for _, d := range devs {
		if d.Name == *name {
			pd = d
			break
		}
	}
	if pd == nil {
		return fmt.Errorf("device %q not in fleet", *name)
	}

	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		return fmt.Errorf("eth0 in namespace: %w", err)
	}
	logf := func(format string, a ...any) { log.Printf("[%s] "+format, append([]any{*name}, a...)...) }
	d := device.New(pd, iface, f.Sink, f.Speed, logf)

	go d.ServeControl(ctx, *sock)
	return d.Run(ctx)
}

// pickProfiles chooses the extra catalog: the --profiles flag wins, else the
// fleet file's "profiles" field. Relative paths are relative to the working
// directory, so prefer absolute paths in fleet files.
func pickProfiles(flagPath string, f *profile.Fleet) string {
	if flagPath != "" {
		return flagPath
	}
	return f.Profiles
}
