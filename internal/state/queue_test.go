package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

func queued(project, name string) state.Agent {
	return state.Agent{Project: project, Name: name, Instance: "ab-" + project + "-" + name, AI: "none",
		Branch: "agentbox/" + name, Status: state.AgentQueued, CreatedAt: time.Now()}
}

func queueNames(t *testing.T, s *state.Store, project string) []string {
	t.Helper()
	q, err := s.Queue(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i, a := range q {
		if a.Position != i+1 {
			t.Errorf("%s is #%d at index %d", a.Name, a.Position, i)
		}
		out = append(out, a.Name)
	}
	return out
}

func TestQueue(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := s.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if err := s.Enqueue(ctx, queued("p", n), []byte(`{"n":"`+n+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Enqueue(ctx, queued("p", "a"), nil); !errors.Is(err, state.ErrExists) {
		t.Errorf("queueing a taken name: %v, want ErrExists", err)
	}
	if err := s.Enqueue(ctx, state.Agent{Project: "p", Name: "d", Status: state.AgentReady}, nil); err == nil {
		t.Error("a ready agent joined the queue")
	}
	if got := queueNames(t, s, "p"); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("queue = %v", got)
	}
	if req, err := s.QueuedAgentRequest(ctx, "p", "b"); err != nil || string(req) != `{"n":"b"}` {
		t.Errorf("request = %s, %v", req, err)
	}

	if err := s.MoveQueued(ctx, "p", "c", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveQueued(ctx, "p", "c", 99); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveQueued(ctx, "p", "b", 1); err != nil {
		t.Fatal(err)
	}
	if got := queueNames(t, s, "p"); got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Errorf("after moves: %v, want b a c", got)
	}
	if err := s.MoveQueued(ctx, "p", "zz", 1); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("moving an agent that isn't queued: %v", err)
	}

	// Starting one takes it out of line, once.
	a := queued("p", "b")
	a.Worktree = "/w/b"
	if err := s.StartQueued(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.StartQueued(ctx, a); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("starting it twice: %v, want ErrNotFound", err)
	}
	got, err := s.Agent(ctx, "p", "b")
	if err != nil || got.Status != state.AgentCreating || got.Worktree != "/w/b" {
		t.Errorf("started agent = %+v, %v", got, err)
	}
	// Removing one takes its place in line with it.
	if err := s.RemoveAgent(ctx, "p", "a"); err != nil {
		t.Fatal(err)
	}
	if got := queueNames(t, s, "p"); len(got) != 1 || got[0] != "c" {
		t.Errorf("queue after starting b and removing a: %v, want c", got)
	}
}

func TestTypicalMemoryPeak(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if peak, err := s.TypicalMemoryPeak(ctx, "p"); err != nil || peak != 0 {
		t.Fatalf("nothing seen: %d, %v", peak, err)
	}
	at := time.Now()
	for i, gb := range []int64{2, 9, 3, 1, 2} {
		if err := s.RecordUsagePeak(ctx, "p", string(rune('a'+i)), gb<<30, float64(i*10), at); err != nil {
			t.Fatal(err)
		}
	}
	// A lower reading doesn't lower a peak.
	if err := s.RecordUsagePeak(ctx, "p", "b", 1<<30, 5, at); err != nil {
		t.Fatal(err)
	}
	peaks, err := s.UsagePeaks(ctx, "p")
	if err != nil || peaks["b"].Memory != 9<<30 || peaks["b"].CPU != 10 || peaks["e"].CPU != 40 {
		t.Errorf("peaks = %v, %v", peaks, err)
	}
	if peak, _ := s.TypicalMemoryPeak(ctx, "p"); peak != 2<<30 {
		t.Errorf("median of 1 2 2 3 9 GiB = %d, want 2 GiB", peak>>30)
	}
	if peak, _ := s.TypicalMemoryPeak(ctx, "other"); peak != 0 {
		t.Errorf("another project's peak = %d", peak)
	}
}

func TestMemoryShape(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if base, burst, err := s.MemoryShape(ctx, "p"); err != nil || base != 0 || burst != 0 {
		t.Fatalf("nothing seen: %d, %d, %v", base, burst, err)
	}
	at := time.Now()
	const mib = int64(1) << 20
	// Three agents: their baselines while writing code, and what they peaked
	// at in their heavy phases (one never had one).
	for _, r := range []struct {
		agent       string
		base, burst int64
	}{{"a", 600, 3000}, {"b", 500, 0}, {"c", 700, 2700}} {
		if err := s.RecordPhasePeak(ctx, "p", r.agent, r.base*mib, false, at); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordPhasePeak(ctx, "p", r.agent, r.burst*mib, true, at); err != nil {
			t.Fatal(err)
		}
	}
	// A lower reading doesn't lower a peak; another project's don't count.
	if err := s.RecordPhasePeak(ctx, "p", "a", 100*mib, false, at); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPhasePeak(ctx, "q", "z", 9000*mib, false, at); err != nil {
		t.Fatal(err)
	}
	base, burst, err := s.MemoryShape(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	// Baselines 500, 600, 700: 600. Bursts over baseline 2400 and 2000.
	if base != 600*mib || burst < 2000*mib || burst > 2400*mib {
		t.Errorf("shape = %d MiB baseline, %d MiB burst", base/mib, burst/mib)
	}
}
