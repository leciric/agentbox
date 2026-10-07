package daemon

import (
	"context"
	"math/rand/v2"
	"time"
)

// usageEventsEvery is how often pending events go out between reports.
const usageEventsEvery = 4 * time.Hour

// usageJitter is the longest delay after UTC midnight before the day's report
// goes out, so installs don't all reach the server at once.
const usageJitter = 30 * time.Minute

// usageClock is watchUsage's view of time, replaced in tests.
type usageClock struct {
	now    func() time.Time
	after  func(d time.Duration) <-chan time.Time
	jitter func() time.Duration
}

func realUsageClock() *usageClock {
	return &usageClock{
		now:    time.Now,
		after:  time.After,
		jitter: func() time.Duration { return rand.N(usageJitter) },
	}
}

// nextUsageReport is when the report after now is due: the next UTC midnight
// plus jitter.
func nextUsageReport(now time.Time, jitter time.Duration) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC).Add(jitter)
}

// watchUsage sends the usage counts shortly after each UTC midnight, when the
// day before them is over, and the pending events every usageEventsEvery,
// rather than waiting for the daily update check. It makes no update check,
// and sends nothing when the stats are off. Failures are silent, as the
// check's are: what wasn't sent waits for the next try.
func (s *Server) watchUsage(ctx context.Context, c *usageClock) {
	now := c.now()
	nextReport := nextUsageReport(now, c.jitter())
	nextEvents := now.Add(usageEventsEvery)
	for {
		next := nextEvents
		if nextReport.Before(next) {
			next = nextReport
		}
		select {
		case <-ctx.Done():
			return
		case <-c.after(max(next.Sub(c.now()), 0)):
		}
		now = c.now()
		reportDue := !now.Before(nextReport)
		if s.usageStatsOn(ctx) {
			if install, err := s.installID(ctx); err == nil {
				if reportDue {
					s.sendPendingUsage(ctx, install, now)
				} else {
					s.sendEventsOnly(ctx, install)
				}
			}
		}
		if reportDue {
			nextReport = nextUsageReport(now, c.jitter())
		}
		nextEvents = now.Add(usageEventsEvery)
	}
}
