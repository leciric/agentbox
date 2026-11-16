package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeTrim struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string
	err   map[string]error
	done  chan struct{}
}

func (f *fakeTrim) run(_ context.Context, mount string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, mount)
	if f.done != nil {
		select {
		case f.done <- struct{}{}:
		default:
		}
	}
	return f.out[mount], f.err[mount]
}

func (f *fakeTrim) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestTrimmer(f *fakeTrim, mounts ...string) *trimmer {
	return &trimmer{
		mounts: func() []string { return mounts },
		run:    f.run,
		busy:   func() bool { return false },
		logf:   func(string, ...any) {},
		kick:   make(chan struct{}, 1),
	}
}

func TestTrimOnceAddsWhatEachMountGaveBack(t *testing.T) {
	f := &fakeTrim{out: map[string]string{
		"/":                 "/: 1 GiB (1073741824 bytes) trimmed on /dev/vda1\n",
		"/var/lib/agentbox": "/var/lib/agentbox: 2 GiB (2147483648 bytes) trimmed on /dev/vdb\n",
	}}
	tr := newTestTrimmer(f, "/", "/var/lib/agentbox")
	res := tr.trimOnce(context.Background())
	if res.Bytes != 3<<30 || res.Err != "" {
		t.Fatalf("got %+v, want 3 GiB and no error", res)
	}
	if got := tr.Last(); got.Bytes != res.Bytes || got.At.IsZero() {
		t.Fatalf("Last() = %+v, want the trim just made", got)
	}
}

func TestTrimSkipsADiskWithoutDiscardFromThenOn(t *testing.T) {
	f := &fakeTrim{
		out: map[string]string{"/": "fstrim: /: the discard operation is not supported\n"},
		err: map[string]error{"/": errors.New("exit status 1")},
	}
	tr := newTestTrimmer(f, "/")
	if res := tr.trimOnce(context.Background()); res.Err != "" {
		t.Fatalf("an unsupported disk is no error: %+v", res)
	}
	tr.trimOnce(context.Background())
	if n := f.count(); n != 1 {
		t.Fatalf("fstrim ran %d times on a disk without discard, want once", n)
	}
}

func TestTrimReportsOtherFailures(t *testing.T) {
	f := &fakeTrim{err: map[string]error{"/": errors.New("sudo: a password is required")}}
	tr := newTestTrimmer(f, "/")
	if res := tr.trimOnce(context.Background()); res.Err == "" {
		t.Fatal("a failed fstrim should be reported")
	}
	tr.trimOnce(context.Background())
	if n := f.count(); n != 2 {
		t.Fatalf("a failure that isn't 'not supported' should be tried again; ran %d times", n)
	}
}

// stepClock hands the loop timers that fire only when the test says.
type stepClock struct{ fire chan time.Time }

func (c stepClock) after(time.Duration) <-chan time.Time { return c.fire }

func TestTrimLoopWaitsForKicksToSettleAndForHeavyRuns(t *testing.T) {
	f := &fakeTrim{done: make(chan struct{}, 4)}
	tr := newTestTrimmer(f, "/")
	var mu sync.Mutex
	busyCalls := 0
	// A heavy run is under way for the first two looks.
	tr.busy = func() bool { mu.Lock(); defer mu.Unlock(); busyCalls++; return busyCalls <= 2 }
	clock := stepClock{fire: make(chan time.Time)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.loop(ctx, time.Hour, time.Hour, time.Minute, time.Minute, clock.after)

	tr.Kick()
	clock.fire <- time.Time{} // the removals settle
	clock.fire <- time.Time{} // the first wait for the heavy run
	if n := f.count(); n != 0 {
		t.Fatalf("trimmed %d times during a heavy run", n)
	}
	clock.fire <- time.Time{} // the second: the run has ended
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no trim once the heavy run ended")
	}
	if n := f.count(); n != 1 {
		t.Fatalf("trimmed %d times, want once", n)
	}
}
