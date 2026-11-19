package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// A project's todo list: what the user means to get done, in their order.
// The user keeps it in the app, the lead through its todos tool, both over
// these routes; a todo can be handed to a new agent as its task (start).

// EventTodo says a project's todo list changed. Its data is a TodoChange.
const EventTodo = "todo"

// TodoChange is EventTodo's data.
type TodoChange struct {
	Project string `json:"project"`
}

// Todo is one entry of a project's todo list.
type Todo struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Text    string `json:"text"`
	Done    bool   `json:"done"`
	// Position is its place in the list, from 1.
	Position int `json:"position"`
	// Job is the job creating the agent it was handed to, and Agent that
	// agent once made; both empty for a todo nobody was given.
	Job   string `json:"job,omitempty"`
	Agent string `json:"agent,omitempty"`
	// CreatedBy is "user" or "lead".
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DoneAt    *time.Time `json:"doneAt,omitempty"`
}

// TodoRequest adds a todo (text) or changes one: only what is set.
type TodoRequest struct {
	Text *string `json:"text,omitempty"`
	Done *bool   `json:"done,omitempty"`
}

// TodoOrder is a project's todos' ids in their new order, each once.
type TodoOrder struct {
	IDs []string `json:"ids"`
}

// TodoStartRequest hands a todo to a new agent, whose task is the todo's
// text; AI (its tool), Model and Effort are the project's defaults when left
// out.
type TodoStartRequest struct {
	AI     string  `json:"ai,omitempty"`
	Model  *string `json:"model,omitempty"`
	Effort *string `json:"effort,omitempty"`
}

// TodoStart is what starting a todo began: the todo, now waiting on Job.
type TodoStart struct {
	Todo Todo `json:"todo"`
	Job  Job  `json:"job"`
}

// TodoCounts is how many todos each project has open; a project with none
// isn't in Open.
type TodoCounts struct {
	Open map[string]int `json:"open"`
}

func todosPath(project string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/todos"
}

// Todos is a project's todo list, in its order.
func (c *Client) Todos(ctx context.Context, project string) ([]Todo, error) {
	var out []Todo
	return out, c.do(ctx, http.MethodGet, todosPath(project), nil, &out)
}

// AddTodo puts one at the end of the list.
func (c *Client) AddTodo(ctx context.Context, project, text string) (Todo, error) {
	var out Todo
	return out, c.do(ctx, http.MethodPost, todosPath(project), TodoRequest{Text: &text}, &out)
}

// UpdateTodo changes one's text, or checks it off or back on.
func (c *Client) UpdateTodo(ctx context.Context, project, id string, req TodoRequest) (Todo, error) {
	var out Todo
	return out, c.do(ctx, http.MethodPatch, todosPath(project)+"/"+url.PathEscape(id), req, &out)
}

// RemoveTodo deletes one.
func (c *Client) RemoveTodo(ctx context.Context, project, id string) error {
	return c.do(ctx, http.MethodDelete, todosPath(project)+"/"+url.PathEscape(id), nil, nil)
}

// ReorderTodos puts the list in ids' order, which names each todo once.
func (c *Client) ReorderTodos(ctx context.Context, project string, ids []string) ([]Todo, error) {
	var out []Todo
	return out, c.do(ctx, http.MethodPut, todosPath(project)+"/order", TodoOrder{IDs: ids}, &out)
}

// StartTodo creates an agent with the todo's text as its task.
func (c *Client) StartTodo(ctx context.Context, project, id string, req TodoStartRequest) (TodoStart, error) {
	var out TodoStart
	return out, c.do(ctx, http.MethodPost, todosPath(project)+"/"+url.PathEscape(id)+"/start", req, &out)
}

// OpenTodoCounts is how many todos each project has open, for the sidebar.
func (c *Client) OpenTodoCounts(ctx context.Context) (TodoCounts, error) {
	var out TodoCounts
	return out, c.do(ctx, http.MethodGet, "/v1/todos/counts", nil, &out)
}

// The lead's own: the same routes on its socket, which names the project.

func (c *Client) LeadTodos(ctx context.Context) ([]Todo, error) {
	var out []Todo
	return out, c.do(ctx, http.MethodGet, c.leadPath("/todos"), nil, &out)
}

func (c *Client) LeadAddTodo(ctx context.Context, text string) (Todo, error) {
	var out Todo
	return out, c.do(ctx, http.MethodPost, c.leadPath("/todos"), TodoRequest{Text: &text}, &out)
}

func (c *Client) LeadUpdateTodo(ctx context.Context, id string, req TodoRequest) (Todo, error) {
	var out Todo
	return out, c.do(ctx, http.MethodPatch, c.leadPath("/todos/"+url.PathEscape(id)), req, &out)
}

func (c *Client) LeadRemoveTodo(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, c.leadPath("/todos/"+url.PathEscape(id)), nil, nil)
}

func (c *Client) LeadReorderTodos(ctx context.Context, ids []string) ([]Todo, error) {
	var out []Todo
	return out, c.do(ctx, http.MethodPut, c.leadPath("/todos/order"), TodoOrder{IDs: ids}, &out)
}

func (c *Client) LeadStartTodo(ctx context.Context, id string, req TodoStartRequest) (TodoStart, error) {
	var out TodoStart
	return out, c.do(ctx, http.MethodPost, c.leadPath("/todos/"+url.PathEscape(id)+"/start"), req, &out)
}
