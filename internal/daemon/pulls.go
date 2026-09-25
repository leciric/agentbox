package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// errNoGitHubToken means the project has no GitHub account with a token, so
// pull requests are quietly unavailable rather than an error: most projects
// never share one.
var errNoGitHubToken = errors.New("no GitHub token for this project")

// githubFor resolves the GitHub client and repository for a project, using
// its account, or the machine's default when it doesn't pick one. It returns
// errNoGitHubToken or github.ErrNoRepo when pull requests aren't available:
// a project with no GitHub remote has nothing to read, while one without an
// account has something to read and no way to, which githubErrorFor tells
// apart. github.ErrNoRepo itself comes in the two shapes remoteProblemOf
// tells apart. The repository is returned alongside errNoGitHubToken, so the
// error can name what wasn't read.
func (s *Server) githubFor(p state.Project) (github.Client, github.Repo, error) {
	repo, err := github.RepoOf(p.Root)
	if err != nil {
		return github.Client{}, github.Repo{}, err
	}
	m := s.manager(nil)
	account, err := m.GitHubAccountFor(p, "")
	if err != nil {
		return github.Client{}, repo, errNoGitHubToken
	}
	token, err := m.Creds.GitHubToken(account)
	if err != nil || token == "" {
		return github.Client{}, repo, errNoGitHubToken
	}
	return s.gitHub(token), repo, nil
}

// gitHub is a GitHub client with token, on the API root the daemon was given.
func (s *Server) gitHub(token string) github.Client {
	return github.Client{Token: token, BaseURL: s.cfg.GitHubAPI}
}

const (
	// pullsTTL is how old a repository's answer may be before the next
	// request starts a refresh behind it.
	pullsTTL = 30 * time.Second
	// pullsBranchTTL is how long "this agent branch isn't in the list page"
	// is believed. A pull request opened later arrives in the list itself,
	// which is sorted by when each last moved, so this only holds answers
	// about branches whose pull request is older than the whole page.
	pullsBranchTTL = 10 * time.Minute
	// pullsFetchTimeout bounds one background refresh.
	pullsFetchTimeout = 60 * time.Second
	// pullsBranchLookups is how many per-branch lookups run at once.
	pullsBranchLookups = 4
)

// pullsCache keeps what GitHub said about a repository — its pull requests,
// what the account may merge, and the pull request of an agent branch the
// list page didn't carry — and hands out whatever it holds at once.
//
// Both the Pull requests tab and the fleet read it, and both poll, while
// GitHub takes hundreds of milliseconds to answer: a list a few seconds old,
// on screen now, is worth more than a fresh one behind a spinner
// (D54). A stale entry is
// refreshed in the background, one refresh at a time per repository, and the
// refresh announces itself when something moved.
type pullsCache struct {
	mu        sync.Mutex
	ttl       time.Duration // how old an answer may be before it's refreshed
	branchTTL time.Duration // how long a per-branch answer is believed
	byRepo    map[string]pullsEntry
	fetching  map[string]bool
	// gen counts how often a repository was invalidated, so a refresh that
	// was in flight over a merge doesn't put the pre-merge list back.
	gen map[string]int
	now func() time.Time
}

type pullsEntry struct {
	prs           []api.PullRequest
	canMerge      bool
	canMergeKnown bool
	mergeMethods  []string
	// listErr is why the pull requests couldn't be read; infoErr is why the
	// repository's merge settings couldn't be. They are kept apart because
	// the fleet only shows pull requests and never merges: a token that can
	// list but not read the repository is not the fleet's problem. Each keeps
	// what kind of failure it was as well as its text, so a repository this
	// account can't see is still told from a token GitHub refused after the
	// answer has been through the cache.
	listErr pullsErr
	infoErr pullsErr
	at      time.Time // when GitHub answered; zero means it never has
	// branches holds what a per-branch lookup found for an agent branch the
	// list page didn't carry — including that it found nothing, so an agent
	// whose branch was never pushed isn't one GitHub call per refresh.
	branches map[string]branchPR
}

type branchPR struct {
	pr     *api.PullRequest
	at     time.Time
	failed bool // the lookup failed; believed enough not to retry until the next refresh
}

func newPullsCache() *pullsCache {
	return &pullsCache{
		ttl: pullsTTL, branchTTL: pullsBranchTTL,
		byRepo: map[string]pullsEntry{}, fetching: map[string]bool{}, gen: map[string]int{},
		now: time.Now,
	}
}

// state is what the cache holds for a repository, fresh or not, and whether a
// refresh is running behind it.
func (c *pullsCache) state(key string) (pullsEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byRepo[key], c.fetching[key]
}

// claim marks a refresh as started when one is wanted and none is running,
// and returns the generation to hand back to finish. A refresh is wanted when
// nothing is cached, when what is cached is older than the TTL, or when an
// agent branch has appeared that a working answer says nothing about — a new
// agent shouldn't wait out the TTL to learn it has a pull request.
func (c *pullsCache) claim(key string, branches []string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fetching[key] {
		return 0, false
	}
	e, ok := c.byRepo[key]
	// An entry whose list failed is only retried on the TTL: an unknown
	// branch must never turn a broken GitHub into a call per request.
	if ok && c.now().Sub(e.at) < c.ttl && (e.listErr.failed() || e.knows(branches, c.now(), c.branchTTL)) {
		return 0, false
	}
	c.fetching[key] = true
	return c.gen[key], true
}

// finish stores a refresh's answer, and reports whether anything a client can
// see moved. An answer for a generation the cache has moved past — a merge
// invalidated it while the refresh was in flight — is dropped.
func (c *pullsCache) finish(key string, gen int, e pullsEntry) (pullsEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.fetching, key)
	if c.gen[key] != gen {
		return pullsEntry{}, false
	}
	e.at = c.now()
	before, had := c.byRepo[key]
	c.byRepo[key] = e
	return e, !had || !before.same(e)
}

// invalidate drops a repository's answer, and makes the cache ignore any
// refresh already in flight for it.
func (c *pullsCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen[key]++
	delete(c.byRepo, key)
}

// reset forgets everything, for when the GitHub account behind it changed:
// what the old token could see says nothing about what the new one can.
func (c *pullsCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.byRepo {
		c.gen[key]++
	}
	clear(c.byRepo)
}

// knows reports whether the entry can answer for every one of these branches:
// either the list page carries it, or a per-branch lookup still stands.
func (e pullsEntry) knows(branches []string, now time.Time, branchTTL time.Duration) bool {
	for _, branch := range branches {
		if slices.ContainsFunc(e.prs, func(pr api.PullRequest) bool { return pr.HeadBranch == branch }) {
			continue
		}
		if b, ok := e.branches[branch]; ok && now.Sub(b.at) < branchTTL {
			continue
		}
		return false
	}
	return true
}

// byBranch is each branch's pull request, the list first and the per-branch
// lookups behind it. The list is sorted by when each pull request last moved,
// so a branch with several keeps the one that moved last. The values are
// copies: the entry itself is shared with whoever else is reading the cache.
func (e pullsEntry) byBranch(branches []string) map[string]*api.PullRequest {
	out := make(map[string]*api.PullRequest, len(branches))
	wanted := make(map[string]bool, len(branches))
	for _, branch := range branches {
		wanted[branch] = true
	}
	for _, pr := range e.prs {
		if wanted[pr.HeadBranch] && out[pr.HeadBranch] == nil {
			out[pr.HeadBranch] = &pr
		}
	}
	for branch, b := range e.branches {
		if wanted[branch] && out[branch] == nil && b.pr != nil {
			pr := *b.pr
			out[branch] = &pr
		}
	}
	return out
}

// same reports whether two answers say the same thing, ignoring when they
// were read: an event is only worth sending when something moved.
func (e pullsEntry) same(o pullsEntry) bool {
	if e.listErr != o.listErr || e.infoErr != o.infoErr {
		return false
	}
	if e.canMerge != o.canMerge || e.canMergeKnown != o.canMergeKnown || !slices.Equal(e.mergeMethods, o.mergeMethods) {
		return false
	}
	if !slices.EqualFunc(e.prs, o.prs, samePullRequest) {
		return false
	}
	if len(e.branches) != len(o.branches) {
		return false
	}
	for branch, b := range e.branches {
		other, ok := o.branches[branch]
		if !ok || (b.pr == nil) != (other.pr == nil) {
			return false
		}
		if b.pr != nil && !samePullRequest(*b.pr, *other.pr) {
			return false
		}
	}
	return true
}

func samePullRequest(a, b api.PullRequest) bool {
	if !sameTime(a.UpdatedAt, b.UpdatedAt) {
		return false
	}
	a.UpdatedAt, b.UpdatedAt = nil, nil
	return a == b // every other field is comparable
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// projectPulls is what the cache holds for a project's repository right now,
// with a background refresh started behind it when that has gone stale. It
// never waits for GitHub: an empty cache answers empty, refreshing, and the
// event that follows fills it in.
func (s *Server) projectPulls(p state.Project, branches []string) (github.Repo, pullsEntry, bool, error) {
	client, repo, err := s.githubFor(p)
	if err != nil {
		return repo, pullsEntry{}, false, err
	}
	key := repo.String()
	if gen, ok := s.pulls.claim(key, branches); ok {
		go s.refreshPulls(p.Name, client, repo, gen, branches)
	}
	entry, refreshing := s.pulls.state(key)
	return repo, entry, refreshing, nil
}

// refreshPulls re-reads a repository and stores what it finds, announcing it
// on the event stream when something moved, so the app doesn't have to wait
// for its next poll (D15).
func (s *Server) refreshPulls(project string, client github.Client, repo github.Repo, gen int, branches []string) {
	key := repo.String()
	before, _ := s.pulls.state(key)
	ctx, cancel := context.WithTimeout(s.background(), pullsFetchTimeout)
	defer cancel()
	entry, changed := s.pulls.finish(key, gen, s.fetchPulls(ctx, client, repo, branches, before))
	if !changed {
		return
	}
	s.events.publish(api.EventPulls, api.PullsChange{Project: project, GitHub: key, FetchedAt: entry.at})
}

// background is the context a refresh runs under: the daemon's own, not the
// request's, which ends the moment the request is answered.
func (s *Server) background() context.Context {
	if s.runCtx != nil {
		return s.runCtx
	}
	return context.Background()
}

// fetchPulls reads a repository's pull requests and merge permissions fresh,
// plus the agent branches the list page didn't carry.
func (s *Server) fetchPulls(ctx context.Context, client github.Client, repo github.Repo, branches []string, before pullsEntry) pullsEntry {
	// The list and the repository's settings are two independent calls, so
	// they go out together rather than one after the other.
	var prs []github.PullRequest
	var info github.RepoInfo
	var listErr, infoErr error
	var g errgroup.Group
	g.Go(func() error {
		prs, listErr = client.PullRequests(ctx, repo)
		return nil
	})
	g.Go(func() error {
		info, infoErr = client.Info(ctx, repo)
		return nil
	})
	g.Wait()

	var entry pullsEntry
	if listErr != nil {
		entry.listErr = newPullsErr(listErr)
	} else {
		entry.prs = toAPIPullRequests(prs)
	}
	if infoErr != nil {
		entry.infoErr = newPullsErr(infoErr)
	} else {
		entry.canMerge, entry.canMergeKnown = info.CanPush, info.CanPushKnown
		entry.mergeMethods = mergeMethodsOf(info)
	}

	if entry.listErr.failed() {
		// Don't ask GitHub branch by branch when it just refused the list.
		entry.branches = before.branches
		return entry
	}
	entry.branches = s.branchPulls(ctx, client, repo, branches, entry.prs, before.branches)
	return entry
}

// branchPulls fills in the agent branches the list page didn't carry. The
// list is the most recently updated pull requests, so a branch missing from
// it either has none or has one nobody has touched in a while — either way
// the answer keeps, and one opened later arrives in the list itself.
func (s *Server) branchPulls(ctx context.Context, client github.Client, repo github.Repo, branches []string, prs []api.PullRequest, before map[string]branchPR) map[string]branchPR {
	listed := make(map[string]bool, len(prs))
	for _, pr := range prs {
		listed[pr.HeadBranch] = true
	}
	now := s.pulls.now()
	out := map[string]branchPR{}
	var ask []string
	for _, branch := range branches {
		switch b, ok := before[branch]; {
		case listed[branch]:
		case ok && !b.failed && now.Sub(b.at) < s.pulls.branchTTL:
			out[branch] = b
		default:
			ask = append(ask, branch)
		}
	}
	if len(ask) == 0 {
		return out
	}
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(pullsBranchLookups)
	for _, branch := range ask {
		g.Go(func() error {
			found, err := client.PullRequestFor(ctx, repo, branch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[branch] = branchPR{at: now, failed: true}
				return nil
			}
			b := branchPR{at: now}
			if found != nil {
				pr := toAPIPullRequests([]github.PullRequest{*found})[0]
				b.pr = &pr
			}
			out[branch] = b
			return nil
		})
	}
	g.Wait()
	return out
}

// projectPullRequests lists a project repository's pull requests: every one
// GitHub has, not only the ones with an agent behind them. The ones that do
// are marked with that agent's name, so the tab can jump there — that link is
// the one thing AgentBox uniquely knows. It always says which account it read
// them with, and why it couldn't when it couldn't; a project without a GitHub
// remote has none to read, which is no failure at all, though it still says
// which kind of "no remote" it was.
//
// It answers from the cache and never waits for GitHub: what it sends says
// when it was read and whether a refresh is running behind it.
func (s *Server) projectPullRequests(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	project := r.PathValue("project")
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return err
	}
	account, _ := s.githubAccountFor(p)
	out := api.ProjectPullRequests{Project: project, GitHubAccount: account, PullRequests: []api.PullRequest{}}

	byBranch := map[string]string{}
	var branches []string
	if agents, err := s.store.Agents(ctx, project); err == nil {
		for _, a := range agents {
			if !a.IsLead() && a.Branch != "" {
				byBranch[a.Branch] = a.Name
				branches = append(branches, a.Branch)
			}
		}
	}

	repo, entry, refreshing, err := s.projectPulls(p, branches)
	if err != nil {
		out.GitHubError = s.githubErrorFor(p, repo, err)
		out.NoOrigin, out.NonGitHubRemote = remoteProblemOf(err)
		return writeJSON(w, http.StatusOK, out)
	}
	out.GitHub = repo.String()
	out.Refreshing = refreshing
	if !entry.at.IsZero() {
		at := entry.at
		out.FetchedAt = &at
	}
	// entry.prs is shared with the cache, and with whoever else is reading it,
	// so the agent link goes on a copy rather than mutating it. make+copy,
	// rather than append to a nil slice, keeps an empty result an empty JSON
	// array rather than null.
	if entry.prs != nil {
		out.PullRequests = make([]api.PullRequest, len(entry.prs))
		copy(out.PullRequests, entry.prs)
	}
	out.CanMerge, out.CanMergeKnown, out.MergeMethods = entry.canMerge, entry.canMergeKnown, entry.mergeMethods
	if out.GitHubError = s.githubErrorFrom(p, repo, entry.listErr); out.GitHubError == nil {
		out.GitHubError = s.githubErrorFrom(p, repo, entry.infoErr)
	}
	for i := range out.PullRequests {
		out.PullRequests[i].Agent = byBranch[out.PullRequests[i].HeadBranch]
	}
	return writeJSON(w, http.StatusOK, out)
}

func mergeMethodsOf(info github.RepoInfo) []string {
	var methods []string
	if info.AllowMergeCommit {
		methods = append(methods, string(github.MergeCommit))
	}
	if info.AllowSquashMerge {
		methods = append(methods, string(github.MergeSquash))
	}
	if info.AllowRebaseMerge {
		methods = append(methods, string(github.MergeRebase))
	}
	return methods
}

func toAPIPullRequests(prs []github.PullRequest) []api.PullRequest {
	out := make([]api.PullRequest, len(prs))
	for i, pr := range prs {
		out[i] = api.PullRequest{
			Number: pr.Number, Title: pr.Title, State: pr.State, Checks: pr.Checks, URL: pr.URL,
			Draft: pr.Draft, Additions: pr.Additions, Deletions: pr.Deletions, Comments: pr.Comments,
			UpdatedAt: pr.UpdatedAt, BaseBranch: pr.BaseBranch, HeadBranch: pr.HeadBranch,
		}
	}
	return out
}

// mergePullRequest merges one of a project repository's pull requests. It
// always re-reads the pull request first, so a stale UI can't merge a draft,
// or one that has since been closed or already merged.
func (s *Server) mergePullRequest(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	project := r.PathValue("project")
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return err
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		return fmt.Errorf("invalid pull request number %q", r.PathValue("number"))
	}
	var req api.MergePullRequestRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	method := github.MergeMethod(req.Method)
	if method == "" {
		method = github.MergeCommit
	}
	if !method.Valid() {
		return fmt.Errorf("invalid merge method %q: use merge, squash or rebase", req.Method)
	}

	client, repo, err := s.githubFor(p)
	if err != nil {
		if errors.Is(err, errNoGitHubToken) {
			return errors.New("this project has no GitHub token: add one with agentbox auth github")
		}
		return err
	}

	pr, err := client.PullRequestByNumber(ctx, repo, number)
	if err != nil {
		return err
	}
	switch {
	case pr.Draft:
		return fmt.Errorf("pull request #%d is a draft: mark it ready for review before merging", number)
	case pr.State != "open":
		return fmt.Errorf("pull request #%d is %s: nothing to merge", number, pr.State)
	}

	if err := client.Merge(ctx, repo, number, method); err != nil {
		return err
	}
	pr.State = "merged"
	s.pulls.invalidate(repo.String())
	s.captureEvent(ctx, project, s.agentOfBranch(ctx, project, pr.HeadBranch), "pr_merged", map[string]any{
		"number": pr.Number, "url": pr.URL, "branch": pr.HeadBranch, "method": string(method),
	}, "")

	out := toAPIPullRequests([]github.PullRequest{*pr})[0]
	return writeJSON(w, http.StatusOK, out)
}
