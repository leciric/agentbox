package daemon

import (
	"testing"
	"time"

	"agentbox/internal/pressure"
)

// Short starts with the first reading that holds runs back, and ends only
// once memory has been calm for memoryShortCalm; only the latest state waits
// to be told.
func TestMemoryShortFlipsWithACalmBeforeEnding(t *testing.T) {
	t.Parallel()
	m := newMemoryShort()
	now := time.Unix(1_000_000, 0)
	told := func() (bool, bool) {
		select {
		case v := <-m.changed:
			return v, true
		default:
			return false, false
		}
	}
	m.note(now, "")
	if _, ok := told(); ok {
		t.Fatal("told with memory never short")
	}
	m.note(now, pressure.Falling)
	if v, ok := told(); !ok || !v {
		t.Fatalf("falling: told %v %v, want short", v, ok)
	}
	m.note(now.Add(2*time.Second), pressure.Stalling)
	m.note(now.Add(4*time.Second), pressure.Easing)
	m.note(now.Add(time.Minute), "")
	if _, ok := told(); ok {
		t.Fatal("told again before it was calm long enough")
	}
	// Short again in between starts the calm over.
	m.note(now.Add(90*time.Second), pressure.Low)
	m.note(now.Add(5*time.Minute), "")
	m.note(now.Add(5*time.Minute+memoryShortCalm-time.Second), "")
	if _, ok := told(); ok {
		t.Fatal("the calm didn't start over")
	}
	m.note(now.Add(5*time.Minute+memoryShortCalm), "")
	if v, ok := told(); !ok || v {
		t.Fatalf("calm: told %v %v, want not short", v, ok)
	}
	// Untaken states are replaced, never queued behind each other.
	m.note(now.Add(time.Hour), pressure.Churning)
	m.note(now.Add(time.Hour+time.Second), "")
	m.note(now.Add(time.Hour+time.Second+memoryShortCalm), "")
	if v, ok := told(); !ok || v {
		t.Fatalf("after a short episode no one took: told %v %v, want not short", v, ok)
	}
}
