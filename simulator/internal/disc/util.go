package disc

import (
	"context"
	"math/rand/v2"
	"time"
)

// Jitter returns d +/- 10%, so a fleet of identical devices doesn't fire in
// lockstep.
func Jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Minute
	}
	return d + time.Duration((rand.Float64()*0.2-0.1)*float64(d))
}

// sleep waits for d or until ctx ends; false means ctx ended.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Sleep is sleep for other packages.
func Sleep(ctx context.Context, d time.Duration) bool { return sleep(ctx, d) }
