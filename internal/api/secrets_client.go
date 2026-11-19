package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The secrets client. One target says which scope is meant, the way the
// command line takes it: "<project>" for a project's secrets, or
// "<project>/<agent>" for one agent's own.

// SecretsPath is the API path of a scope's secrets.
func SecretsPath(target string) (string, error) {
	project, agent, hasAgent := strings.Cut(target, "/")
	switch {
	case project == "":
		return "", fmt.Errorf("invalid target %q: use <project> or <project>/<agent>, like pawly or pawly/agent-01", target)
	case !hasAgent:
		return "/v1/projects/" + url.PathEscape(project) + "/secrets", nil
	case agent == "" || strings.Contains(agent, "/"):
		return "", fmt.Errorf("invalid agent %q: use <project>/<agent>, like pawly/agent-01", target)
	}
	return "/v1/agents/" + url.PathEscape(project) + "/" + url.PathEscape(agent) + "/secrets", nil
}

// Secrets lists what a scope has, without values. A project lists its own
// secrets; an agent lists the ones it holds, its project's included, each
// saying which scope it comes from.
func (c *Client) Secrets(ctx context.Context, target string) ([]Secret, error) {
	path, err := SecretsPath(target)
	if err != nil {
		return nil, err
	}
	var out []Secret
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// SetSecret stores a value under a name, replacing whatever was there, and
// writes it into the agents that get it. The answer carries no value.
func (c *Client) SetSecret(ctx context.Context, target, name, value string) (Secret, error) {
	return c.SetSecretWith(ctx, target, name, SetSecretRequest{Value: value})
}

// SetSecretWith is SetSecret with the rest of the request: whether a project
// secret is the lead's to keep fresh.
func (c *Client) SetSecretWith(ctx context.Context, target, name string, req SetSecretRequest) (Secret, error) {
	path, err := SecretsPath(target)
	if err != nil {
		return Secret{}, err
	}
	var out Secret
	return out, c.do(ctx, http.MethodPut, path+"/"+url.PathEscape(name), req, &out)
}

// SetSecretLeadManaged lets a project's lead fulfil agents' requests for one
// of the project's secrets, or stops it, without giving the value again.
func (c *Client) SetSecretLeadManaged(ctx context.Context, project, name string, managed bool) (Secret, error) {
	var out Secret
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project)+"/secrets/"+url.PathEscape(name),
		SecretSettingsRequest{LeadManaged: managed}, &out)
}

// RemoveSecret forgets a secret and takes it out of the agents that have it.
func (c *Client) RemoveSecret(ctx context.Context, target, name string) error {
	path, err := SecretsPath(target)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, path+"/"+url.PathEscape(name), nil, nil)
}

// ProjectSecretNames lists the names of every secret of the project behind the
// lead socket, in both scopes. Names only, and no route to a value: a project's
// chat can tell an agent "the key is in $FOO" and nothing more
// (D52).
func (c *Client) ProjectSecretNames(ctx context.Context) ([]Secret, error) {
	var out []Secret
	return out, c.do(ctx, http.MethodGet, c.leadPath("/secrets"), nil, &out)
}
