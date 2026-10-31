package github

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Issue is one of a repository's issues, as AgentBox lists it and hands it to
// an agent.
type Issue struct {
	Number       int
	Title        string
	Body         string
	URL          string
	State        string // open or closed
	Author       string
	AuthorAvatar string
	Labels       []Label
	Comments     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IssueComment is one comment on an issue, oldest first in what Issue returns.
type IssueComment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// rawIssue is an issue as GitHub's REST API shapes it. The issues endpoints
// return pull requests too, marked by PullRequest.
type rawIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	State       string    `json:"state"`
	Comments    int       `json:"comments"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Labels      []Label   `json:"labels"`
	PullRequest *struct{} `json:"pull_request"`
	User        *struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
}

func (r rawIssue) issue() Issue {
	out := Issue{
		Number: r.Number, Title: r.Title, Body: r.Body, URL: r.HTMLURL, State: r.State,
		Labels: r.Labels, Comments: r.Comments, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.User != nil {
		out.Author, out.AuthorAvatar = r.User.Login, r.User.AvatarURL
	}
	return out
}

// issuesPageLimit bounds how many pages of open issues Issues reads: 500
// issues, more than anyone scrolls through to pick one to hand off.
const issuesPageLimit = 5

// Issues lists a repository's open issues, most recently updated first,
// leaving out pull requests, which GitHub's issues endpoint returns as well.
// A label, when given, keeps only the issues carrying it.
func (c Client) Issues(ctx context.Context, repo Repo, label string) ([]Issue, error) {
	out := []Issue{}
	for page := 1; page <= issuesPageLimit; page++ {
		q := url.Values{"state": {"open"}, "sort": {"updated"}, "direction": {"desc"},
			"per_page": {fmt.Sprint(openPageSize)}, "page": {fmt.Sprint(page)}}
		if label != "" {
			q.Set("labels", label)
		}
		var list []rawIssue
		path := fmt.Sprintf("/repos/%s/%s/issues?%s", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), q.Encode())
		if err := c.get(ctx, path, &list); err != nil {
			return nil, err
		}
		for _, r := range list {
			if r.PullRequest == nil {
				out = append(out, r.issue())
			}
		}
		if len(list) < openPageSize {
			break
		}
	}
	return out, nil
}

// issueCommentsLimit is how many of an issue's comments Issue reads: one
// page, which is every comment on all but the longest threads.
const issueCommentsLimit = 100

// Issue reads one issue with its comments, oldest first, for the task an
// agent is given. A pull request's number is refused: it isn't an issue.
func (c Client) Issue(ctx context.Context, repo Repo, number int) (Issue, []IssueComment, error) {
	var raw rawIssue
	base := fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number)
	if err := c.get(ctx, base, &raw); err != nil {
		return Issue{}, nil, err
	}
	if raw.PullRequest != nil {
		return Issue{}, nil, fmt.Errorf("#%d in %s is a pull request, not an issue", number, repo)
	}
	comments := []IssueComment{}
	if raw.Comments > 0 {
		var list []struct {
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"created_at"`
			User      *struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := c.get(ctx, fmt.Sprintf("%s/comments?per_page=%d", base, issueCommentsLimit), &list); err != nil {
			return Issue{}, nil, err
		}
		for _, cm := range list {
			one := IssueComment{Body: cm.Body, CreatedAt: cm.CreatedAt}
			if cm.User != nil {
				one.Author = cm.User.Login
			}
			comments = append(comments, one)
		}
	}
	return raw.issue(), comments, nil
}
