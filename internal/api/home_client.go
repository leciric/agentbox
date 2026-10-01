package api

import (
	"context"
	"net/http"
	"net/url"
)

// The Home chat's own calls, on its lead socket (internal/daemon/homeapi.go).
// The rest of what it does goes through the client's ordinary methods —
// Projects, Fleet, CreateAgent, ProjectMemory — at the same paths as the
// user's.

// HomeAgentChat is one agent's conversation, in any project.
func (c *Client) HomeAgentChat(ctx context.Context, project, agent string) (ChatThread, error) {
	var out ChatThread
	return out, c.do(ctx, http.MethodGet, "/v1/agents/"+url.PathEscape(project)+"/"+url.PathEscape(agent)+"/chat", nil, &out)
}

// HomeTellLead passes a message to a project's own chat, and wakes it.
func (c *Client) HomeTellLead(ctx context.Context, project, text string) error {
	return c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/lead/messages", ChatMessageRequest{Text: text}, nil)
}

// HomeAddProject adds a project from a folder, or clones it from a URL first.
func (c *Client) HomeAddProject(ctx context.Context, req HomeAddProjectRequest) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPost, "/v1/projects", req, &out)
}
