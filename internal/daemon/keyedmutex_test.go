package daemon

import (
	"testing"
	"time"
)

// One agent's wake doesn't wait for another's; two of the same agent's do
// wait for each other, and the lock is forgotten once nobody holds it.
func TestKeyedMutexHoldsOnlyItsKey(t *testing.T) {
	t.Parallel()
	var k keyedMutex
	unlockA := k.lock("p/a")
	done := make(chan struct{})
	go func() { k.lock("p/b")(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("p/b waited for p/a's lock")
	}
	again := make(chan struct{})
	go func() { k.lock("p/a")(); close(again) }()
	select {
	case <-again:
		t.Fatal("p/a locked twice at once")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	select {
	case <-again:
	case <-time.After(5 * time.Second):
		t.Fatal("p/a's second lock never got it")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.locks) != 0 {
		t.Errorf("locks left: %v", k.locks)
	}
}
