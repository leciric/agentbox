package chv

import (
	"os"
	"testing"
	"time"
)

func TestShareTrimmer(t *testing.T) {
	const limit = 524288
	t0 := time.Date(2026, 10, 21, 20, 22, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	var tr shareTrimmer

	// Low and steady: nothing to do.
	for i, open := range []int64{40_000, 41_000, 42_000} {
		if trim, why := tr.sample(open, limit, at(float64(2*i))); trim {
			t.Fatalf("trimmed at %d open: %s", open, why)
		}
	}
	// A walk of node_modules: 30k a second would pass 75% within 15 s.
	if trim, why := tr.sample(102_000, limit, at(6)); !trim || why != "growing by 30000 a second" {
		t.Fatalf("a fast climb didn't trim: %v %q", trim, why)
	}
	tr.trimmed(at(6))
	// Too soon after a drop.
	if trim, _ := tr.sample(300_000, limit, at(8)); trim {
		t.Fatal("trimmed again within shareTrimGap")
	}
	// What stays after a drop is pinned: past the limit's 40% but within the
	// slack of it, dropping again is no use.
	tr = shareTrimmer{}
	tr.trimmed(at(0))
	tr.sample(230_000, limit, at(2))
	if tr.floor != 230_000 {
		t.Fatalf("floor = %d, want what the drop left", tr.floor)
	}
	if trim, _ := tr.sample(240_000, limit, at(10)); trim {
		t.Fatal("trimmed what is pinned")
	}
	if trim, why := tr.sample(260_000, limit, at(30)); !trim || why != "past 40% of the limit" {
		t.Fatalf("didn't trim past the floor's slack: %v %q", trim, why)
	}
	tr.trimmed(at(30))
	r := tr.report(260_000, limit)
	if r.Trims != 2 || r.LastTrim == nil || !r.LastTrim.Equal(at(30)) || r.Pinned != 230_000 {
		t.Fatalf("report = %+v", r)
	}
	// With no limit known, it never trims.
	if trim, _ := (&shareTrimmer{}).sample(900_000, 0, t0); trim {
		t.Fatal("trimmed with no limit")
	}
}

func TestParseOpenFileLimit(t *testing.T) {
	limits := "Limit                     Soft Limit           Hard Limit           Units     \n" +
		"Max cpu time              unlimited            unlimited            seconds   \n" +
		"Max open files            524288               524288               files     \n"
	if n := parseOpenFileLimit(limits); n != 524288 {
		t.Fatalf("got %d", n)
	}
	if n := parseOpenFileLimit("Max open files            unlimited  unlimited files\n"); n != 0 {
		t.Fatalf("unlimited: got %d", n)
	}
}

func TestCountFDs(t *testing.T) {
	n, err := countFDs(os.Getpid())
	if err != nil {
		t.Skip("no /proc here")
	}
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if m, _ := countFDs(os.Getpid()); m != n+1 {
		t.Fatalf("%d open, then %d with one more", n, m)
	}
}
