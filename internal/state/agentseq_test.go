package state_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"agentbox/internal/state"
)

func TestNextAgentNameCountsUp(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	for i, want := range []string{"agent-01", "agent-02", "agent-03"} {
		got, err := st.NextAgentName(ctx, "pawly", 1)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got != want {
			t.Errorf("call %d = %q, want %q", i, got, want)
		}
	}
}

func TestNextAgentNameNeverGoesBackwards(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	if got, err := st.NextAgentName(ctx, "pawly", 1); err != nil || got != "agent-01" {
		t.Fatalf("first call = %q, %v", got, err)
	}
	// A floor above the counter's own idea jumps it forward...
	if got, err := st.NextAgentName(ctx, "pawly", 10); err != nil || got != "agent-10" {
		t.Fatalf("raised floor = %q, %v, want agent-10", got, err)
	}
	// ...but a floor from a caller with stale information never pulls it back.
	if got, err := st.NextAgentName(ctx, "pawly", 2); err != nil || got != "agent-11" {
		t.Fatalf("stale floor = %q, %v, want agent-11", got, err)
	}
}

func TestNextAgentNameSeparatesProjects(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	for _, name := range []string{"pawly", "other"} {
		if err := st.AddProject(ctx, state.Project{Name: name, Root: "/src/" + name, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.NextAgentName(ctx, "pawly", 1); err != nil || got != "agent-01" {
		t.Fatalf("pawly = %q, %v", got, err)
	}
	if got, err := st.NextAgentName(ctx, "pawly", 1); err != nil || got != "agent-02" {
		t.Fatalf("pawly again = %q, %v", got, err)
	}
	if got, err := st.NextAgentName(ctx, "other", 1); err != nil || got != "agent-01" {
		t.Fatalf("other = %q, %v, want its own counter", got, err)
	}
}

// TestNextAgentNameConcurrent is the race fixed alongside name reuse: two
// creates reserving a name at the same moment must never both get agent-07 —
// each caller's transaction (the store's connection opens with
// _txlock=immediate) waits for the other's write lock rather than racing it.
func TestNextAgentNameConcurrent(t *testing.T) {
	ctx := context.Background()
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	const n = 20
	names := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			names[i], errs[i] = st.NextAgentName(ctx, "pawly", 1)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if seen[names[i]] {
			t.Fatalf("name %q handed out twice", names[i])
		}
		seen[names[i]] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct names, want %d", len(seen), n)
	}
}

func TestPastAgentNames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	st := open(t, path)
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: "/src/pawly", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	names, err := st.PastAgentNames(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("PastAgentNames() = %v on a fresh project, want none", names)
	}

	// events and agent_reports are package memory's tables; written here by
	// hand rather than pulling that package in as a test dependency.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO events (id, project, agent, at, type) VALUES ('e1', 'pawly', 'agent-03', 0, 'created')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO agent_reports (id, project, agent, created_at) VALUES ('r1', 'pawly', 'agent-05', 0)`); err != nil {
		t.Fatal(err)
	}

	names, err = st.PastAgentNames(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, name := range names {
		got[name] = true
	}
	if !got["agent-03"] || !got["agent-05"] {
		t.Errorf("PastAgentNames() = %v, want agent-03 and agent-05", names)
	}
}
