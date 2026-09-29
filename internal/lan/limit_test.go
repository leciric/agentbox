package lan

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := &Limiter{PerAddr: 2, Total: 3, Window: time.Minute, Now: func() time.Time { return now }}
	if l.Blocked("a", true) {
		t.Fatal("blocked before any failure")
	}
	l.Fail("a")
	l.Fail("a")
	if !l.Blocked("a", false) || l.Blocked("b", true) {
		t.Fatal("a should be blocked after two failures, b not")
	}
	l.Fail("b")
	if !l.Blocked("c", true) || l.Blocked("c", false) {
		t.Fatal("three failures in all block pairing for everyone, and nothing else")
	}
	if got := l.RetryAfter(); got != time.Minute {
		t.Fatalf("RetryAfter %s", got)
	}
	now = now.Add(time.Minute)
	if l.Blocked("a", true) {
		t.Fatal("still blocked in the next window")
	}
}
