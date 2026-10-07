package daemon

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
)

// fakeUsageClock is a clock the test moves: after answers once it has been
// advanced past the wait.
type fakeUsageClock struct {
	mu      sync.Mutex
	t       time.Time
	waiters []fakeWait
}

type fakeWait struct {
	at time.Time
	ch chan time.Time
}

func (f *fakeUsageClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeUsageClock) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	f.waiters = append(f.waiters, fakeWait{f.t.Add(d), ch})
	return ch
}

// advance moves time to t and wakes the waits that are due.
func (f *fakeUsageClock) advance(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if !w.at.After(t) {
			w.ch <- t
		} else {
			kept = append(kept, w)
		}
	}
	f.waiters = kept
}

func (f *fakeUsageClock) waiting() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

func TestNextUsageReport(t *testing.T) {
	now := time.Date(2026, 10, 7, 23, 59, 0, 0, time.FixedZone("x", -3*3600)) // 02:59 UTC on the 8th
	want := time.Date(2026, 10, 9, 0, 10, 0, 0, time.UTC)
	if got := nextUsageReport(now, 10*time.Minute); !got.Equal(want) {
		t.Errorf("nextUsageReport = %v, want %v", got, want)
	}
}

func TestUsageIsSentAfterMidnightAndEventsEveryFifteenMinutes(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	var fake fakeUsage
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	ctx := context.Background()
	waitFor(t, "the check as the daemon starts", func() bool {
		status, err := d.client.Update(ctx)
		return err == nil && status.CheckedAt != nil
	})
	checks := len(fake.sent())

	start := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	clock := &fakeUsageClock{t: start}
	uc := &usageClock{now: clock.now, after: clock.after, jitter: func() time.Duration { return 20 * time.Minute }}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.srv.watchUsage(ctx, uc) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "the loop to wait", func() bool { return clock.waiting() == 1 })

	_ = d.srv.store.CountFeature(ctx, "2026-10-07", api.FeatureAgentCreateClaude)
	d.srv.recordEvent(eventHeartbeat, heartbeatEvent{})

	// A server that's down: the events wait, and the retry comes a minute on.
	fake.answerEvents(http.StatusServiceUnavailable)
	at := start.Add(15 * time.Minute)
	clock.advance(at)
	waitFor(t, "the loop to wait", func() bool { return clock.waiting() == 1 })
	if n := pendingUsage(t, d).EventsWaiting; n != 1 {
		t.Fatalf("after a 503, %d waiting, want 1", n)
	}
	fake.answerEvents(http.StatusOK)
	clock.advance(at.Add(time.Minute))
	waitFor(t, "events", func() bool { return len(fake.sentEvents()[eventHeartbeat]) == 1 })
	if n := len(fake.sent()); n != checks {
		t.Errorf("sent %d usage reports before midnight, want none", n)
	}

	waitFor(t, "the loop to wait", func() bool { return clock.waiting() == 1 })

	// Past midnight and the jitter, the day's counts go out.
	clock.advance(time.Date(2026, 10, 8, 0, 21, 0, 0, time.UTC))
	waitFor(t, "the report", func() bool { return len(fake.sent()) == checks+1 })

	waitFor(t, "the loop to wait", func() bool { return clock.waiting() == 1 })

	// Off, the next wake-ups send nothing.
	if _, err := patchSettings(t, d, `{"usageStats": false}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the loop to wait", func() bool { return clock.waiting() == 1 })
	_ = d.srv.store.CountFeature(ctx, "2026-10-08", api.FeatureAgentCreateClaude)
	clock.advance(time.Date(2026, 10, 9, 0, 40, 0, 0, time.UTC))
	waitFor(t, "the loop to wait again", func() bool { return clock.waiting() == 1 })
	if n := len(fake.sent()); n != checks+1 {
		t.Errorf("sent %d reports with the stats off", n-checks-1)
	}
}
