// Package github reads what GitHub knows about a repository's pull
// requests, and can merge one: whether the checks on it are green, whether
// the token can write to the repository, and the actual reason when a merge
// is refused.
//
// It talks to the REST API with the token AgentBox shares, so the host needs no
// `gh` installed. Agents get the same token in their environment and use `gh`
// there; this is only for what AgentBox shows about them.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

// What GitHub's refusals mean, so a caller can tell "you are not this account"
// from "this account isn't in that repository" without reading the message.
// ErrNoRepo, the project having no GitHub repository at all, is in remote.go.
var (
	// ErrBadToken is the token itself being refused (401): revoked, expired,
	// or pasted wrong. Any other account would fail the same way.
	ErrBadToken = errors.New("GitHub refused the token")
	// ErrNoAccess is the token working, but not for this: GitHub answers 404
	// for a private repository the account isn't in — it hides it rather than
	// admitting it exists — and 403 when the account is blocked from it. Both
	// mean the same thing to a user: another account might see it.
	ErrNoAccess = errors.New("this GitHub account can't see it")
	// ErrNoCommit is GitHub not having a commit (422): it was never pushed,
	// which for an agent's newest commit is ordinary rather than a failure.
	ErrNoCommit = errors.New("GitHub has no such commit")
)

// Repo is a GitHub repository, as owner and name. remote.go finds one from a
// git remote.
type Repo struct{ Owner, Name string }

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// PullRequest is what AgentBox shows about a pull request.
type PullRequest struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`  // open, closed or merged
	Checks    string     `json:"checks"` // passing, failing, pending, or "" when there are none
	URL       string     `json:"url"`
	Draft     bool       `json:"draft,omitempty"`
	Additions int        `json:"additions,omitempty"`
	Deletions int        `json:"deletions,omitempty"`
	Comments  int        `json:"comments,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	// BaseBranch and HeadBranch are what it would merge into, and its own
	// branch.
	BaseBranch string `json:"baseBranch,omitempty"`
	HeadBranch string `json:"headBranch,omitempty"`
	// HeadSHA is the commit its branch is at, which is what ties it to an
	// agent: the branch it was pushed to can be called anything.
	HeadSHA string `json:"headSha,omitempty"`
	// Author is who opened it: their GitHub login, and avatar when GitHub
	// sent one. Empty when GitHub reports no user for it, which happens for a
	// pull request whose account was since deleted.
	Author       string `json:"author,omitempty"`
	AuthorAvatar string `json:"authorAvatar,omitempty"`
}

// rawPR is a pull request as GitHub's API shapes it, whichever endpoint sent
// it. The list endpoint leaves Additions, Deletions and Comments at zero;
// only the single pull request endpoint fills them in.
type rawPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	Draft     bool      `json:"draft"`
	Comments  int       `json:"comments"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	UpdatedAt time.Time `json:"updated_at"`
	MergedAt  *string   `json:"merged_at"`
	User      *struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

func (pr rawPR) pullRequest() PullRequest {
	out := PullRequest{
		Number: pr.Number, Title: pr.Title, State: pr.State, URL: pr.HTMLURL,
		Draft: pr.Draft, Comments: pr.Comments, Additions: pr.Additions, Deletions: pr.Deletions,
		UpdatedAt: &pr.UpdatedAt, BaseBranch: pr.Base.Ref, HeadBranch: pr.Head.Ref, HeadSHA: pr.Head.SHA,
	}
	if pr.User != nil {
		out.Author, out.AuthorAvatar = pr.User.Login, pr.User.AvatarURL
	}
	if pr.MergedAt != nil && *pr.MergedAt != "" {
		out.State = "merged"
	}
	return out
}

// Client reads the GitHub API with AgentBox's shared token.
type Client struct {
	Token string
	HTTP  *http.Client
	// BaseURL is the API root; tests point it at a stub.
	BaseURL string
}

const defaultBaseURL = "https://api.github.com"

// apiRoot is where the API lives. AGENTBOX_GITHUB_API points it somewhere else,
// which is how the demo runs point at a stub instead of the real GitHub.
func apiRoot() string {
	if base := strings.TrimRight(os.Getenv("AGENTBOX_GITHUB_API"), "/"); base != "" {
		return base
	}
	return defaultBaseURL
}

// do sends a request and decodes its JSON response. A nil body sends none; a
// nil out discards the response instead of decoding it.
func (c Client) do(ctx context.Context, method, path string, body, out any) error {
	base := c.BaseURL
	if base == "" {
		base = apiRoot()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		return apiError(path, res)
	}
	if out == nil {
		_, err := io.Copy(io.Discard, res.Body)
		return err
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// get is do with no body, for the common read.
func (c Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// apiError turns a non-2xx response into an error that says what went wrong,
// using GitHub's own message when it sent one — a merge conflict, a check
// that hasn't passed, a missing review — not just the status code. The status
// also decides which of ErrBadToken and ErrNoAccess it wraps, so a caller can
// say whose problem it is.
func apiError(path string, res *http.Response) error {
	var body struct {
		Message string `json:"message"`
	}
	data, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(data, &body); err != nil {
		// Not every non-2xx response is JSON (a proxy's error page, for
		// example); fall back to the raw body as the message.
		body.Message = strings.TrimSpace(string(data))
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		if body.Message != "" {
			return fmt.Errorf("%w (%s): %s — check agentbox auth github", ErrBadToken, res.Status, body.Message)
		}
		return fmt.Errorf("%w (%s): check agentbox auth github", ErrBadToken, res.Status)
	case res.StatusCode == http.StatusForbidden && res.Header.Get("X-RateLimit-Remaining") == "0":
		// Rate-limited, not shut out: the account is fine and waiting fixes it,
		// so this must not read as "pick another account".
		return fmt.Errorf("GitHub %s for %s: %s", res.Status, path, body.Message)
	case res.StatusCode == http.StatusUnprocessableEntity && strings.HasPrefix(body.Message, "No commit found"):
		return fmt.Errorf("%w: %s", ErrNoCommit, body.Message)
	case res.StatusCode == http.StatusForbidden, res.StatusCode == http.StatusNotFound:
		if body.Message != "" {
			return fmt.Errorf("%w: %s (%s): %s — check agentbox auth github", ErrNoAccess, path, res.Status, body.Message)
		}
		return fmt.Errorf("%w: %s (%s) — check agentbox auth github", ErrNoAccess, path, res.Status)
	case body.Message != "":
		return fmt.Errorf("GitHub %s for %s: %s", res.Status, path, body.Message)
	default:
		return fmt.Errorf("GitHub %s for %s", res.Status, path)
	}
}

// Login is the account the token belongs to, for the Setup page.
func (c Client) Login(ctx context.Context) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := c.get(ctx, "/user", &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// PullRequestsWithCommit returns the repository's pull requests whose branch
// carries commit, most recently updated first, or none when GitHub has never
// seen the commit. It is how an agent's pull request is found when the list
// page doesn't carry it: by what the agent committed, not by a branch name,
// since an agent's work gets pushed under whatever name suits it.
func (c Client) PullRequestsWithCommit(ctx context.Context, repo Repo, commit string) ([]PullRequest, error) {
	var list []rawPR
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/pulls",
		url.PathEscape(repo.Owner), url.PathEscape(repo.Name), url.PathEscape(commit))
	if err := c.get(ctx, path, &list); err != nil {
		if errors.Is(err, ErrNoCommit) {
			return nil, nil
		}
		return nil, err
	}
	slices.SortStableFunc(list, func(a, b rawPR) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	out := make([]PullRequest, 0, len(list))
	for _, raw := range list {
		item := raw.pullRequest()
		if raw.Head.SHA != "" {
			item.Checks = c.checks(ctx, repo, raw.Head.SHA)
		}
		out = append(out, item)
	}
	return out, nil
}

// pullRequestPageSize bounds how many of a repository's most recently moved
// pull requests PullRequests returns.
const pullRequestPageSize = 30

// PullRequests lists a repository's pull requests, most recently updated
// first, in one call rather than one per branch. Open ones are enriched with
// their checks, comment count and diff size, which the list endpoint doesn't
// carry and GitHub only gives out one pull request at a time; closed and
// merged ones keep whatever the list already had, since nobody is about to
// act on their history.
func (c Client) PullRequests(ctx context.Context, repo Repo) ([]PullRequest, error) {
	var list []rawPR
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=all&sort=updated&direction=desc&per_page=%d",
		url.PathEscape(repo.Owner), url.PathEscape(repo.Name), pullRequestPageSize)
	if err := c.get(ctx, path, &list); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(list))
	for _, raw := range list {
		item := raw.pullRequest()
		if item.State == "open" {
			item.Checks = c.checks(ctx, repo, raw.Head.SHA)
			item.Additions, item.Deletions, item.Comments = c.prStats(ctx, repo, item.Number)
		}
		out = append(out, item)
	}
	return out, nil
}

// PullRequestByNumber reads one pull request fresh, for a check right before
// merging it: whether it's still open, and not a draft.
func (c Client) PullRequestByNumber(ctx context.Context, repo Repo, number int) (*PullRequest, error) {
	var raw rawPR
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number)
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	out := raw.pullRequest()
	if raw.Head.SHA != "" {
		out.Checks = c.checks(ctx, repo, raw.Head.SHA)
	}
	return &out, nil
}

// prStats reads a pull request's diff size and comment count, which only the
// single pull request endpoint carries. It never fails the call: a pull
// request whose stats couldn't be read is still worth listing.
func (c Client) prStats(ctx context.Context, repo Repo, number int) (additions, deletions, comments int) {
	var pr struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Comments  int `json:"comments"`
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number)
	if err := c.get(ctx, path, &pr); err != nil {
		return 0, 0, 0
	}
	return pr.Additions, pr.Deletions, pr.Comments
}

// checks folds a commit's check runs into one word. It never fails the call:
// a pull request with unknown checks is still worth showing.
func (c Client) checks(ctx context.Context, repo Repo, sha string) string {
	var runs struct {
		Total int `json:"total_count"`
		Runs  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), url.PathEscape(sha))
	if err := c.get(ctx, path, &runs); err != nil || runs.Total == 0 {
		return ""
	}
	state := "passing"
	for _, run := range runs.Runs {
		switch {
		case run.Status != "completed":
			return "pending"
		case run.Conclusion == "failure", run.Conclusion == "timed_out", run.Conclusion == "cancelled":
			state = "failing"
		}
	}
	return state
}

// RepoInfo is what a repository allows: whether the token can push to it,
// and so merge into it, when GitHub says so, and which merge methods it
// accepts.
type RepoInfo struct {
	DefaultBranch string
	// CanPush is whether the token can push to this repository. CanPushKnown
	// is false when GitHub didn't say so — some fine-grained and GitHub App
	// tokens omit the field — in which case CanPush isn't meaningful.
	CanPush          bool
	CanPushKnown     bool
	AllowMergeCommit bool
	AllowSquashMerge bool
	AllowRebaseMerge bool
}

// Info reads what a repository allows a merge to do.
func (c Client) Info(ctx context.Context, repo Repo) (RepoInfo, error) {
	var out struct {
		DefaultBranch    string `json:"default_branch"`
		AllowMergeCommit bool   `json:"allow_merge_commit"`
		AllowSquashMerge bool   `json:"allow_squash_merge"`
		AllowRebaseMerge bool   `json:"allow_rebase_merge"`
		Permissions      *struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	path := fmt.Sprintf("/repos/%s/%s", url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	if err := c.get(ctx, path, &out); err != nil {
		return RepoInfo{}, err
	}
	info := RepoInfo{
		DefaultBranch:    out.DefaultBranch,
		AllowMergeCommit: out.AllowMergeCommit,
		AllowSquashMerge: out.AllowSquashMerge,
		AllowRebaseMerge: out.AllowRebaseMerge,
	}
	if out.Permissions != nil {
		info.CanPush, info.CanPushKnown = out.Permissions.Push, true
	}
	return info, nil
}

// MergeMethod is how a pull request's commits join its base branch.
type MergeMethod string

const (
	MergeCommit MergeMethod = "merge"
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
)

// Valid reports whether m is one of the merge methods GitHub accepts.
func (m MergeMethod) Valid() bool {
	switch m {
	case MergeCommit, MergeSquash, MergeRebase:
		return true
	}
	return false
}

// Merge merges a pull request. GitHub explains why when it refuses: a merge
// conflict, a required check that hasn't passed, a missing review, a
// protected branch, or a token without write access.
func (c Client) Merge(ctx context.Context, repo Repo, number int, method MergeMethod) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number)
	body := struct {
		MergeMethod string `json:"merge_method,omitempty"`
	}{MergeMethod: string(method)}
	return c.do(ctx, http.MethodPut, path, body, nil)
}
