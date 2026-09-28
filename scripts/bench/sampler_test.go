package main

import (
	"strings"
	"testing"
	"time"
)

func TestParsePressure(t *testing.T) {
	p := parsePressure("some avg10=0.08 avg60=5.02 avg300=15.29 total=1492496860\nfull avg10=0.03 avg60=4.32 avg300=11.17 total=1151566614\n")
	if p.some != 1492496860 || p.full != 1151566614 {
		t.Fatalf("got %+v", p)
	}
}

func TestParseCommitStats(t *testing.T) {
	c := parseCommitStats("commits 1240\ncur_commit_ms 0\nlast_commit_ms 25\nmax_commit_ms 20342\ntotal_commit_ms 470965\n")
	if c != (commitStats{Commits: 1240, LastMs: 25, MaxMs: 20342, TotalMs: 470965}) {
		t.Fatalf("got %+v", c)
	}
}

func TestParseMeminfo(t *testing.T) {
	mi := parseMeminfo("MemTotal:       30000000 kB\nMemAvailable:   20000000 kB\nHugePages_Total:       0\n")
	if mi["MemTotal"] != 30000000*1024 || mi["MemAvailable"] != 20000000*1024 || mi["HugePages_Total"] != 0 {
		t.Fatalf("got %v", mi)
	}
}

func TestSummarizeLatency(t *testing.T) {
	var ms []float64
	for i := 1; i <= 100; i++ {
		ms = append(ms, float64(i))
	}
	ms = append(ms, 20000) // one probe stuck behind a 20 s commit
	l := summarizeLatency(ms)
	if l.Count != 101 || l.P50 != 51 || l.P95 != 96 || l.Max != 20000 || l.Over100ms != 1 || l.Over1s != 1 {
		t.Fatalf("got %+v", l)
	}
	if (summarizeLatency(nil) != latency{}) {
		t.Fatal("no probes should summarize to zero")
	}
}

func TestWindowSummarizesOnlyItsStretch(t *testing.T) {
	s := &sampler{start: time.Now()}
	s.fsync = []fsyncSample{{T: 1, Ms: 1}, {T: 5, Ms: 4000}, {T: 6, Ms: 2}, {T: 20, Ms: 9000}}
	s.host = []hostSample{
		{T: 4, IOSome: 10, IOFull: 5, WriteMiBs: 100, Btrfs: &commitStats{Commits: 10, TotalMs: 1000, LastMs: 50, MaxMs: 20000}},
		{T: 5, IOSome: 90, IOFull: 60, WriteMiBs: 300, Btrfs: &commitStats{Commits: 11, TotalMs: 3000, LastMs: 2000, MaxMs: 20000}},
		{T: 6, IOSome: 20, IOFull: 10, WriteMiBs: 200, HeldGiB: 3, Btrfs: &commitStats{Commits: 12, TotalMs: 3100, LastMs: 100, CurMs: 700, MaxMs: 20000}},
		{T: 30, IOSome: 100, Btrfs: &commitStats{Commits: 99, TotalMs: 99999}},
	}
	w := s.window("write", 4, 6)
	if w.Fsync.Count != 2 || w.Fsync.Max != 4000 {
		t.Errorf("fsync: %+v", w.Fsync)
	}
	if w.IOSomeMax != 90 || w.IOSomeAvg != 40 || w.IOFullMax != 60 {
		t.Errorf("pressure: some %.1f/%.1f full %.1f", w.IOSomeAvg, w.IOSomeMax, w.IOFullMax)
	}
	// Two commits ended in the window, 2100 ms between them; the longest
	// was 2000 ms, and none since mount beat the 20 s one from before.
	if w.Commits != 2 || w.CommitMeanMs != 1050 || w.CommitMaxMs != 2000 {
		t.Errorf("commits: %d, mean %.0f, max %.0f", w.Commits, w.CommitMeanMs, w.CommitMaxMs)
	}
	if w.WriteMiB != 600 || w.WriteMiBsMax != 300 || w.HeldMaxGiB != 3 {
		t.Errorf("disk %.0f MiB (max %.0f/s), held %.0f", w.WriteMiB, w.WriteMiBsMax, w.HeldMaxGiB)
	}
}

func TestMountOfFindsTheLongestMount(t *testing.T) {
	fstype, source := mountOf("/proc/self")
	if fstype != "proc" || source == "" {
		t.Fatalf("/proc/self is on %q from %q", fstype, source)
	}
}

func TestMarkdownTable(t *testing.T) {
	res := &results{
		Options: map[string]any{"writeGiB": 10.0, "reclaimWait": "1m0s"},
		Modes: []*modeResult{
			{Mode: "containers", Metrics: map[string]float64{"agent.create": 6.5, "mem.agents1.delta": 0.34, "mem.agents1.held": 1.07},
				Notes: map[string]string{"agent.create": "failed once"}, Windows: map[string]*window{
					"write": {Fsync: latency{Count: 300, P50: 3.1, P95: 861.4, Max: 4160, Over100ms: 20, Over1s: 3}},
				}},
			{Mode: "chproto", Metrics: map[string]float64{"agent.create": 1.2, "vm.pause": 0.002}, Errors: []string{"boom"}},
		},
	}
	md := markdown(res)
	for _, want := range []string{
		"| Measure | containers | chproto |",
		"| Agent create (median of 5) | 6.5 s ¹ | 1.2 s |",
		"| VM pause | — | 2 ms |",
		"| Memory: 1 agent | +0.34 (1.07) | — |",
		"| Sustained write, and its writeback: host fsync p50 / p95 / max, ms | 3.1 / 861.4 / 4160 | — |",
		"| Sustained write, and its writeback: fsyncs over 100 ms / over 1 s | 20 / 3 of 300 | — |",
		"- containers, Agent create (median of 5): failed once",
		"- chproto: boom",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Build, one agent") {
		t.Error("a row no mode has should be left out")
	}
}

func TestShq(t *testing.T) {
	for in, want := range map[string]string{"plain-word_1.2": "plain-word_1.2", "two words": "'two words'", "it's": `'it'\''s'`, "": "''"} {
		if got := shq(in); got != want {
			t.Errorf("shq(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"16G": 16 << 30, "16GiB": 16 << 30, "512M": 512 << 20, "1.5G": 3 << 29} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestGoVersionOf(t *testing.T) {
	v, err := goVersionOf("../..")
	if err != nil || strings.Count(v, ".") != 2 {
		t.Fatalf("go.mod's Go: %q, %v", v, err)
	}
}

func TestQuickKeepsWhatWasGiven(t *testing.T) {
	o, err := parseFlags([]string{"--quick", "--write-gib", "3", "--repo", "."})
	if err != nil {
		t.Fatal(err)
	}
	if o.writeGiB != 3 || o.npm != "" || o.idleWindow != 5*time.Second {
		t.Fatalf("write %.0f, npm %q, idle %s", o.writeGiB, o.npm, o.idleWindow)
	}
	if _, err := parseFlags([]string{"--modes", "containers,lima", "--repo", "."}); err == nil {
		t.Fatal("an unknown mode should be refused")
	}
}
