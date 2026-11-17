package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// todosTool is the lead's hand on the project's todo list
// (internal/daemon/todos.go), the same list the user keeps in the app's
// Todos tab: what they mean to get done, in their order.
func todosTool(ctx context.Context, c *api.Client) mcp.Tool {
	type request struct {
		ID, Text, AI, Model, Effort string
		Position                    int
	}
	read := func(raw json.RawMessage) (request, error) {
		var in request
		if err := decodeArgs(raw, &in); err != nil {
			return in, err
		}
		if in.ID == "" {
			return in, errors.New("name the todo by its id, from list")
		}
		return in, nil
	}
	check := func(done bool) func(json.RawMessage) (string, error) {
		return func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			t, err := c.LeadUpdateTodo(ctx, in.ID, api.TodoRequest{Done: &done})
			if err != nil {
				return "", err
			}
			return describeTodo(t), nil
		}
	}
	optional := func(s string) *string {
		if s = strings.TrimSpace(s); s == "" {
			return nil
		}
		return &s
	}
	return actionTool("todos",
		"The user's todo list for this project (the app's Todos tab), in their order. Add when the user asks you to note "+
			"something; check off what is done; start hands one to a new agent as its task. Not for your own plans.",
		"list: in order, with ids. add: text. edit: id, text. check, uncheck, delete: id. move: id, position (1 is the top). "+
			"start: id, and optionally ai, model, effort as create_agent",
		map[string]any{
			"id":       str("the todo's id"),
			"text":     str("what is to be done, in the user's words"),
			"position": map[string]any{"type": "integer", "description": "move: 1 for the top"},
			"ai":       str("start: as create_agent"),
			"model":    str("start: as create_agent"),
			"effort":   str("start: as create_agent"),
		},
		action{"list", func(json.RawMessage) (string, error) {
			list, err := c.LeadTodos(ctx)
			if err != nil {
				return "", err
			}
			return describeTodos(list), nil
		}},
		action{"add", func(raw json.RawMessage) (string, error) {
			var in request
			if err := decodeArgs(raw, &in); err != nil {
				return "", err
			}
			t, err := c.LeadAddTodo(ctx, in.Text)
			if err != nil {
				return "", err
			}
			return "Added. " + describeTodo(t), nil
		}},
		action{"edit", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			t, err := c.LeadUpdateTodo(ctx, in.ID, api.TodoRequest{Text: &in.Text})
			if err != nil {
				return "", err
			}
			return "Changed. " + describeTodo(t), nil
		}},
		action{"check", check(true)},
		action{"uncheck", check(false)},
		action{"move", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			list, err := c.LeadTodos(ctx)
			if err != nil {
				return "", err
			}
			ids, err := movedTodo(list, in.ID, in.Position)
			if err != nil {
				return "", err
			}
			list, err = c.LeadReorderTodos(ctx, ids)
			if err != nil {
				return "", err
			}
			return "Moved.\n" + describeTodos(list), nil
		}},
		action{"start", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			res, err := c.LeadStartTodo(ctx, in.ID, api.TodoStartRequest{AI: strings.TrimSpace(in.AI), Model: optional(in.Model), Effort: optional(in.Effort)})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("An agent is being created for it (job %s), with its text as the task; you hear when it is up.", res.Job.ID), nil
		}},
		action{"delete", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			if err := c.LeadRemoveTodo(ctx, in.ID); err != nil {
				return "", err
			}
			return "Deleted.", nil
		}},
	)
}

// movedTodo is list's ids with id moved to position (from 1, clamped to the
// list).
func movedTodo(list []api.Todo, id string, position int) ([]string, error) {
	ids := make([]string, 0, len(list))
	for _, t := range list {
		ids = append(ids, t.ID)
	}
	at := slices.Index(ids, id)
	if at < 0 {
		return nil, fmt.Errorf("there is no todo %s: list shows their ids", id)
	}
	ids = slices.Delete(ids, at, at+1)
	to := min(max(position, 1), len(ids)+1) - 1
	return slices.Insert(ids, to, id), nil
}

func describeTodos(list []api.Todo) string {
	if len(list) == 0 {
		return "The project's todo list is empty. add puts one on it."
	}
	var b strings.Builder
	for _, t := range list {
		b.WriteString(describeTodo(t) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// describeTodo is one todo in a line, for the lead.
func describeTodo(t api.Todo) string {
	mark := " "
	if t.Done {
		mark = "x"
	}
	text, _, more := strings.Cut(t.Text, "\n")
	if more {
		text += " …"
	}
	line := fmt.Sprintf("%d. [%s] %s (id %s", t.Position, mark, text, t.ID)
	switch {
	case t.Agent != "":
		line += ", given to " + t.Agent
	case t.Job != "":
		line += ", its agent being made (job " + t.Job + ")"
	}
	if t.CreatedBy == "lead" {
		line += ", added by you"
	}
	return line + ")"
}
