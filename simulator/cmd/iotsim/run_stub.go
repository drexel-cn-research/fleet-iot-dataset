//go:build !linux

package main

import (
	"context"
	"fmt"
)

// The simulation engine uses Linux network namespaces, so run/worker only
// work on the Pis. On macOS this binary is just the sink and ctl client.

func cmdRun(ctx context.Context, args []string) error {
	return fmt.Errorf("`run` works only on Linux (it uses network namespaces); on this machine use `iotsim sink` and `iotsim ctl`")
}

func cmdWorker(ctx context.Context, args []string) error {
	return fmt.Errorf("`worker` works only on Linux")
}
