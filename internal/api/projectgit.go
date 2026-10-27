package api

import (
	"context"
	"net/http"
	"net/url"
)

// The project git profiles API: where a project's agents clone from in a
// cluster. A project has up to two, private, where its work-in-progress
// branches stay, and public, where a finished change is published, and picks
// which one its agents use. One rule shapes all of it, as with secrets: no
// response here ever carries a credential. A profile names the login; the
// login's value goes in once and is never read back (D52).

// ProjectGit is GET /v1/projects/{project}/git: a project's profiles and which
// of them its agents use.
type ProjectGit struct {
	Profiles []ProjectGitProfile `json:"profiles"`
	// AgentRemote is the profile its agents clone and push with:
	// GitProfilePrivate or GitProfilePublic.
	AgentRemote string `json:"agentRemote"`
}

// ProjectGitProfile is one stored profile, without its secret.
type ProjectGitProfile struct {
	// Profile is "private" or "public".
	Profile string `json:"profile"`
	// URL is what the project stores, which may be an alias this machine
	// resolves.
	URL string `json:"url"`
	// Auth is how the URL signs in: "token", "ssh-key" or "github-account".
	Auth string `json:"auth"`
	// Credential names the stored login: a git credential's name, or the
	// GitHub account for "github-account".
	Credential string `json:"credential"`
	// HasCredential says whether that login is stored now, which is what a pod
	// starting needs; never the login itself.
	HasCredential bool `json:"hasCredential"`
	// ResolvedURL is the URL a pod is given: alias-free, and https for a token
	// login. Absent when it can't be resolved, which ResolveError says.
	ResolvedURL  string `json:"resolvedURL,omitempty"`
	ResolveError string `json:"resolveError,omitempty"`
}

// SetProjectGitProfileRequest is PUT /v1/projects/{project}/git/{profile}.
// Secret is the only place a credential's value appears in this API, and only
// ever on the way in.
type SetProjectGitProfileRequest struct {
	URL string `json:"url"`
	// Auth is "token" (the default when empty), "ssh-key" or
	// "github-account".
	Auth string `json:"auth,omitempty"`
	// Credential names the stored login. Empty is the profile's own name.
	Credential string `json:"credential,omitempty"`
	// Secret, when set, is stored under Credential with the kind Auth implies:
	// the profile and its login are written by one request.
	Secret string `json:"secret,omitempty"`
}

// UseProjectGitRequest is POST /v1/projects/{project}/git/use: which profile
// the project's agents clone with from now on.
type UseProjectGitRequest struct {
	Profile string `json:"profile"`
}

// TestProjectGitRequest is POST /v1/projects/{project}/git/test. Empty Profile
// tests the one the project's agents use.
type TestProjectGitRequest struct {
	Profile string `json:"profile,omitempty"`
}

// ProjectGitTest is what the test found: whether the remote answered with that
// login, and how many refs it has. Detail is a redacted reason when it didn't.
type ProjectGitTest struct {
	// Profile is the one that was tested.
	Profile string `json:"profile"`
	// URL is the resolved URL that was asked, never carrying a secret.
	URL string `json:"url"`
	OK  bool   `json:"ok"`
	// Refs is how many refs the remote listed.
	Refs int `json:"refs"`
	// Detail says what answered, or what it said it didn't like.
	Detail string `json:"detail,omitempty"`
}

// ProjectGit is a project's git profiles and the one its agents use.
func (c *Client) ProjectGit(ctx context.Context, project string) (ProjectGit, error) {
	var out ProjectGit
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/git", nil, &out)
}

// SetProjectGitProfile stores one profile, and the login its secret names when
// one is given, and answers every profile the project now has.
func (c *Client) SetProjectGitProfile(ctx context.Context, project, profile string, req SetProjectGitProfileRequest) (ProjectGit, error) {
	var out ProjectGit
	return out, c.do(ctx, http.MethodPut, "/v1/projects/"+url.PathEscape(project)+"/git/"+url.PathEscape(profile), req, &out)
}

// RemoveProjectGitProfile forgets one profile, and the login no other profile
// of the project names.
func (c *Client) RemoveProjectGitProfile(ctx context.Context, project, profile string) (ProjectGit, error) {
	var out ProjectGit
	return out, c.do(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(project)+"/git/"+url.PathEscape(profile), nil, &out)
}

// UseProjectGitProfile picks which profile the project's agents clone with.
func (c *Client) UseProjectGitProfile(ctx context.Context, project, profile string) (ProjectGit, error) {
	var out ProjectGit
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/git/use", UseProjectGitRequest{Profile: profile}, &out)
}

// TestProjectGitProfile asks the profile's remote for its refs with the stored
// login. An empty profile tests the one the project's agents use.
func (c *Client) TestProjectGitProfile(ctx context.Context, project, profile string) (ProjectGitTest, error) {
	var out ProjectGitTest
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/git/test", TestProjectGitRequest{Profile: profile}, &out)
}
