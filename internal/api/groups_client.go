package api

import (
	"context"
	"net/http"
	"net/url"
)

// FanOut runs one task on several agents at once, as a group.
func (c *Client) FanOut(ctx context.Context, req FanOutRequest) (FanOutResult, error) {
	var out FanOutResult
	return out, c.do(ctx, http.MethodPost, "/v1/groups", req, &out)
}

// Groups lists one project's groups, or every project's for "": every open
// one, and those decided in the last week.
func (c *Client) Groups(ctx context.Context, project string) ([]Group, error) {
	var out []Group
	path := "/v1/groups"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// Group is one group, its agents compared.
func (c *Client) Group(ctx context.Context, id string) (Group, error) {
	var out Group
	return out, c.do(ctx, http.MethodGet, "/v1/groups/"+url.PathEscape(id), nil, &out)
}

// KeepInGroup keeps one of a group's agents and retires the others.
func (c *Client) KeepInGroup(ctx context.Context, id, agent string) (Group, error) {
	var out Group
	return out, c.do(ctx, http.MethodPost, "/v1/groups/"+url.PathEscape(id)+"/keep", KeepRequest{Agent: agent}, &out)
}

// CloseGroup retires every agent of a group, keeping none.
func (c *Client) CloseGroup(ctx context.Context, id string) (Group, error) {
	var out Group
	return out, c.do(ctx, http.MethodPost, "/v1/groups/"+url.PathEscape(id)+"/close", nil, &out)
}

// JudgeGroup asks the project's chat to compare a group's agents and
// recommend one.
func (c *Client) JudgeGroup(ctx context.Context, id string) (Group, error) {
	var out Group
	return out, c.do(ctx, http.MethodPost, "/v1/groups/"+url.PathEscape(id)+"/judge", nil, &out)
}
