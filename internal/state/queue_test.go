package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

// leaveQueued adds a queued agent the way an earlier release's queue did: its
// row, and one in agent_queue beside it.
func leaveQueued(t *testing.T, s *state.Store, project, name string, position int) {
	t.Helper()
	ctx := context.Background()
	a := state.Agent{Project: project, Name: name, Instance: "ab-" + project + "-" + name, AI: "none",
		Branch: "agentbox/" + name, Status: state.AgentQueued, CreatedAt: time.Now()}
	if err := s.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO agent_queue (project, name, position, request, queued_at) VALUES (?, ?, ?, ?, ?)`,
		project, name, position, `{"n":"`+name+`"}`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
}

func TestLeftoverQueue(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := s.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	leaveQueued(t, s, "p", "a", 2)
	leaveQueued(t, s, "p", "b", 5)
	leaveQueued(t, s, "p", "c", 9)
	q, err := s.LeftoverQueue(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 3 || q[0].Name != "a" || q[0].Position != 1 || q[2].Position != 3 || string(q[1].Request) != `{"n":"b"}` {
		t.Fatalf("queue = %+v", q)
	}

	// Starting one takes it out of line, once.
	a := state.Agent{Project: "p", Name: "b", Instance: "ab-p-b", AI: "none", Branch: "agentbox/b", Worktree: "/w/b"}
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
	if q, _ := s.LeftoverQueue(ctx, "p"); len(q) != 1 || q[0].Name != "c" {
		t.Errorf("queue after starting b and removing a: %+v, want c", q)
	}
}
