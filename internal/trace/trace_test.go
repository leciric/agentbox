package trace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestPercentile(t *testing.T) {
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i + 1) // 1..100
	}
	for _, c := range []struct {
		p    float64
		want float64
	}{{0.5, 50}, {0.95, 95}, {0.99, 99}, {1, 100}, {0, 1}} {
		if got := Percentile(values, c.p); got != c.want {
			t.Errorf("p%v = %v, want %v", c.p*100, got, c.want)
		}
	}
	if got := Percentile([]float64{7}, 0.95); got != 7 {
		t.Errorf("p95 of one = %v, want 7", got)
	}
	if got := Percentile(nil, 0.5); got != 0 {
		t.Errorf("p50 of none = %v, want 0", got)
	}
	// Nearest rank, never an interpolation: p50 of two is the lower one.
	if got := Percentile([]float64{10, 20}, 0.5); got != 10 {
		t.Errorf("p50 of 10, 20 = %v, want 10", got)
	}
}

func TestSummarize(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var spans []Span
	for i := 1; i <= 20; i++ {
		s := Span{Name: "GET /v1/agents", Start: at.Add(time.Duration(i) * time.Second), DurationMS: float64(i)}
		if i%10 == 0 {
			s.Error = "boom"
		}
		spans = append(spans, s)
	}
	spans = append(spans, Span{Name: "job create", Start: at, DurationMS: 5000})
	sum := Summarize(spans, 3)
	if len(sum.Stats) != 2 || sum.Stats[0].Name != "job create" {
		t.Fatalf("stats = %+v, want job create first (most total time)", sum.Stats)
	}
	got := sum.Stats[1]
	want := api.SpanStats{Name: "GET /v1/agents", Count: 20, Errors: 2, P50MS: 10, P95MS: 19, MaxMS: 20, TotalMS: 210}
	if got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
	if len(sum.Slowest) != 3 || sum.Slowest[0].DurationMS != 5000 || sum.Slowest[1].DurationMS != 20 {
		t.Errorf("slowest = %+v", sum.Slowest)
	}
	if len(sum.Failures) != 2 || sum.Failures[0].DurationMS != 20 || sum.Failures[1].DurationMS != 10 {
		t.Errorf("failures = %+v, want newest first", sum.Failures)
	}
	if empty := Summarize(nil, 5); empty.Stats == nil || empty.Failures == nil || empty.Slowest == nil {
		t.Error("an empty summary has nil lists, which JSON makes null")
	}
}

func TestRotationKeepsWholeLinesAndCapsSize(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenLimits(dir, Limits{MaxBytes: 2000, MaxFiles: 2}, Limits{MaxBytes: 1000, MaxFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	for i := range 200 {
		r.Record(Span{Name: "n", Start: at, DurationMS: float64(i), Attrs: map[string]any{"i": i}})
		if i%10 == 0 {
			// Let the writer catch up, so the batches are small and rotate.
			waitQueue(t, r)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SpansFile)
	var total int64
	for _, p := range []string{path, Backup(path, 1), Backup(path, 2)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(p), err)
		}
		total += info.Size()
		if info.Size() > 2000+200 { // a batch may take a file one batch over
			t.Errorf("%s is %d bytes, over the cap", filepath.Base(p), info.Size())
		}
		b, _ := os.ReadFile(p)
		if !strings.HasSuffix(string(b), "\n") {
			t.Errorf("%s ends mid-line", filepath.Base(p))
		}
	}
	if _, err := os.Stat(Backup(path, 3)); err == nil {
		t.Error("a third backup was kept")
	}
	spans, err := Read(dir, SpansFile, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) == 0 || len(spans) == 200 {
		t.Fatalf("read %d spans: rotation should have dropped the oldest", len(spans))
	}
	// Oldest first, the newest kept.
	if last := spans[len(spans)-1]; last.DurationMS != 199 {
		t.Errorf("last span = %v, want 199", last.DurationMS)
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].DurationMS != spans[i-1].DurationMS+1 {
			t.Fatalf("spans out of order at %d: %v then %v", i, spans[i-1].DurationMS, spans[i].DurationMS)
		}
	}
}

func waitQueue(t *testing.T, r *Recorder) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(r.queue) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the writer never caught up")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStallsGoToTheirOwnFile(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(r)
	defer SetDefault(nil)

	ctx, parent := Start(context.Background(), "GET /v1/agents", "route", "x")
	_, child := Start(ctx, "incus list")
	child.End(errors.New("incus isn't answering"))
	parent.Warn("slow request")
	parent.End(nil)
	parent.End(errors.New("ignored: only the first End counts"))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	spans, _ := Read(dir, SpansFile, time.Time{})
	if len(spans) != 2 {
		t.Fatalf("spans = %+v", spans)
	}
	if spans[0].Parent != spans[1].ID || spans[0].Error == "" {
		t.Errorf("child = %+v, want parent %s and an error", spans[0], spans[1].ID)
	}
	stalls, _ := Read(dir, StallsFile, time.Time{})
	if len(stalls) != 1 || stalls[0].Name != "GET /v1/agents" || stalls[0].Attrs["stall"] != "slow request" || stalls[0].Error != "" {
		t.Errorf("stalls = %+v", stalls)
	}
}

func TestReadSkipsOldSpansAndBrokenLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	lines := []string{}
	for _, s := range []Span{
		{Name: "old", Start: now.Add(-2 * time.Hour), DurationMS: 1},
		{Name: "long", Start: now.Add(-2 * time.Hour), DurationMS: float64((2*time.Hour - time.Minute) / time.Millisecond)},
		{Name: "new", Start: now.Add(-time.Minute), DurationMS: 1},
	} {
		b, _ := marshal(s)
		lines = append(lines, string(b))
	}
	body := strings.Join(lines, "") + `{"name":"cut off`
	if err := os.WriteFile(filepath.Join(dir, SpansFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	spans, err := Read(dir, SpansFile, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 || spans[0].Name != "long" || spans[1].Name != "new" {
		t.Errorf("spans = %+v, want the two that ended in the last 30 minutes", spans)
	}
	if none, err := Read(t.TempDir(), SpansFile, time.Time{}); err != nil || len(none) != 0 {
		t.Errorf("no file: %v, %v", none, err)
	}
}

func TestNothingRecordingCostsNothing(t *testing.T) {
	SetDefault(nil)
	ctx := context.Background()
	got, l := Start(ctx, "x", "k", "v")
	if got != ctx || l != nil {
		t.Fatal("Start with no recorder made a span")
	}
	l.Set("a", 1)
	l.Warn("w")
	l.End(errors.New("e")) // a nil *Live does nothing
	if n := testing.AllocsPerRun(100, func() {
		_, l := Start(ctx, "x")
		l.End(nil)
	}); n != 0 {
		t.Errorf("a span with no recorder allocates %v times", n)
	}
}

func TestAFullQueueDrops(t *testing.T) {
	r := &Recorder{queue: make(chan Span, 1)}
	r.Record(Span{Name: "a"})
	r.Record(Span{Name: "b"})
	if r.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", r.Dropped())
	}
	close(r.queue)
	r.Record(Span{Name: "after close"})
	if r.Dropped() != 2 {
		t.Errorf("dropped = %d, want 2 after a close", r.Dropped())
	}
}

func BenchmarkSpan(b *testing.B) {
	r, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	SetDefault(r)
	defer func() { SetDefault(nil); _ = r.Close() }()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, l := Start(ctx, "GET /v1/agents", "status", 200)
		l.End(nil)
	}
}
