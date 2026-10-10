package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentbox/internal/memory"
)

func addGlobal(t *testing.T, s *memory.Store, m memory.Memory) memory.Memory {
	t.Helper()
	m.Project = memory.Global
	out, err := s.AddMemory(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every project searches the AgentBox-wide memories beside its own, and no
// project searches another's.
func TestGlobalMemoryIsSearchedFromEveryProject(t *testing.T) {
	s, ctx := open(t), context.Background()
	pref := addGlobal(t, s, memory.Memory{Kind: memory.KindDecision, Title: "Agent preference: one agent at a time",
		Content: "The user wants a single agent running per project.", Importance: 4, Origin: "pawly"})
	if pref.Origin != "pawly" || len(pref.Anchors) != 0 {
		t.Fatalf("written as %+v: want its origin kept and no anchors", pref)
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "Agent count is capped by the VM"}); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"pawly", "agentbox"} {
		got, err := s.Search(ctx, project, "agent preference", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Memories) == 0 || got.Memories[0].ID != pref.ID || got.Memories[0].Project != memory.Global {
			t.Fatalf("%s searched %+v: want the AgentBox-wide preference first", project, got.Memories)
		}
		for _, m := range got.Memories {
			if m.Project != project && m.Project != memory.Global {
				t.Fatalf("%s found %s's memory %q", project, m.Project, m.Title)
			}
		}
	}
	// A project's own listing stays its own.
	own, err := s.Memories(ctx, "agentbox", nil)
	if err != nil || len(own) != 0 {
		t.Fatalf("agentbox lists %v, %v: want nothing of its own", own, err)
	}
}

// A project's chat may resolve an AgentBox-wide memory, and supersedes stay
// within a scope, with an error that says which way to go.
func TestGlobalMemoryResolveAndSupersede(t *testing.T) {
	s, ctx := open(t), context.Background()
	pref := addGlobal(t, s, memory.Memory{Title: "Agent preference: Sonnet for small fixes"})
	own, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "The API listens on 7777"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "Opus for everything", SupersedesID: pref.ID})
	if err == nil || !strings.Contains(err.Error(), "for all projects") {
		t.Fatalf("a project memory replacing a global one: %v", err)
	}
	_, err = s.AddMemory(ctx, memory.Memory{Project: memory.Global, Title: "Port 8080", SupersedesID: own.ID})
	if err == nil || !strings.Contains(err.Error(), "one project's memory") {
		t.Fatalf("a global memory replacing a project one: %v", err)
	}

	next := addGlobal(t, s, memory.Memory{Title: "Agent preference: Haiku for small fixes", SupersedesID: pref.ID})
	list, err := s.Memories(ctx, memory.Global, nil)
	if err != nil || len(list) != 1 || list[0].ID != next.ID {
		t.Fatalf("global memories %v, %v: want only the replacement", list, err)
	}

	closed, err := s.ResolveMemory(ctx, "pawly", next.ID, "the user dropped it")
	if err != nil || !closed.Resolved() || closed.Project != memory.Global {
		t.Fatalf("resolving from pawly: %+v, %v", closed, err)
	}
	if _, err := s.ResolveMemory(ctx, "agentbox", own.ID, "not ours"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("resolving another project's memory: %v, want ErrNotFound", err)
	}
}

// Deleting a memory takes what it superseded with it, so nothing older comes
// back in its place.
func TestDeleteMemoryTakesItsChain(t *testing.T) {
	s, ctx := open(t), context.Background()
	first := addGlobal(t, s, memory.Memory{Title: "Agent preference: one agent at a time"})
	second := addGlobal(t, s, memory.Memory{Title: "Agent preference: two agents at a time", SupersedesID: first.ID})
	other := addGlobal(t, s, memory.Memory{Title: "Commit messages are conventional"})

	if err := s.DeleteMemory(ctx, "pawly", second.ID); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("deleting from the wrong scope: %v", err)
	}
	if err := s.DeleteMemory(ctx, memory.Global, second.ID); err != nil {
		t.Fatal(err)
	}
	list, err := s.Memories(ctx, memory.Global, nil)
	if err != nil || len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("after the delete: %v, %v; want only %q", list, err, other.Title)
	}
	if _, err := s.Memory(ctx, memory.Global, first.ID); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("the superseded memory: %v, want it gone too", err)
	}
	if got, _ := s.Search(ctx, "pawly", "agent preference", 10); len(got.Memories) != 0 {
		t.Fatalf("search still finds %v", got.Memories)
	}
}

// A context carries the AgentBox-wide memories in a section of their own,
// labelled as everybody's, and doesn't repeat them as the project's knowledge.
func TestContextHasTheGlobalSection(t *testing.T) {
	s, ctx := open(t), context.Background()
	fill(t, s, "pawly")
	addGlobal(t, s, memory.Memory{Kind: memory.KindDecision, Title: "Agent preference: one agent at a time", Importance: 4})
	addGlobal(t, s, memory.Memory{Title: "Pagination is reviewed by the user", Importance: 2})

	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "pawly", Query: "pagination", Budget: memory.MaxBudgetTokens})
	if err != nil {
		t.Fatal(err)
	}
	var global, knowledge *memory.ContextSection
	for i, sec := range built.Sections {
		switch sec.Kind {
		case memory.SectionGlobal:
			global = &built.Sections[i]
		case memory.SectionKnowledge:
			knowledge = &built.Sections[i]
		}
	}
	if global == nil || global.Rows != 2 || !strings.Contains(built.Text, "What holds in every project") {
		t.Fatalf("no global section with both memories in:\n%s", built.Text)
	}
	if knowledge == nil || strings.Contains(knowledge.Body, "reviewed by the user") {
		t.Fatalf("the knowledge section repeats a global memory:\n%v", knowledge)
	}
	if strings.Index(built.Text, "every project") > strings.Index(built.Text, "What the project knows") {
		t.Fatalf("the global section comes after the project's knowledge:\n%s", built.Text)
	}
}
