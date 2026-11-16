package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A project's todo list (state/todos.go): the user's in the app, the lead's
// through its todos tool, the same routes for both. A todo can be handed to
// a new agent, whose task is its text.

// maxTodo is how long a todo's text may be: a line or a short paragraph,
// though one handed to an agent is its whole task.
const maxTodo = 4000

func toAPITodo(t state.Todo) api.Todo {
	out := api.Todo{
		ID: t.ID, Project: t.Project, Text: t.Text, Done: t.Done, Position: t.Position, Job: t.Job, Agent: t.Agent,
		CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	if !t.DoneAt.IsZero() {
		at := t.DoneAt
		out.DoneAt = &at
	}
	return out
}

func todoText(p *string) (string, error) {
	text := strings.TrimSpace(*p)
	switch {
	case text == "":
		return "", errors.New("a todo needs its text: what is to be done")
	case len([]rune(text)) > maxTodo:
		return "", fmt.Errorf("a todo is at most %d characters", maxTodo)
	}
	return text, nil
}

// todoChanged tells the app the project's list changed, and records what
// happened to it in the project's history; a reorder isn't history.
func (s *Server) todoChanged(ctx context.Context, t state.Todo, event string) {
	s.events.publish(api.EventTodo, api.TodoChange{Project: t.Project})
	if event != "" {
		s.captureEvent(ctx, t.Project, t.Agent, event, map[string]any{"todo": t.ID, "text": t.Text, "by": t.CreatedBy}, "")
	}
}

func (s *Server) listTodos(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	return s.writeTodos(w, r.Context(), project)
}

func (s *Server) writeTodos(w http.ResponseWriter, ctx context.Context, project string) error {
	list, err := s.store.Todos(ctx, project)
	if err != nil {
		return err
	}
	out := make([]api.Todo, 0, len(list))
	for _, t := range list {
		out = append(out, toAPITodo(t))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addTodo(createdBy string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, err := s.store.Project(r.Context(), r.PathValue("project"))
		if err != nil {
			return err
		}
		var req api.TodoRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		if req.Text == nil {
			req.Text = new(string)
		}
		text, err := todoText(req.Text)
		if err != nil {
			return err
		}
		now := nowMilli()
		t := state.Todo{ID: newID(), Project: p.Name, Text: text, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now}
		if req.Done != nil && *req.Done {
			t.Done, t.DoneAt = true, now
		}
		if t, err = s.store.AddTodo(r.Context(), t); err != nil {
			return err
		}
		s.todoChanged(r.Context(), t, "todo_added")
		return writeJSON(w, http.StatusCreated, toAPITodo(t))
	}
}

func (s *Server) updateTodo(w http.ResponseWriter, r *http.Request) error {
	t, err := s.store.TodoByID(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.TodoRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	now := nowMilli()
	event := ""
	if req.Text != nil {
		if t.Text, err = todoText(req.Text); err != nil {
			return err
		}
	}
	if req.Done != nil && *req.Done != t.Done {
		t.Done = *req.Done
		t.DoneAt, event = now, "todo_done"
		if !t.Done {
			t.DoneAt, event = time.Time{}, "todo_reopened"
		}
	}
	t.UpdatedAt = now
	if err := s.store.UpdateTodo(r.Context(), t); err != nil {
		return err
	}
	s.todoChanged(r.Context(), t, event)
	return writeJSON(w, http.StatusOK, toAPITodo(t))
}

func (s *Server) removeTodo(w http.ResponseWriter, r *http.Request) error {
	t, err := s.store.TodoByID(r.Context(), r.PathValue("project"), r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.store.DeleteTodo(r.Context(), t.Project, t.ID); err != nil {
		return err
	}
	s.todoChanged(r.Context(), t, "")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) reorderTodos(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.TodoOrder
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.store.ReorderTodos(r.Context(), project, req.IDs); err != nil {
		if errors.Is(err, state.ErrTodoOrder) {
			return fmt.Errorf("%w: %w", err, state.ErrExists)
		}
		return err
	}
	s.todoChanged(r.Context(), state.Todo{Project: project}, "")
	return s.writeTodos(w, r.Context(), project)
}

// startTodo hands a todo to a new agent: its text is the agent's task, its
// first line the agent's title. A todo already with an agent that is still
// there, or whose agent is still being made, is refused; one whose agent
// failed to start, or is gone, can be started again.
func (s *Server) startTodo(byLead bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		t, err := s.store.TodoByID(ctx, r.PathValue("project"), r.PathValue("id"))
		if err != nil {
			return err
		}
		if err := s.todoTaken(ctx, t); err != nil {
			return err
		}
		var req api.TodoStartRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		create := api.CreateAgentRequest{Project: t.Project, Title: titleFrom(t.Text), Task: t.Text, AI: req.AI, Model: req.Model, Effort: req.Effort}
		job, err := s.createAgentJob(ctx, create, byLead)
		if err != nil {
			return err
		}
		t.Job, t.Agent, t.UpdatedAt = job.ID, "", nowMilli()
		if err := s.store.UpdateTodo(ctx, t); err != nil {
			return err
		}
		s.todoChanged(ctx, t, "todo_started")
		go s.nameTodoAgent(s.background(), t.Project, job.ID)
		return writeJSON(w, http.StatusAccepted, api.TodoStart{Todo: toAPITodo(t), Job: job})
	}
}

// todoTaken refuses to start a todo again while what it was handed to is
// still at it.
func (s *Server) todoTaken(ctx context.Context, t state.Todo) error {
	if t.Agent != "" {
		if _, err := s.store.Agent(ctx, t.Project, t.Agent); err == nil {
			return fmt.Errorf("todo %s is %s's already: %w", t.ID, t.Agent, state.ErrExists)
		}
		return nil
	}
	if t.Job != "" {
		if j, ok := s.jobs.get(t.Job); ok && !j.snapshot().Done() {
			return fmt.Errorf("todo %s's agent is still being made (job %s): %w", t.ID, t.Job, state.ErrExists)
		}
	}
	return nil
}

// nameTodoAgent waits for a started todo's job and records the agent it made.
func (s *Server) nameTodoAgent(ctx context.Context, project, id string) {
	j, ok := s.jobs.get(id)
	if !ok {
		return
	}
	info, err := j.follow(ctx, 0, func(string) error { return nil })
	if err != nil {
		return
	}
	var made struct {
		Name string `json:"name"`
	}
	if info.Status == api.JobSucceeded && json.Unmarshal(info.Result, &made) == nil && made.Name != "" {
		if ok, err := s.store.SetTodoAgent(ctx, project, id, made.Name); err != nil || !ok {
			return
		}
	}
	// Failed or made: either way the app shows it differently now.
	s.events.publish(api.EventTodo, api.TodoChange{Project: project})
}

func (s *Server) todoCounts(w http.ResponseWriter, r *http.Request) error {
	open, err := s.store.OpenTodoCounts(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.TodoCounts{Open: open})
}

// todoRoutes are the same for the user, under /v1/projects/{project}, and
// for a lead, under /v1/project; only who is recorded as having added one,
// and who started its agent, differ.
func (s *Server) todoRoutes(prefix string, byLead bool, handle func(pattern string, fn func(http.ResponseWriter, *http.Request) error)) {
	createdBy := "user"
	if byLead {
		createdBy = "lead"
	}
	handle("GET "+prefix+"/todos", s.listTodos)
	handle("POST "+prefix+"/todos", s.addTodo(createdBy))
	handle("PUT "+prefix+"/todos/order", s.reorderTodos)
	handle("PATCH "+prefix+"/todos/{id}", s.updateTodo)
	handle("DELETE "+prefix+"/todos/{id}", s.removeTodo)
	handle("POST "+prefix+"/todos/{id}/start", s.startTodo(byLead))
}
