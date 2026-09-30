package incus

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// An Incus that has died while systemd still holds its socket (incus.service
// stuck starting, say) accepts every connection and answers none: each call
// waits until its context ends, and a call with no deadline waits forever. So
// the daemon watches Incus (daemon/incuswatch.go), and while it finds it not
// answering it says so in the Client's Health, and every call fails at once with
// ErrNotAnswering, and why, instead of joining the wait.

// ErrNotAnswering is what every Incus call returns while Incus is marked as
// not answering.
var ErrNotAnswering = errors.New("Incus isn't answering")

// Health is whether Incus answers, as the daemon's watch last found it.
type Health struct{ why atomic.Pointer[string] }

// SetNotAnswering marks Incus as not answering, for why; an empty why clears
// the mark.
func (h *Health) SetNotAnswering(why string) {
	if why == "" {
		h.why.Store(nil)
		return
	}
	h.why.Store(&why)
}

// notAnsweringErr is the error calls fail with now, if any.
func (c Client) notAnsweringErr() error {
	if c.Health == nil {
		return nil
	}
	if why := c.Health.why.Load(); why != nil {
		return fmt.Errorf("%w: %s", ErrNotAnswering, *why)
	}
	return nil
}

// Probe is Ping past the mark: how the watch finds out whether Incus answers
// again.
func (c Client) Probe(ctx context.Context) error { return c.ping(ctx) }

// connectTimeout bounds making the shared connection, which holds the lock
// every other call waits on: a caller with no deadline would otherwise keep
// them all waiting on an Incus that doesn't answer.
const connectTimeout = 10 * time.Second
