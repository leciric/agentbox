package lan

import (
	"sync"
	"time"
)

// Limiter counts failed attempts to get in — a pairing secret or a phone's
// token that was wrong — so that guessing either is slow, and shows in the
// log, once the page can be reached from the internet. Each address gets
// PerAddr failures a Window; pairing gets Total from all of them together, so
// a guesser that spreads over many addresses still gets few tries, while a
// phone that is already paired doesn't pair and is never held up by it.
type Limiter struct {
	PerAddr, Total int
	Window         time.Duration
	Now            func() time.Time // time.Now when nil; for tests

	mu    sync.Mutex
	start time.Time
	addr  map[string]int
	all   int
}

// NewLimiter is a Limiter with the limits the daemon uses.
func NewLimiter() *Limiter {
	return &Limiter{PerAddr: 20, Total: 100, Window: 10 * time.Minute}
}

func (l *Limiter) roll() {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	if t := now(); l.addr == nil || t.Sub(l.start) >= l.Window {
		l.start, l.addr, l.all = t, map[string]int{}, 0
	}
}

// Blocked says addr has failed too often to try again this window, and, when
// total is set, that everyone together has.
func (l *Limiter) Blocked(addr string, total bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	return l.addr[addr] >= l.PerAddr || total && l.all >= l.Total
}

// Fail counts a failed attempt from addr.
func (l *Limiter) Fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	l.addr[addr]++
	l.all++
}

// RetryAfter is how long until the window starts again.
func (l *Limiter) RetryAfter() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll()
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	return l.Window - now().Sub(l.start)
}
