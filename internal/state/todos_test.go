package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func todoTexts(t *testing.T, s *Store, project string) []string {
	t.Helper()
	list, err := s.Todos(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i, td := range list {
		if td.Position != i+1 {
			t.Fatalf("%q is at %d, want %d", td.Text, td.Position, i+1)
		}
		out = append(out, td.Text)
	}
	return out
}

func TestTodosKeepTheirOrder(t *testing.T) {
	s := openSearchStore(t)
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.AddTodo(ctx, Todo{ID: id, Project: "p", Text: id, CreatedBy: "user", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddTodo(ctx, Todo{ID: "x", Project: "other", Text: "x", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got := todoTexts(t, s, "p"); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("added: %v", got)
	}
	if err := s.ReorderTodos(ctx, "p", []string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got := todoTexts(t, s, "p"); got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("reordered: %v", got)
	}
	// An order missing one, naming one twice or another project's is refused.
	for _, bad := range [][]string{{"c", "a"}, {"c", "a", "a"}, {"c", "a", "x"}} {
		if err := s.ReorderTodos(ctx, "p", bad); !errors.Is(err, ErrTodoOrder) {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	if err := s.DeleteTodo(ctx, "p", "a"); err != nil {
		t.Fatal(err)
	}
	if got := todoTexts(t, s, "p"); len(got) != 2 || got[0] != "c" || got[1] != "b" {
		t.Fatalf("after delete: %v", got)
	}
	// A new one goes at the end, after the gap closed.
	added, err := s.AddTodo(ctx, Todo{ID: "d", Project: "p", Text: "d", CreatedAt: now, UpdatedAt: now})
	if err != nil || added.Position != 3 {
		t.Fatalf("added %+v, %v", added, err)
	}
	if err := s.DeleteTodo(ctx, "p", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
	if _, err := s.TodoByID(ctx, "other", "c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's todo: %v", err)
	}
}

func TestTodosAreCheckedOffAndHandedToAnAgent(t *testing.T) {
	s := openSearchStore(t)
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	if _, err := s.AddTodo(ctx, Todo{ID: "a", Project: "p", Text: "ship it", CreatedBy: "lead", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTodo(ctx, Todo{ID: "b", Project: "p", Text: "then this", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimTodo(ctx, "p", "a", "", "", "claim:1", now); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.SetTodoJob(ctx, "p", "a", "claim:1", "job1"); err != nil {
		t.Fatal(err)
	}
	done := true
	if _, flipped, err := s.PatchTodo(ctx, "p", "a", TodoPatch{Done: &done}, now); err != nil || !flipped {
		t.Fatalf("checked off: %v %v", flipped, err)
	}
	// Checked off again: nothing changes, done_at included.
	if got, flipped, _ := s.PatchTodo(ctx, "p", "a", TodoPatch{Done: &done}, now.Add(time.Hour)); flipped || !got.DoneAt.Equal(now) {
		t.Fatalf("checked off twice: %v %+v", flipped, got)
	}
	if ok, err := s.SetTodoAgent(ctx, "p", "job1", "agent-03"); !ok || err != nil {
		t.Fatalf("set agent: %v %v", ok, err)
	}
	// Only once: a later job's result doesn't overwrite it.
	if ok, _ := s.SetTodoAgent(ctx, "p", "job1", "agent-04"); ok {
		t.Fatal("set twice")
	}
	got, err := s.TodoByID(ctx, "p", "a")
	if err != nil || !got.Done || !got.DoneAt.Equal(now) || got.Agent != "agent-03" || got.CreatedBy != "lead" {
		t.Fatalf("got %+v, %v", got, err)
	}
	counts, err := s.OpenTodoCounts(ctx)
	if err != nil || len(counts) != 1 || counts["p"] != 1 {
		t.Fatalf("counts %v, %v", counts, err)
	}
	if _, _, err := s.PatchTodo(ctx, "other", "a", TodoPatch{Done: &done}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's: %v", err)
	}
}

// An edit made from a copy read before a start leaves the start's job and
// agent alone: it writes only what it changes.
func TestPatchingATodoKeepsWhoItWentTo(t *testing.T) {
	s := openSearchStore(t)
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	if _, err := s.AddTodo(ctx, Todo{ID: "a", Project: "p", Text: "ship it", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimTodo(ctx, "p", "a", "", "", "claim:1", now); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.SetTodoJob(ctx, "p", "a", "claim:1", "job1"); err != nil {
		t.Fatal(err)
	}
	text, done := "ship it on Friday", true
	got, _, err := s.PatchTodo(ctx, "p", "a", TodoPatch{Text: &text, Done: &done}, now)
	if err != nil || got.Text != text || !got.Done || got.Job != "job1" {
		t.Fatalf("patched: %+v, %v", got, err)
	}
	if ok, _ := s.SetTodoAgent(ctx, "p", "job1", "agent-03"); !ok {
		t.Fatal("the job's agent wasn't recorded after the edit")
	}
	reopen := false
	if got, _, _ = s.PatchTodo(ctx, "p", "a", TodoPatch{Done: &reopen}, now); got.Agent != "agent-03" || got.Done || !got.DoneAt.IsZero() {
		t.Fatalf("reopened: %+v", got)
	}
}

// Of two claims made from the same reading, one lands; a failed start gives
// the todo back as it was.
func TestOnlyOneClaimOnATodoLands(t *testing.T) {
	s := openSearchStore(t)
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	if _, err := s.AddTodo(ctx, Todo{ID: "a", Project: "p", Text: "x", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimTodo(ctx, "p", "a", "", "", "claim:1", now)
	second, err2 := s.ClaimTodo(ctx, "p", "a", "", "", "claim:2", now)
	if err != nil || err2 != nil || !first || second {
		t.Fatalf("claims: %v %v (%v %v)", first, second, err, err2)
	}
	// The second's release finds nothing of its own to give back.
	if err := s.ReleaseTodo(ctx, "p", "a", "claim:2", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TodoByID(ctx, "p", "a"); got.Job != "claim:1" {
		t.Fatalf("job = %q, want the first claim's", got.Job)
	}
	if err := s.ReleaseTodo(ctx, "p", "a", "claim:1", "", ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ClaimTodo(ctx, "p", "a", "", "", "claim:3", now); !ok {
		t.Fatal("a released todo couldn't be claimed again")
	}
}
