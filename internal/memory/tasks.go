package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Project state, as a graph (D77).
//
// This is the fourth thing the architecture separates, beside events (raw
// history), memories (judgement) and artifacts (references): what the project
// is doing *now*, in a shape something can reason over. Working memory
// answers "what is happening" in prose and is the right size for that; it
// cannot answer "what is blocked on what", because a list of agent names has
// no edges in it.
//
// A task is not a memory and never becomes one. A memory outlives the work —
// "the API listens on 7777" is true after every task about it is closed — and
// a task is the work. So tasks live in their own tables, are not indexed for
// search, and are read by id and by filter rather than by relevance. What the
// graph *did* lands in events like everything else, so consolidation sees it.

// Task statuses. A closed set, checked here rather than by the database so
// the error can say what the set is.
const (
	// TaskOpen is written down and not started.
	TaskOpen = "open"
	// TaskActive is being worked on now.
	TaskActive = "active"
	// TaskBlocked is started and stopped: it is waiting on something, which
	// is usually a task it depends on and sometimes a person.
	TaskBlocked = "blocked"
	// TaskDone is finished.
	TaskDone = "done"
	// TaskAbandoned is closed without being finished: it stopped mattering,
	// or it was tried and didn't work.
	TaskAbandoned = "abandoned"
)

// TaskStatuses are the statuses a task may have, in the order they are worth
// reading: what is in the way, then what is being worked on, then what is
// waiting, then what is over.
var TaskStatuses = []string{TaskBlocked, TaskActive, TaskOpen, TaskDone, TaskAbandoned}

// TaskOpenStatuses are the statuses of a task that is still somebody's
// problem. Everything else is closed.
var TaskOpenStatuses = []string{TaskBlocked, TaskActive, TaskOpen}

// TaskClosed reports whether a status means the task is over, either way.
func TaskClosed(status string) bool { return status == TaskDone || status == TaskAbandoned }

// Task is one piece of work the project has: what it is, who is on it, where
// it sits in the plan and what it is waiting on.
type Task struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Agent is the agent doing it, when one is. A task with no agent is
	// written down and not handed to anybody yet.
	Agent string `json:"agent,omitempty"`
	// ParentID is the task this one is part of, when it is part of one.
	ParentID string `json:"parentId,omitempty"`
	Status   string `json:"status"`
	// Goal is what the task is, in a line: the thing a plan is read by.
	Goal string `json:"goal"`
	// Detail is everything else — the task as it was handed over, and what
	// has been reported about it since.
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// ClosedAt is when it stopped being open, and zero while it still is. It
	// is derived from Status on every write rather than set separately, so
	// the two can never disagree.
	ClosedAt time.Time `json:"closedAt,omitzero,omitempty"`
	// DependsOn are the tasks this one is waiting on, and Blocks the tasks
	// waiting on it. Both are filled on read from task_dependencies; neither
	// is a column.
	DependsOn []string `json:"dependsOn,omitempty"`
	Blocks    []string `json:"blocks,omitempty"`
}

// Open reports whether the task is still somebody's problem.
func (t Task) Open() bool { return !TaskClosed(t.Status) }

// TaskFilter narrows Tasks. A zero filter is the whole of a project's graph,
// what is in the way first.
type TaskFilter struct {
	Agent string // one agent's tasks; empty is every agent's and nobody's
	// Statuses are the statuses to return; empty is every status.
	Statuses []string
	// Parent returns only the subtasks of one task.
	Parent string
	// OpenOnly leaves out what is done or abandoned. It is the common read:
	// a plan is what is left to do.
	OpenOnly bool
	Limit    int // at most this many; 0 is MaxLimit, because a plan is read whole
}

// TaskPatch is a merge patch over a task, the way WorkingMemoryPatch is over
// working memory: a nil field is left alone, a set one replaces. An empty
// ParentID takes the task out of whatever contained it.
type TaskPatch struct {
	Status   *string `json:"status,omitempty"`
	Goal     *string `json:"goal,omitempty"`
	Detail   *string `json:"detail,omitempty"`
	Agent    *string `json:"agent,omitempty"`
	ParentID *string `json:"parentId,omitempty"`
}

// Empty reports whether the patch asks for nothing.
func (p TaskPatch) Empty() bool {
	return p.Status == nil && p.Goal == nil && p.Detail == nil && p.Agent == nil && p.ParentID == nil
}

// maxTaskGoal is how long a task's goal may be. It is a line somebody reads a
// plan by, not the brief: the brief is Detail, bounded like a memory's
// content. Both match what a report already allows for the same two things.
const maxTaskGoal = MaxTitleLen * 4

// AddTask writes a task down. A task needs a goal and nothing else: who is on
// it, what contains it and what it waits on are all things that come later,
// and a plan that can't be written until it is complete is a plan nobody
// writes.
func (s *Store) AddTask(ctx context.Context, t Task) (Task, error) {
	if err := requireProject(t.Project); err != nil {
		return Task{}, err
	}
	if t.Status == "" {
		t.Status = TaskOpen
	}
	if err := oneOf("a task's status", t.Status, TaskStatuses); err != nil {
		return Task{}, err
	}
	goal, err := text("a task's goal", t.Goal, maxTaskGoal)
	if err != nil {
		return Task{}, err
	}
	if goal == "" {
		return Task{}, errors.New("a task needs a goal: what is to be done, in a line somebody would recognise it by")
	}
	detail, err := text("a task's detail", t.Detail, MaxContentLen)
	if err != nil {
		return Task{}, err
	}
	t.Goal, t.Detail = goal, detail
	t.Agent = strings.TrimSpace(t.Agent)
	if t.ID == "" {
		t.ID = newID("task")
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	t.CreatedAt, t.UpdatedAt = stamp(t.CreatedAt), stamp(t.UpdatedAt)
	t.ClosedAt = closedStamp(t.Status, t.UpdatedAt, time.Time{})
	if t.ParentID != "" {
		parent, err := s.Task(ctx, t.Project, t.ParentID)
		if err != nil {
			return Task{}, err
		}
		if parent.ID == t.ID {
			return Task{}, errors.New("a task can't be its own parent")
		}
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, project, agent, parent_task_id, status, goal, detail, created_at, updated_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Project, t.Agent, nullable(t.ParentID), t.Status, t.Goal, t.Detail,
		t.CreatedAt.UnixMilli(), t.UpdatedAt.UnixMilli(), millis(t.ClosedAt))
	if err != nil {
		return Task{}, err
	}
	// The edges of a task nothing points at yet are both empty, so this is
	// the one read that doesn't need the join.
	return t, nil
}

// Task is one task by id, with its edges.
func (s *Store) Task(ctx context.Context, project, id string) (Task, error) {
	if err := requireProject(project); err != nil {
		return Task{}, err
	}
	tasks, err := s.queryTasks(ctx, `WHERE project = ? AND id = ?`, project, id)
	if err != nil {
		return Task{}, err
	}
	if len(tasks) == 0 {
		return Task{}, notFound("task", id)
	}
	if err := s.fillEdges(ctx, project, tasks); err != nil {
		return Task{}, err
	}
	return tasks[0], nil
}

// Tasks are a project's, narrowed by the filter, with their edges: what is in
// the way first, then what is being worked on, then what is waiting, then
// what is over. That order is the plan's rather than the log's — a task graph
// is read to decide what to do next, and "newest first" answers a different
// question.
func (s *Store) Tasks(ctx context.Context, project string, f TaskFilter) ([]Task, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	where := []string{"project = ?"}
	args := []any{project}
	if f.Agent != "" {
		where = append(where, "agent = ?")
		args = append(args, f.Agent)
	}
	for _, status := range f.Statuses {
		if err := oneOf("a task's status", status, TaskStatuses); err != nil {
			return nil, err
		}
	}
	// OpenOnly and a list of statuses are an and, not an or: a caller that
	// asked for both wants what they agree on, and a caller that asked for
	// only closed statuses and OpenOnly asked for nothing.
	statuses := f.Statuses
	switch {
	case f.OpenOnly && len(f.Statuses) > 0:
		if statuses = intersect(f.Statuses, TaskOpenStatuses); len(statuses) == 0 {
			return nil, nil
		}
	case f.OpenOnly:
		statuses = TaskOpenStatuses
	}
	if len(statuses) > 0 {
		where = append(where, "status IN ("+placeholders(len(statuses))+")")
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	if f.Parent != "" {
		where = append(where, "parent_task_id = ?")
		args = append(args, f.Parent)
	}
	limit := MaxLimit
	if f.Limit > 0 && f.Limit < MaxLimit {
		limit = f.Limit
	}
	tasks, err := s.queryTasks(ctx, `WHERE `+strings.Join(where, " AND ")+
		` ORDER BY `+taskOrder+`, created_at DESC, rowid DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	if err := s.fillEdges(ctx, project, tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// taskOrder ranks the statuses the way TaskStatuses lists them. It is written
// out rather than joined against a table of one column: five constants that
// change only when the closed set does.
const taskOrder = `CASE status
	WHEN '` + TaskBlocked + `' THEN 0
	WHEN '` + TaskActive + `' THEN 1
	WHEN '` + TaskOpen + `' THEN 2
	WHEN '` + TaskDone + `' THEN 3
	ELSE 4 END`

// UpdateTask changes what the patch names and leaves the rest. Closing and
// reopening a task are both this: closed_at follows status, because a task
// that says "done" and has no closing time is a task two writers disagreed
// about.
//
// Moving a task under another is restructuring the plan, and is refused when
// it would make a task contain itself — directly or through any number of
// parents — with an error that names the edge that closed the loop.
func (s *Store) UpdateTask(ctx context.Context, project, id string, patch TaskPatch) (Task, error) {
	current, err := s.Task(ctx, project, id)
	if err != nil {
		return Task{}, err
	}
	if patch.Empty() {
		return current, nil
	}
	updated := current
	if patch.Status != nil {
		status := strings.TrimSpace(*patch.Status)
		if err := oneOf("a task's status", status, TaskStatuses); err != nil {
			return Task{}, err
		}
		updated.Status = status
	}
	if patch.Goal != nil {
		goal, err := text("a task's goal", *patch.Goal, maxTaskGoal)
		if err != nil {
			return Task{}, err
		}
		if goal == "" {
			return Task{}, errors.New("a task needs a goal: clear it and nobody can read the plan")
		}
		updated.Goal = goal
	}
	if patch.Detail != nil {
		detail, err := text("a task's detail", *patch.Detail, MaxContentLen)
		if err != nil {
			return Task{}, err
		}
		updated.Detail = detail
	}
	if patch.Agent != nil {
		updated.Agent = strings.TrimSpace(*patch.Agent)
	}
	if patch.ParentID != nil {
		parent := strings.TrimSpace(*patch.ParentID)
		if parent != "" {
			if _, err := s.Task(ctx, project, parent); err != nil {
				return Task{}, err
			}
			if err := s.checkParentCycle(ctx, project, id, parent); err != nil {
				return Task{}, err
			}
		}
		updated.ParentID = parent
	}
	now := stamp(time.Now())
	updated.UpdatedAt = now
	updated.ClosedAt = closedStamp(updated.Status, now, current.ClosedAt)
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET agent = ?, parent_task_id = ?, status = ?, goal = ?, detail = ?, updated_at = ?, closed_at = ?
		 WHERE project = ? AND id = ?`,
		updated.Agent, nullable(updated.ParentID), updated.Status, updated.Goal, updated.Detail,
		updated.UpdatedAt.UnixMilli(), millis(updated.ClosedAt), project, id)
	if err != nil {
		return Task{}, err
	}
	return updated, nil
}

// closedStamp keeps closed_at and status agreeing. A task that closes now
// takes now; one that was already closed keeps the time it closed at, so
// editing a finished task's detail doesn't move when it finished; one that
// reopens loses it.
func closedStamp(status string, now, was time.Time) time.Time {
	if !TaskClosed(status) {
		return time.Time{}
	}
	if !was.IsZero() {
		return was
	}
	return now
}

// LinkTasks says that one task is waiting on another: task can't be finished
// until dependsOn is. It is not the parent edge — a task is blocked on
// however many things it is blocked on, and none of them contains it.
//
// A cycle is refused rather than stored, because a graph with one in it can
// never answer "what can be started now", and the error names the edge that
// closed it: the two tasks, and the path already between them.
func (s *Store) LinkTasks(ctx context.Context, project, task, dependsOn string) error {
	if err := requireProject(project); err != nil {
		return err
	}
	task, dependsOn = strings.TrimSpace(task), strings.TrimSpace(dependsOn)
	if task == "" || dependsOn == "" {
		return errors.New("linking tasks needs both of them: the one that is waiting, and the one it waits on")
	}
	if task == dependsOn {
		return fmt.Errorf("%s can't depend on itself", task)
	}
	if _, err := s.Task(ctx, project, task); err != nil {
		return err
	}
	if _, err := s.Task(ctx, project, dependsOn); err != nil {
		return err
	}
	if err := s.checkDependencyCycle(ctx, project, task, dependsOn); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_dependencies (project, task_id, depends_on_id, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (project, task_id, depends_on_id) DO NOTHING`,
		project, task, dependsOn, time.Now().UnixMilli())
	return err
}

// UnlinkTasks takes a blocking edge back out. Unlinking something that was
// never linked is not an error: the graph afterwards is what was asked for.
func (s *Store) UnlinkTasks(ctx context.Context, project, task, dependsOn string) error {
	if err := requireProject(project); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM task_dependencies WHERE project = ? AND task_id = ? AND depends_on_id = ?`,
		project, strings.TrimSpace(task), strings.TrimSpace(dependsOn))
	return err
}

// TaskEdge is one blocking edge, for a caller that wants the graph rather
// than a task at a time.
type TaskEdge struct {
	TaskID      string `json:"taskId"`
	DependsOnID string `json:"dependsOnId"`
}

// TaskEdges are a project's blocking edges.
func (s *Store) TaskEdges(ctx context.Context, project string) ([]TaskEdge, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, depends_on_id FROM task_dependencies WHERE project = ? ORDER BY created_at, rowid`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TaskEdge
	for rows.Next() {
		var e TaskEdge
		if err := rows.Scan(&e.TaskID, &e.DependsOnID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// checkDependencyCycle refuses an edge that would close a loop of blocking
// edges. It walks forward from the proposed dependency: if what task is about
// to wait on is already waiting, however indirectly, on task, then adding
// this edge means neither can ever start.
//
// The edges are loaded and walked in Go rather than asked for with a
// recursive CTE because the answer has to be the *path*, not a yes: a model
// told "that would make a cycle" and not which edge to unlink can only guess.
func (s *Store) checkDependencyCycle(ctx context.Context, project, task, dependsOn string) error {
	edges, err := s.TaskEdges(ctx, project)
	if err != nil {
		return err
	}
	next := map[string][]string{}
	for _, e := range edges {
		next[e.TaskID] = append(next[e.TaskID], e.DependsOnID)
	}
	if path := pathTo(next, dependsOn, task); path != nil {
		return fmt.Errorf("%s can't depend on %s: that closes a cycle, because %s already depends on %s (%s). "+
			"Unlink one of those edges first, or make one of them a subtask instead",
			task, dependsOn, dependsOn, task, strings.Join(append([]string{dependsOn}, path...), " → "))
	}
	return nil
}

// checkParentCycle refuses a parent that the task already contains. The
// parent edges are one per task, so this walks up rather than searching.
func (s *Store) checkParentCycle(ctx context.Context, project, task, parent string) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_task_id FROM tasks WHERE project = ? AND parent_task_id IS NOT NULL`, project)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	up := map[string]string{}
	for rows.Next() {
		var id, of string
		if err := rows.Scan(&id, &of); err != nil {
			return err
		}
		up[id] = of
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// The edge being asked for, walked from the proposed parent upwards: if
	// it reaches the task, the task would contain itself.
	next := map[string][]string{}
	for id, of := range up {
		next[id] = []string{of}
	}
	if path := pathTo(next, parent, task); path != nil {
		return fmt.Errorf("%s can't be a subtask of %s: that closes a cycle, because %s is already inside %s (%s)",
			task, parent, parent, task, strings.Join(append([]string{parent}, path...), " → "))
	}
	if parent == task {
		return fmt.Errorf("%s can't be its own parent", task)
	}
	return nil
}

// pathTo is the route from one node to another through the edges, or nil when
// there is none. It is a breadth-first walk, so the path it reports is the
// shortest one — the fewest edges somebody has to look at to see the loop.
func pathTo(next map[string][]string, from, to string) []string {
	if from == to {
		return []string{}
	}
	seen := map[string]bool{from: true}
	via := map[string]string{}
	queue := []string{from}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, to2 := range next[at] {
			if seen[to2] {
				continue
			}
			seen[to2], via[to2] = true, at
			if to2 == to {
				var path []string
				for node := to2; node != from; node = via[node] {
					path = append([]string{node}, path...)
				}
				return path
			}
			queue = append(queue, to2)
		}
	}
	return nil
}

// fillEdges reads both directions of the blocking graph for the tasks given,
// in one query rather than one per task: a plan of thirty tasks is read whole,
// every time, by the context builder.
func (s *Store) fillEdges(ctx context.Context, project string, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	edges, err := s.TaskEdges(ctx, project)
	if err != nil {
		return err
	}
	dependsOn, blocks := map[string][]string{}, map[string][]string{}
	for _, e := range edges {
		dependsOn[e.TaskID] = append(dependsOn[e.TaskID], e.DependsOnID)
		blocks[e.DependsOnID] = append(blocks[e.DependsOnID], e.TaskID)
	}
	for i := range tasks {
		tasks[i].DependsOn = dependsOn[tasks[i].ID]
		tasks[i].Blocks = blocks[tasks[i].ID]
	}
	return nil
}

const taskColumns = `id, project, agent, parent_task_id, status, goal, detail, created_at, updated_at, closed_at`

func (s *Store) queryTasks(ctx context.Context, clause string, args ...any) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Task
	for rows.Next() {
		var t Task
		var parent *string
		var created, updated, closed int64
		if err := rows.Scan(&t.ID, &t.Project, &t.Agent, &parent, &t.Status, &t.Goal, &t.Detail,
			&created, &updated, &closed); err != nil {
			return nil, err
		}
		if parent != nil {
			t.ParentID = *parent
		}
		t.CreatedAt, t.UpdatedAt, t.ClosedAt = attime(created), attime(updated), attime(closed)
		out = append(out, t)
	}
	return out, rows.Err()
}

// intersect is the values in both lists, in the order of the first.
func intersect(a, b []string) []string {
	var out []string
	for _, value := range a {
		for _, other := range b {
			if value == other {
				out = append(out, value)
				break
			}
		}
	}
	return out
}
