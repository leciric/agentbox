package api

import (
	"context"
	"net/http"
	"net/url"
)

// Artifacts lists a project's Hatch artifacts, or only those agent's chat
// made or updated ("" for all).
func (c *Client) Artifacts(ctx context.Context, project, agent string) (Artifacts, error) {
	path := "/v1/projects/" + url.PathEscape(project) + "/artifacts"
	if agent != "" {
		path += "?" + url.Values{"agent": {agent}}.Encode()
	}
	var out Artifacts
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ArtifactPreview brings one of a project's artifacts up to date from Hatch.
func (c *Client) ArtifactPreview(ctx context.Context, project, id string) (ArtifactPreview, error) {
	var out ArtifactPreview
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/artifacts/"+url.PathEscape(id), nil, &out)
}

// ArtifactPage is an artifact's HTML.
func (c *Client) ArtifactPage(ctx context.Context, project, id string) (string, error) {
	var out string
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/artifacts/"+url.PathEscape(id)+"/page", nil, &out)
}
