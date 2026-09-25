package daemon

import (
	"context"
	"time"
)

// mediaSweepInterval is how often the daemon checks for expired media. A
// 30-day default retention doesn't need finer than this, and it's cheap
// enough to also run once at startup, so a daemon that was off for a while
// (or never running long enough to hit the ticker) still catches up.
const mediaSweepInterval = time.Hour

// sweepMedia periodically removes kept media whose retention period has
// passed: the row and its file together. Media whose agent still exists is
// never touched, however old.
func (s *Server) sweepMedia(ctx context.Context) {
	s.sweepExpiredMedia(ctx, time.Now())
	s.firstSweeps.Done()
	ticker := time.NewTicker(mediaSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepExpiredMedia(ctx, time.Now())
		}
	}
}

// sweepExpiredMedia does one pass, as of now. Split out from sweepMedia so
// tests can fake the clock instead of waiting on retention periods.
func (s *Server) sweepExpiredMedia(ctx context.Context, now time.Time) {
	items, err := s.store.ExpiredMedia(ctx, now)
	if err != nil {
		s.logf("sweep media: %v", err)
		return
	}
	if len(items) == 0 {
		return
	}
	m := s.manager(s.cfg.Log)
	removed := 0
	for _, item := range items {
		if err := m.DeleteMedia(ctx, item); err != nil {
			s.logf("sweep media %s: %v", item.ID, err)
			continue
		}
		removed++
	}
	s.logf("media sweep: removed %d expired item(s)", removed)
}
