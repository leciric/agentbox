package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The pull request watch reads a repository's open pull requests, with their
// checks, whether they conflict with their base and their review decision, in
// one GraphQL request: the REST API would take one request for the list, and
// another two for each open pull request (its checks, and whether it
// conflicts), on every look. A GraphQL query like this one costs a single
// point of the account's 5,000 an hour.

// watchPageSize is how many open pull requests one look reads, most recently
// updated first. Past it, a pull request the watch already follows is still
// read by its number, in the same request.
const watchPageSize = 50

// watchChecksLimit bounds how many of a commit's checks are read, which is all
// the watch needs to name the failing ones.
const watchChecksLimit = 50

// WatchedPR is a pull request as the watch sees it.
type WatchedPR struct {
	Number     int
	Title      string
	URL        string
	State      string // open, closed or merged
	Draft      bool
	HeadSHA    string
	HeadBranch string
	BaseBranch string
	UpdatedAt  time.Time
	// Mergeable is GitHub's word on whether it merges into its base cleanly:
	// mergeable, conflicting, or unknown while GitHub is still working it out,
	// which it does lazily after the base or the head moves.
	Mergeable string
	// Checks folds every check and commit status on its head into one word:
	// passing, failing, pending, or "" when there are none.
	Checks string
	// Failing names the checks that failed, with where to read them.
	Failing []FailingCheck
	// Review is the review decision: approved, changes_requested,
	// review_required, or "" when the repository asks for no review.
	Review string
	// Author is who opened it, CreatedAt when; ReviewRequested is every
	// user a review is asked of now, by login (teams aren't listed).
	Author          string
	CreatedAt       time.Time
	ReviewRequested []string
}

// WatchOptions is what a look reads besides pull requests: for event
// triggers (internal/daemon/eventtriggers.go), in the same request.
type WatchOptions struct {
	// Events reads Watch.Events: who the account is, and the checks on the
	// default branch's newest commit.
	Events bool
	// Labels reads the open issues carrying any of them into Events.Issues.
	Labels []string
}

// RepoEvents is what a look with WatchOptions.Events read.
type RepoEvents struct {
	// Viewer is the login of the account the repository is read with.
	Viewer string
	// DefaultBranch is the repository's default branch, and Head its newest
	// commit: "" when the repository is empty.
	DefaultBranch string
	Head          Commit
	// Issues are the open issues with one of WatchOptions.Labels, most
	// recently updated first.
	Issues []WatchedIssue
}

// Commit is a commit with its checks, folded as WatchedPR.Checks is.
type Commit struct {
	SHA      string
	URL      string
	Headline string
	Checks   string
	Failing  []FailingCheck
}

// WatchedIssue is an open issue as a look sees it.
type WatchedIssue struct {
	Number    int
	Title     string
	URL       string
	Author    string
	Body      string
	Labels    []string
	CreatedAt time.Time
}

// watchIssuesLimit is how many labelled issues one look reads.
const watchIssuesLimit = 30

// FailingCheck is one failing check on a pull request's head.
type FailingCheck struct {
	Name string
	URL  string
}

// Watch is what one look at a repository found.
type Watch struct {
	// Open is every open pull request on the first page, most recently
	// updated first.
	Open []WatchedPR
	// Asked is each pull request asked for by number, whatever state it is
	// in: how the watch learns one it follows was merged or closed. A number
	// GitHub doesn't know is left out.
	Asked map[int]WatchedPR
	// Events is what WatchOptions asked for besides; nil when it didn't.
	Events *RepoEvents
	// Remaining and Reset are what is left of the account's GraphQL budget,
	// and when it fills up again; Remaining is -1 when GitHub didn't say.
	Remaining int
	Reset     time.Time
}

// RateLimitError is GitHub saying to slow down: the hourly budget is spent,
// or a secondary limit tripped. Until is when it's worth asking again.
type RateLimitError struct {
	Until   time.Time
	Message string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("GitHub's rate limit: %s (until %s)", e.Message, e.Until.Format(time.RFC3339))
}

// ErrRateLimited is what a RateLimitError matches with errors.Is.
var ErrRateLimited = errors.New("GitHub's rate limit")

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

const watchFragment = `fragment pr on PullRequest {
  number title url state isDraft createdAt updatedAt headRefName headRefOid baseRefName mergeable reviewDecision
  author { login }
  reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } } } }
  commits(last: 1) { nodes { commit { ...commit } } }
}
fragment commit on Commit { oid url messageHeadline statusCheckRollup { state
  contexts(first: ` + "%d" + `) { nodes { __typename
    ... on CheckRun { name status conclusion detailsUrl }
    ... on StatusContext { context state targetUrl } } } } }`

// watchQuery is the whole look: the page of open pull requests, and each
// number asked for by an alias of its own; with opts.Events, who the account
// is and the default branch's head, and with opts.Labels the issues carrying
// them.
func watchQuery(numbers []int, opts WatchOptions) string {
	var b strings.Builder
	b.WriteString("query($owner: String!, $name: String!) {\n  rateLimit { remaining resetAt }\n")
	if opts.Events {
		b.WriteString("  viewer { login }\n")
	}
	b.WriteString("  repository(owner: $owner, name: $name) {\n")
	fmt.Fprintf(&b, "    open: pullRequests(states: OPEN, first: %d, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { ...pr } }\n", watchPageSize)
	for _, n := range numbers {
		fmt.Fprintf(&b, "    pr%d: pullRequest(number: %d) { ...pr }\n", n, n)
	}
	if opts.Events {
		b.WriteString("    defaultBranchRef { name target { ...commit } }\n")
		if len(opts.Labels) > 0 {
			labels, _ := json.Marshal(opts.Labels)
			fmt.Fprintf(&b, "    issues(states: OPEN, labels: %s, first: %d, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { number title url body createdAt author { login } labels(first: 20) { nodes { name } } } }\n",
				labels, watchIssuesLimit)
		}
	}
	b.WriteString("  }\n}\n")
	fmt.Fprintf(&b, watchFragment, watchChecksLimit)
	return b.String()
}

type gqlLogin struct {
	Login string `json:"login"`
}

type gqlPR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	State          string    `json:"state"`
	IsDraft        bool      `json:"isDraft"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	HeadRefName    string    `json:"headRefName"`
	HeadRefOid     string    `json:"headRefOid"`
	BaseRefName    string    `json:"baseRefName"`
	Mergeable      string    `json:"mergeable"`
	ReviewDecision string    `json:"reviewDecision"`
	Author         *gqlLogin `json:"author"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *struct {
				Type  string `json:"__typename"`
				Login string `json:"login"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Commits struct {
		Nodes []struct {
			Commit gqlCommit `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type gqlCommit struct {
	Oid             string `json:"oid"`
	URL             string `json:"url"`
	MessageHeadline string `json:"messageHeadline"`
	Rollup          *struct {
		State    string `json:"state"`
		Contexts struct {
			Nodes []struct {
				Type       string `json:"__typename"`
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				DetailsURL string `json:"detailsUrl"`
				Context    string `json:"context"`
				State      string `json:"state"`
				TargetURL  string `json:"targetUrl"`
			} `json:"nodes"`
		} `json:"contexts"`
	} `json:"statusCheckRollup"`
}

func (a *gqlLogin) login() string {
	if a == nil {
		return "" // a deleted account: "ghost"
	}
	return a.Login
}

func (pr gqlPR) watched() WatchedPR {
	out := WatchedPR{
		Number: pr.Number, Title: pr.Title, URL: pr.URL, State: strings.ToLower(pr.State), Draft: pr.IsDraft,
		HeadSHA: pr.HeadRefOid, HeadBranch: pr.HeadRefName, BaseBranch: pr.BaseRefName, UpdatedAt: pr.UpdatedAt,
		Mergeable: strings.ToLower(pr.Mergeable), Review: strings.ToLower(pr.ReviewDecision),
		Author: pr.Author.login(), CreatedAt: pr.CreatedAt,
	}
	for _, n := range pr.ReviewRequests.Nodes {
		if r := n.RequestedReviewer; r != nil && r.Type == "User" && r.Login != "" {
			out.ReviewRequested = append(out.ReviewRequested, r.Login)
		}
	}
	if len(pr.Commits.Nodes) > 0 {
		c := pr.Commits.Nodes[0].Commit.commit()
		out.Checks, out.Failing = c.Checks, c.Failing
	}
	return out
}

func (c gqlCommit) commit() Commit {
	out := Commit{SHA: c.Oid, URL: c.URL, Headline: c.MessageHeadline}
	if c.Rollup == nil {
		return out
	}
	switch c.Rollup.State {
	case "SUCCESS":
		out.Checks = "passing"
	case "FAILURE", "ERROR":
		out.Checks = "failing"
	case "PENDING", "EXPECTED":
		out.Checks = "pending"
	}
	for _, x := range c.Rollup.Contexts.Nodes {
		switch {
		case x.Type == "CheckRun" && x.Status == "COMPLETED" && failedConclusion(x.Conclusion):
			out.Failing = append(out.Failing, FailingCheck{Name: x.Name, URL: x.DetailsURL})
		case x.Type == "StatusContext" && (x.State == "FAILURE" || x.State == "ERROR"):
			out.Failing = append(out.Failing, FailingCheck{Name: x.Context, URL: x.TargetURL})
		}
	}
	return out
}

func failedConclusion(c string) bool {
	switch c {
	case "FAILURE", "TIMED_OUT", "CANCELLED", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return true
	}
	return false
}

// WatchPullRequests reads a repository's open pull requests, and the ones
// numbered, in one request.
func (c Client) WatchPullRequests(ctx context.Context, repo Repo, numbers []int) (Watch, error) {
	return c.WatchRepository(ctx, repo, numbers, WatchOptions{})
}

// WatchRepository is WatchPullRequests reading what opts asks for besides,
// still in one request.
func (c Client) WatchRepository(ctx context.Context, repo Repo, numbers []int, opts WatchOptions) (Watch, error) {
	numbers = append([]int(nil), numbers...)
	sort.Ints(numbers)
	body := map[string]any{
		"query":     watchQuery(numbers, opts),
		"variables": map[string]string{"owner": repo.Owner, "name": repo.Name},
	}
	var out struct {
		Data struct {
			RateLimit *struct {
				Remaining int       `json:"remaining"`
				ResetAt   time.Time `json:"resetAt"`
			} `json:"rateLimit"`
			Viewer     *gqlLogin                  `json:"viewer"`
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	header, err := c.graphQL(ctx, body, &out)
	if err != nil {
		return Watch{}, err
	}
	w := Watch{Asked: map[int]WatchedPR{}, Remaining: -1}
	if rl := out.Data.RateLimit; rl != nil {
		w.Remaining, w.Reset = rl.Remaining, rl.ResetAt
	} else if n, err := strconv.Atoi(header.Get("X-RateLimit-Remaining")); err == nil {
		w.Remaining, w.Reset = n, resetOf(header)
	}
	for _, e := range out.Errors {
		if e.Type == "RATE_LIMITED" {
			until := w.Reset
			if until.IsZero() {
				until = time.Now().Add(time.Hour)
			}
			return Watch{}, &RateLimitError{Until: until, Message: e.Message}
		}
	}
	if out.Data.Repository == nil {
		// A repository this account can't see comes back as null, with an
		// error saying so, rather than a 404.
		msg := "the repository wasn't returned"
		if len(out.Errors) > 0 {
			msg = out.Errors[0].Message
		}
		return Watch{}, fmt.Errorf("%w: %s", ErrNoAccess, msg)
	}
	if raw, ok := out.Data.Repository["open"]; ok {
		var page struct {
			Nodes []gqlPR `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return Watch{}, err
		}
		for _, pr := range page.Nodes {
			w.Open = append(w.Open, pr.watched())
		}
	}
	for _, n := range numbers {
		raw, ok := out.Data.Repository["pr"+strconv.Itoa(n)]
		if !ok || string(raw) == "null" {
			continue // GitHub doesn't know it: deleted, or another repository's
		}
		var pr gqlPR
		if err := json.Unmarshal(raw, &pr); err != nil {
			return Watch{}, err
		}
		w.Asked[n] = pr.watched()
	}
	if opts.Events {
		events, err := readEvents(out.Data.Viewer.login(), out.Data.Repository)
		if err != nil {
			return Watch{}, err
		}
		w.Events = &events
	}
	return w, nil
}

func readEvents(viewer string, repo map[string]json.RawMessage) (RepoEvents, error) {
	out := RepoEvents{Viewer: viewer}
	if raw, ok := repo["defaultBranchRef"]; ok && string(raw) != "null" {
		var ref struct {
			Name   string     `json:"name"`
			Target *gqlCommit `json:"target"`
		}
		if err := json.Unmarshal(raw, &ref); err != nil {
			return out, err
		}
		out.DefaultBranch = ref.Name
		if ref.Target != nil {
			out.Head = ref.Target.commit()
		}
	}
	if raw, ok := repo["issues"]; ok && string(raw) != "null" {
		var page struct {
			Nodes []struct {
				Number    int       `json:"number"`
				Title     string    `json:"title"`
				URL       string    `json:"url"`
				Body      string    `json:"body"`
				CreatedAt time.Time `json:"createdAt"`
				Author    *gqlLogin `json:"author"`
				Labels    struct {
					Nodes []struct {
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"labels"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return out, err
		}
		for _, n := range page.Nodes {
			is := WatchedIssue{Number: n.Number, Title: n.Title, URL: n.URL, Author: n.Author.login(), Body: n.Body, CreatedAt: n.CreatedAt}
			for _, l := range n.Labels.Nodes {
				is.Labels = append(is.Labels, l.Name)
			}
			out.Issues = append(out.Issues, is)
		}
	}
	return out, nil
}

// graphQL posts a query and decodes its answer, turning GitHub's rate limits
// into a RateLimitError that says when to come back.
func (c Client) graphQL(ctx context.Context, body, out any) (http.Header, error) {
	base := c.BaseURL
	if base == "" {
		base = apiRoot()
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/graphql", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if limited := rateLimitOf(res); limited != nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return res.Header, limited
	}
	if res.StatusCode >= 300 {
		return res.Header, apiError("/graphql", res)
	}
	return res.Header, json.NewDecoder(res.Body).Decode(out)
}

// rateLimitOf reads a response for GitHub telling the client to slow down:
// a 403 or 429 with no requests left or a Retry-After, which is how both the
// hourly budget and the secondary limits say it.
func rateLimitOf(res *http.Response) *RateLimitError {
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	if s := res.Header.Get("Retry-After"); s != "" {
		if secs, err := strconv.Atoi(s); err == nil {
			return &RateLimitError{Until: time.Now().Add(time.Duration(secs) * time.Second), Message: res.Status}
		}
	}
	if res.Header.Get("X-RateLimit-Remaining") == "0" {
		until := resetOf(res.Header)
		if until.IsZero() {
			until = time.Now().Add(time.Hour)
		}
		return &RateLimitError{Until: until, Message: res.Status}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		// No word on when: GitHub's own advice is to wait at least a minute.
		return &RateLimitError{Until: time.Now().Add(time.Minute), Message: res.Status}
	}
	return nil
}

func resetOf(h http.Header) time.Time {
	secs, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil || secs == 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}
