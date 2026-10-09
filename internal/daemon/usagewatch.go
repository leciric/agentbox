package daemon

import (
	"context"
	"math/rand/v2"
	"time"
)

const (
	// usageEventsEvery is how often pending events go out.
	usageEventsEvery = 15 * time.Minute
	// usageRetryFirst is the wait after a failed send, doubling with each
	// failure up to usageEventsEvery.
	usageRetryFirst = time.Minute
	// usageJitter is the longest delay after UTC midnight before the day's
	// counts go out, so installs don't all reach the server at once.
	usageJitter = 30 * time.Minute
)

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

// nextUsageReport is when the counts after now are due: the next UTC
// midnight plus jitter.
func nextUsageReport(now time.Time, jitter time.Duration) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC).Add(jitter)
}

// usageRetry is the wait after the nth failure in a row.
func usageRetry(fails int) time.Duration {
	d := usageRetryFirst
	for i := 1; i < fails && d < usageEventsEvery; i++ {
		d *= 2
	}
	return min(d, usageEventsEvery)
}

// watchUsage sends the usage counts shortly after each UTC midnight, when the
// day before them is over, and the pending events every usageEventsEvery,
// retrying a failed send with a growing wait, rather than leaving both to the
// daily ping. It makes no update check, and sends nothing when the stats are
// off. Failures are silent, as the check's are.
func (s *Server) watchUsage(ctx context.Context, c *usageClock) {
	now := c.now()
	nextReport := nextUsageReport(now, c.jitter())
	nextEvents := now.Add(usageEventsEvery)
	fails := 0
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
		var sendErr error
		if s.usageStatsOn(ctx) {
			if install, err := s.installID(ctx); err == nil {
				if reportDue {
					s.sendPendingUsage(ctx, install, now)
				}
				sendErr = s.sendEventsOnly(ctx, install)
			}
		}
		if reportDue {
			nextReport = nextUsageReport(now, c.jitter())
		}
		if sendErr != nil {
			fails++
			nextEvents = now.Add(usageRetry(fails))
		} else {
			fails = 0
			nextEvents = now.Add(usageEventsEvery)
		}
	}
}
