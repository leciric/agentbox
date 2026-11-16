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

// HomeTellLead asks a project's own chat something, and wakes it: the thread
// its answer comes back to the Home chat in.
func (c *Client) HomeTellLead(ctx context.Context, project, text string) (ProjectThread, error) {
	var out ProjectThread
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/lead/messages", ChatMessageRequest{Text: text}, &out)
}

// HomeAddProject adds a project from a folder, or clones it from a URL first.
func (c *Client) HomeAddProject(ctx context.Context, req HomeAddProjectRequest) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPost, "/v1/projects", req, &out)
}
