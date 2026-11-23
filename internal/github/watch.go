package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// the watch needs to name the failing ones. When there are more, GitHub's
// roll-up is believed where the ones read can't say (see gqlPR.watched).
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
	Fork       bool // its head branch is in another repository
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
// Low is AgentBox holding a background read back itself, with the budget
// not spent but nearly.
type RateLimitError struct {
	Until   time.Time
	Message string
	Low     bool
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("GitHub's rate limit for this account: %s; AgentBox reads GitHub again at %s", e.Message, e.Until.Local().Format("15:04"))
}

// ErrRateLimited is what a RateLimitError matches with errors.Is.
var ErrRateLimited = errors.New("GitHub's rate limit")

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

const watchFragment = `fragment pr on PullRequest {
  number title url state isDraft createdAt updatedAt headRefName headRefOid baseRefName isCrossRepository mergeable reviewDecision
  author { login }
  reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } } } }
  commits(last: 1) { nodes { commit { ...commit } } }
}
fragment commit on Commit { oid url messageHeadline statusCheckRollup { state
  contexts(first: ` + "%d" + `) { totalCount nodes { __typename
    ... on CheckRun { name status conclusion detailsUrl
      checkSuite { workflowRun { runNumber workflow { id } } } }
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
	IsCrossRepo    bool      `json:"isCrossRepository"`
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
			TotalCount int          `json:"totalCount"`
			Nodes      []gqlContext `json:"nodes"`
		} `json:"contexts"`
	} `json:"statusCheckRollup"`
}

// gqlContext is one check run or commit status on a head: a CheckRun fills
// Name, Status, Conclusion, DetailsURL and CheckSuite, a StatusContext Context,
// State and TargetURL.
type gqlContext struct {
	Type       string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsUrl"`
	CheckSuite *struct {
		// WorkflowRun is null for a check an app reports, as opposed to a
		// GitHub Actions workflow.
		WorkflowRun *struct {
			RunNumber int `json:"runNumber"`
			Workflow  struct {
				ID string `json:"id"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
	Context   string `json:"context"`
	State     string `json:"state"`
	TargetURL string `json:"targetUrl"`
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
		HeadSHA: pr.HeadRefOid, HeadBranch: pr.HeadRefName, BaseBranch: pr.BaseRefName, Fork: pr.IsCrossRepo, UpdatedAt: pr.UpdatedAt,
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
	out.Checks = rollupWord(c.Rollup.State)
	failing, pending := judgeChecks(c.Rollup.Contexts.Nodes)
	out.Failing = failing
	if out.Checks == "failing" {
		// GitHub's roll-up calls a run failed when it was cancelled, or is
		// waiting for someone to approve it, so before the watch tells an agent
		// its checks failed, it looks for one that did.
		switch {
		case len(failing) > 0:
		case pending:
			out.Checks = "pending"
		case c.Rollup.Contexts.TotalCount > len(c.Rollup.Contexts.Nodes):
			// Some checks weren't read, and the failure may be one of them:
			// believe GitHub rather than hide it.
		default:
			out.Checks = "passing"
		}
	}
	return out
}

// rollupWord is a commit's check rollup in the one word the app shows.
func rollupWord(state string) string {
	switch state {
	case "SUCCESS":
		return "passing"
	case "FAILURE", "ERROR":
		return "failing"
	case "PENDING", "EXPECTED":
		return "pending"
	}
	return ""
}

// judgeChecks names the checks on a head that failed, and says whether any is
// still to settle. It differs from GitHub's roll-up on two runs it won't call
// failed:
//   - a cancelled run that a newer run of the same workflow superseded on this
//     head, which is what opening a pull request and then labelling it, or
//     marking it ready, does to the first run: it's neither failed nor pending,
//     the newer run's checks speak for the workflow;
//   - a run awaiting approval (ACTION_REQUIRED), which is pending.
//
// A run cancelled with no newer one still counts as failed, and so does any
// other failure.
func judgeChecks(nodes []gqlContext) (failing []FailingCheck, pending bool) {
	newest := map[string]int{} // newest run number of each workflow
	for _, c := range nodes {
		if id, n, ok := c.workflowRun(); ok && n > newest[id] {
			newest[id] = n
		}
	}
	for _, c := range nodes {
		switch c.Type {
		case "CheckRun":
			switch {
			case c.Status != "COMPLETED":
				pending = true
			case c.Conclusion == "ACTION_REQUIRED":
				pending = true
			case c.Conclusion == "CANCELLED":
				if id, n, ok := c.workflowRun(); ok && n < newest[id] {
					continue // superseded
				}
				failing = append(failing, FailingCheck{Name: c.Name, URL: c.DetailsURL})
			case failedConclusion(c.Conclusion):
				failing = append(failing, FailingCheck{Name: c.Name, URL: c.DetailsURL})
			}
		case "StatusContext":
			switch c.State {
			case "FAILURE", "ERROR":
				failing = append(failing, FailingCheck{Name: c.Context, URL: c.TargetURL})
			case "PENDING", "EXPECTED":
				pending = true
			}
		}
	}
	return failing, pending
}

// workflowRun is the GitHub Actions workflow a check run belongs to and which
// run of it that was; run numbers count up within a workflow.
func (c gqlContext) workflowRun() (workflow string, number int, ok bool) {
	if c.CheckSuite == nil || c.CheckSuite.WorkflowRun == nil || c.CheckSuite.WorkflowRun.Workflow.ID == "" {
		return "", 0, false
	}
	run := c.CheckSuite.WorkflowRun
	return run.Workflow.ID, run.RunNumber, true
}

// failedConclusion is a completed check run's conclusion for a check that
// failed outright. A cancelled run is judged apart, since a newer run may have
// taken its place, and an ACTION_REQUIRED one hasn't failed, it's waiting.
func failedConclusion(c string) bool {
	switch c {
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE":
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
	data, header, err := c.send(ctx, http.MethodPost, "/graphql", body)
	if err != nil {
		return header, err
	}
	return header, json.Unmarshal(data, out)
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
