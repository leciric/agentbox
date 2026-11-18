package chv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// 18 Nov 2026 (UTC): the VM started at 11:34, 11:40, 11:48, 11:55, 12:01 and
// 12:08, each time without a stop line before it: the host's kernel had
// killed it, or the host had rebooted. The first start after two such deaths
// within 15 minutes, the third, is a safe one, and so is every one after;
// the first two aren't.
func TestRecordedCrashLoopStartsSafe(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	starts := []time.Time{at(11, 34), at(11, 40), at(11, 48), at(11, 55), at(12, 1), at(12, 8)}
	path := filepath.Join(dir, "runs.json")
	for i, start := range starts {
		rl := readRuns(path)
		b := rl.start(start)
		if want := i >= 2; b.Safe != want {
			t.Errorf("start at %s: safe %v, want %v (%s)", start.Format("15:04"), b.Safe, want, b.Why)
		}
		if b.Safe {
			if len(b.Crashes) < crashLoopCount || !b.SafeUntil.Equal(start.Add(safeFor)) || !strings.Contains(b.Why, "without a clean stop") {
				t.Errorf("start at %s: %+v", start.Format("15:04"), b)
			}
		}
		// It runs, seen every aliveEvery, until about a minute before the
		// next start, and dies with no end recorded.
		if i+1 < len(starts) {
			for seen := start.Add(aliveEvery); seen.Before(starts[i+1].Add(-time.Minute)); seen = seen.Add(aliveEvery) {
				rl.alive(seen)
			}
		}
	}
}

// Clean stops don't count, a stop the VM's own machine reported as an error
// does, and old deaths fall out of the window.
func TestCrashLoopCountsOnlyUncleanEndsLately(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	for _, tc := range []struct {
		name string
		runs []vmRun
		safe bool
	}{
		{"none", nil, false},
		{"two clean stops", []vmRun{
			{Started: ago(20), Ended: ago(12), Clean: true},
			{Started: ago(10), Ended: ago(2), Clean: true},
		}, false},
		{"killed twice", []vmRun{
			{Started: ago(20), Ended: ago(12), How: "signal: killed"},
			{Started: ago(10), Alive: ago(2)},
		}, true},
		{"one death, one clean stop", []vmRun{
			{Started: ago(20), Alive: ago(12)},
			{Started: ago(10), Ended: ago(2), Clean: true},
		}, false},
		{"two deaths, one long ago", []vmRun{
			{Started: ago(90), Alive: ago(60)},
			{Started: ago(10), Alive: ago(2)},
		}, false},
		{"died during its boot", []vmRun{
			{Started: ago(14)},
			{Started: ago(10), Alive: ago(2)},
		}, true},
	} {
		if b := bootFor(tc.runs, now); b.Safe != tc.safe {
			t.Errorf("%s: safe %v, want %v", tc.name, b.Safe, tc.safe)
		}
	}
}

// A run ends clean or not, the record keeps runsKept runs, and boot.json is
// what api.ReadVMBoot reads back.
func TestRunLogAndBootFile(t *testing.T) {
	dir := t.TempDir()
	l := Layout{Root: dir, Name: "agentbox"}
	if err := os.MkdirAll(l.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	for i := range runsKept + 5 {
		rl := readRuns(l.RunsFile())
		rl.start(now.Add(time.Duration(i) * time.Hour))
		rl.end(now.Add(time.Duration(i)*time.Hour+time.Minute), i%2 == 0, "stopped")
	}
	rl := readRuns(l.RunsFile())
	if len(rl.runs) != runsKept {
		t.Fatalf("kept %d runs, want %d", len(rl.runs), runsKept)
	}
	last := rl.runs[len(rl.runs)-1]
	if last.Ended.IsZero() || last.Clean != ((runsKept+4)%2 == 0) {
		t.Errorf("last run %+v", last)
	}
	// What the daemon reads, and nothing where there's no VM.
	want := api.VMBoot{Started: now, Safe: true, SafeUntil: now.Add(safeFor), Why: "why", Crashes: []time.Time{now.Add(-time.Minute)}}
	if err := writeBoot(l.BootFile(), want); err != nil {
		t.Fatal(err)
	}
	got := api.ReadVMBoot(l.Dir())
	if !got.Safe || !got.SafeUntil.Equal(want.SafeUntil) || got.Why != "why" || len(got.Crashes) != 1 {
		t.Errorf("ReadVMBoot = %+v", got)
	}
	if b := api.ReadVMBoot(""); b.Safe {
		t.Errorf("no VM: %+v", b)
	}
	if b := api.ReadVMBoot(t.TempDir()); b.Safe {
		t.Errorf("no file: %+v", b)
	}
}
