package hostload

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func calm() Reading {
	return Reading{OS: "linux", Memory: 0, CPU: 5, IO: 3, CPUShare: -1, MemoryTotal: 32 << 30, Available: 20 << 30}
}

func TestJudgeThresholds(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Reading)
		why  string
	}{
		{"calm", func(*Reading) {}, ""},
		{"memory stall", func(r *Reading) { r.Memory = 12 }, "memory"},
		{"below the reserve", func(r *Reading) { r.Available = 7 << 30 }, "memory"},
		{"cpu", func(r *Reading) { r.CPU = 60 }, "cpu"},
		{"io", func(r *Reading) { r.IO = 45 }, "io"},
		{"windows cpu", func(r *Reading) { r.CPU, r.CPUShare = -1, 95 }, "cpu"},
		{"mac warn", func(r *Reading) { r.Memory, r.Available, r.Level = -1, 0, "warn" }, "memory"},
		{"mac normal", func(r *Reading) { r.Memory, r.Available, r.Level = -1, 0, "normal" }, ""},
	} {
		r := calm()
		tc.edit(&r)
		var j Judge
		got := j.Judge(r, t0)
		if got.Why != tc.why || got.Busy != (tc.why != "") || got.Reclaim != (tc.why == "memory") {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

func TestJudgeCalmsAfterAWhile(t *testing.T) {
	var j Judge
	busy := calm()
	busy.IO = 80
	if !j.Judge(busy, t0).Busy {
		t.Fatal("not busy")
	}
	now := t0.Add(time.Second)
	if l := j.Judge(calm(), now); !l.Busy || l.Why != "" {
		t.Fatalf("calm at once: %+v", l)
	}
	if !j.Judge(calm(), now.Add(CalmAfter-time.Second)).Busy {
		t.Fatal("calm before CalmAfter")
	}
	// Another burst starts the wait over.
	j.Judge(busy, now.Add(CalmAfter-time.Second/2))
	if !j.Judge(calm(), now.Add(CalmAfter+time.Second)).Busy {
		t.Fatal("a burst didn't start the wait over")
	}
	if j.Judge(calm(), now.Add(3*CalmAfter)).Busy {
		t.Fatal("still busy long after")
	}
}

func TestReserve(t *testing.T) {
	if Reserve(8<<30) != 4<<30 || Reserve(64<<30) != 16<<30 {
		t.Fatalf("Reserve: %d %d", Reserve(8<<30), Reserve(64<<30))
	}
}
