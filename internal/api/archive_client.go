package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ArchivedOptions narrow GET /v1/archived-agents. The zero value is every past
// agent of every project.
type ArchivedOptions struct {
	Project string // by slug or by what it is called
	Query   string // in the name, title, task, branch or pull request
	PR      string // PRStateOpen, PRStateMerged, PRStateClosed or PRStateNone
	// Since and Until are dates (2026-10-11) or RFC 3339 times; Until, a
	// date, includes that day.
	Since, Until string
	Limit        int
}

// ArchivedAgents lists past agents, the most recently archived first.
func (c *Client) ArchivedAgents(ctx context.Context, opt ArchivedOptions) ([]ArchivedAgent, error) {
	var out []ArchivedAgent
	return out, c.do(ctx, http.MethodGet, "/v1/archived-agents?"+opt.values().Encode(), nil, &out)
}

func (opt ArchivedOptions) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"project": opt.Project, "q": opt.Query, "pr": opt.PR, "since": opt.Since, "until": opt.Until} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if opt.Limit > 0 {
		q.Set("limit", strconv.Itoa(opt.Limit))
	}
	return q
}

func archivedPath(ref string) (string, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return "", err
	}
	return "/v1/archived-agents/" + strings.TrimPrefix(path, "/v1/agents/"), nil
}

// ArchivedAgent is one past agent, by ref.
func (c *Client) ArchivedAgent(ctx context.Context, ref string) (ArchivedAgent, error) {
	path, err := archivedPath(ref)
	if err != nil {
		return ArchivedAgent{}, err
	}
	var out ArchivedAgent
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// DeleteArchived deletes past agents for good, with their chats.
func (c *Client) DeleteArchived(ctx context.Context, refs []string) (DeleteArchivedResult, error) {
	var out DeleteArchivedResult
	return out, c.do(ctx, http.MethodPost, "/v1/archived-agents/delete", DeleteArchivedRequest{Refs: refs}, &out)
}

// ContinueArchived starts the job that makes a new agent from a past one's
// branch, seeded with a summary of its chat.
func (c *Client) ContinueArchived(ctx context.Context, ref string, req ContinueArchivedRequest) (Job, error) {
	path, err := archivedPath(ref)
	if err != nil {
		return Job{}, err
	}
	var out Job
	return out, c.do(ctx, http.MethodPost, path+"/continue", req, &out)
}
