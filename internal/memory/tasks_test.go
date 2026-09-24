package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentbox/internal/memory"
)

// addTask is a task written down in one line, because most of these tests are
// about the edges between tasks rather than about the rows.
func addTask(t *testing.T, s *memory.Store, project, goal string, edit ...func(*memory.Task)) memory.Task {
	t.Helper()
	in := memory.Task{Project: project, Goal: goal}
	for _, e := range edit {
		e(&in)
	}
	out, err := s.AddTask(context.Background(), in)
	if err != nil {
		t.Fatalf("adding task %q: %v", goal, err)
	}
	return out
}

func TestTasks(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	waiting := addTask(t, s, "pawly", "Ship the reminders page")
	doing := addTask(t, s, "pawly", "Build the API", func(in *memory.Task) {
		in.Agent, in.Status, in.Detail = "agent-01", memory.TaskActive, "the endpoints and their tests"
	})
	addTask(t, s, "other", "Somebody else's work")

	if waiting.Status != memory.TaskOpen {
		t.Errorf("a task with no status is %q, want %q", waiting.Status, memory.TaskOpen)
	}
	if !waiting.ClosedAt.IsZero() {
		t.Errorf("an open task closed at %v", waiting.ClosedAt)
	}

	tasks, err := s.Tasks(ctx, "pawly", memory.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want the project's 2", len(tasks))
	}
	// What is being worked on comes before what is merely waiting: the order
	// is the plan's, not the log's.
	if tasks[0].ID != doing.ID {
		t.Errorf("first task is %q, want the active one", tasks[0].Goal)
	}

	mine, err := s.Tasks(ctx, "pawly", memory.TaskFilter{Agent: "agent-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].ID != doing.ID {
		t.Errorf("one agent's tasks = %d rows, want only its own", len(mine))
	}

	if _, err := s.Task(ctx, "other", doing.ID); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("a task read from another project = %v, want ErrNotFound", err)
	}
}

func TestTaskNeedsAGoal(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.AddTask(ctx, memory.Task{Project: "pawly", Goal: "   "}); err == nil {
		t.Error("a task with no goal was written down")
	}
	if _, err := s.AddTask(ctx, memory.Task{Project: "", Goal: "Something"}); err == nil {
		t.Error("a task with no project was written down")
	}
	if _, err := s.AddTask(ctx, memory.Task{Project: "pawly", Goal: "Something", Status: "nearly"}); err == nil {
		t.Error("a task with a status outside the closed set was written down")
	} else if !strings.Contains(err.Error(), memory.TaskAbandoned) {
		t.Errorf("the error doesn't say what the set is: %v", err)
	}
}

func TestUpdateTaskClosesAndReopens(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	task := addTask(t, s, "pawly", "Build the API")
	done, err := s.UpdateTask(ctx, "pawly", task.ID, memory.TaskPatch{Status: ptr(memory.TaskDone)})
	if err != nil {
		t.Fatal(err)
	}
	if done.ClosedAt.IsZero() {
		t.Error("a done task has no closing time")
	}
	closedAt := done.ClosedAt

	// Editing a closed task doesn't move when it closed.
	edited, err := s.UpdateTask(ctx, "pawly", task.ID, memory.TaskPatch{Detail: ptr("merged in #42")})
	if err != nil {
		t.Fatal(err)
	}
	if !edited.ClosedAt.Equal(closedAt) {
		t.Errorf("editing a closed task moved its closing time from %v to %v", closedAt, edited.ClosedAt)
	}

	reopened, err := s.UpdateTask(ctx, "pawly", task.ID, memory.TaskPatch{Status: ptr(memory.TaskOpen)})
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.ClosedAt.IsZero() {
		t.Errorf("a reopened task is still closed at %v", reopened.ClosedAt)
	}

	// OpenOnly is what a plan is read with, and a closed task isn't in it.
	if _, err := s.UpdateTask(ctx, "pawly", task.ID, memory.TaskPatch{Status: ptr(memory.TaskAbandoned)}); err != nil {
		t.Fatal(err)
	}
	open, err := s.Tasks(ctx, "pawly", memory.TaskFilter{OpenOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("an abandoned task is still in the plan: %d rows", len(open))
	}
}

func TestLinkTasksBothWays(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	api := addTask(t, s, "pawly", "Build the API")
	page := addTask(t, s, "pawly", "Build the page")
	if err := s.LinkTasks(ctx, "pawly", page.ID, api.ID); err != nil {
		t.Fatal(err)
	}
	// Linking twice is the same graph, not an error: a model that repeats
	// itself shouldn't be told it broke something.
	if err := s.LinkTasks(ctx, "pawly", page.ID, api.ID); err != nil {
		t.Fatalf("linking the same edge twice: %v", err)
	}

	blocked, err := s.Task(ctx, "pawly", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.DependsOn) != 1 || blocked.DependsOn[0] != api.ID {
		t.Errorf("dependsOn = %v, want [%s]", blocked.DependsOn, api.ID)
	}
	blocker, err := s.Task(ctx, "pawly", api.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocker.Blocks) != 1 || blocker.Blocks[0] != page.ID {
		t.Errorf("blocks = %v, want [%s]", blocker.Blocks, page.ID)
	}

	if err := s.UnlinkTasks(ctx, "pawly", page.ID, api.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := s.Task(ctx, "pawly", page.ID); err != nil || len(again.DependsOn) != 0 {
		t.Errorf("after unlinking, dependsOn = %v (%v)", again.DependsOn, err)
	}
	// Unlinking an edge that was never there is the graph that was asked for.
	if err := s.UnlinkTasks(ctx, "pawly", page.ID, api.ID); err != nil {
		t.Errorf("unlinking nothing: %v", err)
	}
}

// A cycle can never answer "what can be started now", so it is refused on the
// write that would close it — and the refusal has to say which edge, because
// a model told only "that would make a cycle" can only guess what to unlink.
func TestLinkTasksRefusesACycle(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	first := addTask(t, s, "pawly", "First")
	second := addTask(t, s, "pawly", "Second")
	third := addTask(t, s, "pawly", "Third")
	for _, edge := range [][2]string{{second.ID, first.ID}, {third.ID, second.ID}} {
		if err := s.LinkTasks(ctx, "pawly", edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}

	err := s.LinkTasks(ctx, "pawly", first.ID, third.ID)
	if err == nil {
		t.Fatal("a cycle was written down")
	}
	for _, want := range []string{first.ID, third.ID, second.ID, "→"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal doesn't name %s: %v", want, err)
		}
	}

	// Nothing was stored, so the graph still answers.
	blocked, err := s.Task(ctx, "pawly", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.DependsOn) != 0 {
		t.Errorf("the refused edge was stored anyway: %v", blocked.DependsOn)
	}

	if err := s.LinkTasks(ctx, "pawly", first.ID, first.ID); err == nil {
		t.Error("a task was allowed to depend on itself")
	}
}

func TestSubtasksRefuseACycle(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	parent := addTask(t, s, "pawly", "Ship the reminders page")
	child := addTask(t, s, "pawly", "Build the API", func(in *memory.Task) { in.ParentID = parent.ID })
	grandchild := addTask(t, s, "pawly", "Write its tests", func(in *memory.Task) { in.ParentID = child.ID })

	_, err := s.UpdateTask(ctx, "pawly", parent.ID, memory.TaskPatch{ParentID: ptr(grandchild.ID)})
	if err == nil {
		t.Fatal("a task was made a subtask of its own grandchild")
	}
	for _, want := range []string{parent.ID, grandchild.ID, child.ID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal doesn't name %s: %v", want, err)
		}
	}

	// Subtasks are a different graph from blocking edges, and a task may sit
	// in both: being inside something doesn't stop it waiting on something.
	if err := s.LinkTasks(ctx, "pawly", grandchild.ID, child.ID); err != nil {
		t.Errorf("a subtask couldn't be made to wait on its parent: %v", err)
	}
}
