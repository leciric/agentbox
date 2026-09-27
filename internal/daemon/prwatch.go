package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// The pull request watch follows every agent's open pull request until it is
// merged or closed, and speaks up when one breaks: it conflicts with its base,
// its checks fail, or a reviewer asks for changes. It tells the agent whose
// pull request it is what broke and how to fix it — starting its machine if it
// was stopped — and puts a line in front of the project's chat; when the agent
// is gone, the chat is the one told, and asked to decide.
//
// It is meant to be cheap on GitHub, which is the user's own account:
//
//   - One request per repository per look, whatever the number of pull
//     requests: a GraphQL query (github.WatchPullRequests) that costs one point
//     of the account's 5,000 an hour, where REST would cost two or three calls
//     per open pull request.
//   - Nothing at all for a project with no agent pull request to follow and no
//     agent with commits of its own that could have opened one.
//   - An interval that adapts: prWatchFast while checks are running, GitHub
//     is still working out whether it merges, or the head just moved; doubling
//     from prWatchCalm up to prWatchIdle while nothing changes.
//   - A pause until GitHub's reset when the budget runs low or GitHub says to
//     slow down, and a doubling back-off on any other failure.
//
// It speaks only on a transition, never twice for the same state, and what it
// last saw is kept in the database (state.PRWatch) so a restart doesn't
// announce every broken pull request again.

const (
	// prWatchTick is how often the loop looks for a repository that is due.
	prWatchTick = 15 * time.Second
	// prWatchFast is the interval while something is about to change on its
	// own: checks running, mergeability unknown, a head that just moved.
	prWatchFast = 30 * time.Second
	// prWatchCalm is the first interval after something changed; each look
	// that finds nothing new doubles it, up to prWatchIdle.
	prWatchCalm = 2 * time.Minute
	// prWatchIdle is the longest between two looks at a repository, and how
	// often one with nothing to follow is checked for something new.
	prWatchIdle = 15 * time.Minute
	// prWatchLowBudget is how many GraphQL points the account may have left
	// before the watch stands aside until GitHub's reset, leaving the rest to
	// the user and their agents.
	prWatchLowBudget = 250
)

// prWatcher is the watch's schedule and what it last read, per project.
type prWatcher struct {
	// every is how often the loop runs; zero turns the loop off, which is
	// what tests do, driving pollPRWatch themselves.
	every time.Duration
	now   func() time.Time

	pollMu sync.Mutex // one look at a time, so two can't both announce the same break

	mu    sync.Mutex
	repos map[string]*prRepo // by project
}

type prRepo struct {
	next     time.Time
	interval time.Duration
	failures int
	// live is each followed pull request as last read, by number: what the
	// app's views are overlaid with.
	live map[int]github.WatchedPR
}

func newPRWatcher() *prWatcher {
	return &prWatcher{every: prWatchTick, now: time.Now, repos: map[string]*prRepo{}}
}

// repo is a project's schedule, made on first use.
func (w *prWatcher) repo(project string) *prRepo {
	w.mu.Lock()
	defer w.mu.Unlock()
	r, ok := w.repos[project]
	if !ok {
		r = &prRepo{}
		w.repos[project] = r
	}
	return r
}

// poke brings a project's next look forward to now: an agent finished, or the
// pull request cache saw something move, and a pull request may have been
// opened or pushed to.
func (w *prWatcher) poke(project string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.repos[project]; ok {
		r.next = time.Time{}
		r.interval = 0
	}
}

func (w *prWatcher) forget(project string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.repos, project)
}

// overlay puts what the watch last read of a pull request onto the one the
// pull request cache holds: the watch reads an agent's pull requests more
// often, and it alone knows whether one conflicts. Only an open one whose
// head is the one the watch read is touched, so neither side's staler answer
// wins over the other's fresher one.
func (w *prWatcher) overlay(project string, pr *api.PullRequest) {
	if pr == nil || pr.State != "open" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r, ok := w.repos[project]
	if !ok {
		return
	}
	live, ok := r.live[pr.Number]
	if !ok || live.HeadSHA != pr.HeadSHA {
		return
	}
	pr.Watched = true
	pr.Conflict = live.Mergeable == "conflicting"
	pr.Review = live.Review
	if live.Checks != "" {
		pr.Checks = live.Checks
	}
}

// prWatchOn is whether a project's agents' pull requests are watched: the
// project's own say, or the installation's when it leaves it to that.
func (s *Server) prWatchOn(ctx context.Context, p state.Project) bool {
	switch p.PRWatch {
	case state.PRWatchOn:
		return true
	case state.PRWatchOff:
		return false
	}
	on, err := s.store.FlagOn(ctx, state.SettingPRWatch)
	return err != nil || on
}

// setPRWatch is the installation's setting changing. Every lead's brief says
// whether the watch is on, so each is written again, and the projects that
// follow the setting are told, for their Settings.
func (s *Server) setPRWatch(ctx context.Context, on bool) error {
	if err := s.store.SetFlag(ctx, state.SettingPRWatch, on); err != nil {
		return err
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		s.prWatch.poke(p.Name)
		if err := s.manager(nil).ReconfigureLead(ctx, p.Name); err != nil {
			s.logf("reconfiguring the %s chat: %v", p.Name, err)
		}
		if p.PRWatch == "" {
			s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
		}
	}
	return nil
}

// watchPullRequests is the loop Run starts.
func (s *Server) watchPullRequests(ctx context.Context) {
	if s.prWatch.every <= 0 {
		return
	}
	ticker := time.NewTicker(s.prWatch.every)
	defer ticker.Stop()
	for {
		s.pollPRWatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pollPRWatch looks at every project whose look is due.
func (s *Server) pollPRWatch(ctx context.Context) {
	s.prWatch.pollMu.Lock()
	defer s.prWatch.pollMu.Unlock()
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("pull request watch: %v", err)
		return
	}
	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		if !s.prWatchOn(ctx, p) {
			// Turned off: what it saw is forgotten with it, so turning it on
			// again starts from what is true then.
			s.prWatch.forget(p.Name)
			if err := s.store.ForgetPRWatches(ctx, p.Name); err != nil {
				s.logf("pull request watch in %s: %v", p.Name, err)
			}
			continue
		}
		r := s.prWatch.repo(p.Name)
		s.prWatch.mu.Lock()
		due := !s.prWatch.now().Before(r.next)
		s.prWatch.mu.Unlock()
		if due {
			s.watchProject(ctx, p, r)
		}
	}
}

// watchProject is one look at a project's repository, and what follows from
// it.
func (s *Server) watchProject(ctx context.Context, p state.Project, r *prRepo) {
	now := s.prWatch.now()
	schedule := func(next time.Time, interval time.Duration, live map[int]github.WatchedPR) {
		s.prWatch.mu.Lock()
		defer s.prWatch.mu.Unlock()
		r.next, r.interval = next, interval
		if live != nil {
			r.live = live
		}
	}
	client, repo, err := s.githubFor(p)
	if err != nil {
		schedule(now.Add(prWatchIdle), 0, map[int]github.WatchedPR{})
		return
	}
	rows, err := s.store.PRWatches(ctx, p.Name)
	if err != nil {
		s.logf("pull request watch in %s: %v", p.Name, err)
		schedule(now.Add(prWatchIdle), 0, nil)
		return
	}
	agents, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		s.logf("pull request watch in %s: %v", p.Name, err)
		schedule(now.Add(prWatchIdle), 0, nil)
		return
	}
	heads := agentHeads(p.Root, agents)
	if len(rows) == 0 && !slices.ContainsFunc(heads, func(h agentHead) bool { return h.tip != "" }) {
		// No agent has anything of its own that could be in a pull request:
		// nothing to ask GitHub.
		schedule(now.Add(prWatchIdle), 0, map[int]github.WatchedPR{})
		return
	}

	numbers := make([]int, len(rows))
	for i, row := range rows {
		numbers[i] = row.Number
	}
	watch, err := client.WatchPullRequests(ctx, repo, numbers)
	if err != nil {
		var limited *github.RateLimitError
		if errors.As(err, &limited) {
			s.logf("pull request watch in %s: %v", p.Name, err)
			schedule(limited.Until, 0, nil)
			return
		}
		s.prWatch.mu.Lock()
		r.failures++
		wait := min(prWatchIdle, prWatchFast<<min(r.failures, 10))
		s.prWatch.mu.Unlock()
		if r.failures == 1 {
			s.logf("pull request watch in %s: %v", p.Name, err)
		}
		schedule(now.Add(wait), 0, nil)
		return
	}
	s.prWatch.mu.Lock()
	r.failures = 0
	interval := r.interval
	before := r.live
	s.prWatch.mu.Unlock()

	// What GitHub said about each pull request: the page of open ones, and
	// the followed ones asked for by number, which say so when one is merged
	// or closed.
	read := map[int]github.WatchedPR{}
	for _, pr := range watch.Open {
		read[pr.Number] = pr
	}
	for n, pr := range watch.Asked {
		read[n] = pr
	}
	followed := map[int]state.PRWatch{}
	for _, row := range rows {
		followed[row.Number] = row
	}
	// An open pull request carrying an agent's own commits is that agent's,
	// found the same way the fleet finds it (agentHead).
	ids := map[string]string{}
	for _, a := range agents {
		ids[a.Name] = a.ID
	}
	for _, pr := range watch.Open {
		if _, ok := followed[pr.Number]; ok {
			continue
		}
		for _, h := range heads {
			if h.owns(pr.HeadSHA) || (h.tip != "" && pr.HeadSHA == h.tip) {
				followed[pr.Number] = state.PRWatch{Project: p.Name, Number: pr.Number, Agent: h.agent, AgentID: ids[h.agent]}
				break
			}
		}
	}

	live := map[int]github.WatchedPR{}
	fast, changed := false, false
	for _, n := range slices.Sorted(maps.Keys(followed)) {
		prev := followed[n]
		pr, ok := read[n]
		if !ok || pr.State != "open" {
			// Merged or closed — or gone from GitHub altogether: nothing more
			// to watch.
			if err := s.store.ForgetPRWatch(ctx, p.Name, n); err != nil {
				s.logf("pull request watch in %s: %v", p.Name, err)
			}
			changed = true
			continue
		}
		live[n] = pr
		next := prev
		next.HeadSHA, next.Checks, next.Review, next.UpdatedAt = pr.HeadSHA, pr.Checks, pr.Review, now
		// Unknown means GitHub hasn't worked it out yet, not that the
		// conflict went away.
		if pr.Mergeable != "unknown" {
			next.Conflict = pr.Mergeable == "conflicting"
		}
		if problems := prProblems(prev, pr); len(problems) > 0 {
			s.prBroken(ctx, p, repo, next, pr, problems)
		}
		if err := s.store.SavePRWatch(ctx, next); err != nil {
			s.logf("pull request watch in %s: %v", p.Name, err)
		}
		fast = fast || pr.Checks == "pending" || pr.Mergeable == "unknown" || pr.HeadSHA != prev.HeadSHA
		changed = changed || next.Conflict != prev.Conflict || next.Checks != prev.Checks || next.Review != prev.Review || next.HeadSHA != prev.HeadSHA
	}

	switch {
	case len(live) == 0:
		interval = prWatchIdle
	case fast:
		interval = prWatchFast
	case changed || interval == 0:
		interval = prWatchCalm
	default:
		interval = min(prWatchIdle, interval*2)
	}
	next := now.Add(interval)
	if watch.Remaining >= 0 && watch.Remaining < prWatchLowBudget && watch.Reset.After(next) {
		// Little left of the account's budget: leave it to the user and their
		// agents until it fills up again.
		next = watch.Reset
	}
	schedule(next, interval, live)
	if !sameLive(before, live) {
		s.events.publish(api.EventPulls, api.PullsChange{Project: p.Name, GitHub: repo.String(), FetchedAt: now})
	}
}

func sameLive(a, b map[int]github.WatchedPR) bool {
	if len(a) != len(b) {
		return false
	}
	for n, x := range a {
		y, ok := b[n]
		if !ok || x.HeadSHA != y.HeadSHA || x.Checks != y.Checks || x.Mergeable != y.Mergeable || x.Review != y.Review {
			return false
		}
	}
	return true
}

// A prProblem is one way a pull request just broke.
type prProblem string

const (
	prConflict prProblem = "conflict"
	prChecks   prProblem = "checks"
	prReview   prProblem = "review"
)

// prProblems is what broke since the watch last looked: only what turned bad,
// never what was already bad. Checks failing on a new head are a new failure,
// though, since a push is somebody's attempt at fixing them.
func prProblems(prev state.PRWatch, pr github.WatchedPR) []prProblem {
	var out []prProblem
	if pr.Mergeable == "conflicting" && !prev.Conflict {
		out = append(out, prConflict)
	}
	if pr.Checks == "failing" && (prev.Checks != "failing" || prev.HeadSHA != pr.HeadSHA) {
		out = append(out, prChecks)
	}
	if pr.Review == "changes_requested" && prev.Review != "changes_requested" {
		out = append(out, prReview)
	}
	return out
}

// prBroken acts on a pull request that just broke: the agent that owns it is
// told what to do — its machine started first if it was stopped — and the
// project's chat hears about it. When the agent is gone, or couldn't be told,
// the chat is told instead, with a turn to decide what to do.
func (s *Server) prBroken(ctx context.Context, p state.Project, repo github.Repo, row state.PRWatch, pr github.WatchedPR, problems []prProblem) {
	what := prProblemWords(pr, problems)
	s.captureEvent(ctx, p.Name, row.Agent, "pr_broken", map[string]any{
		"number": pr.Number, "url": pr.URL, "problems": problems, "head": pr.HeadSHA,
	}, "")
	a, err := s.store.Agent(ctx, p.Name, row.Agent)
	if err != nil || (row.AgentID != "" && a.ID != "" && a.ID != row.AgentID) {
		s.prLead(ctx, p.Name, fmt.Sprintf(
			"AgentBox's pull request watch: PR #%d (%s) %s. The agent that opened it, %s, is gone: decide who fixes it — an agent of its own, or the user — and say so in a line.",
			pr.Number, pr.URL, what, row.Agent), true)
		return
	}
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title, Kind: api.AgentPRBroken,
		Summary: fmt.Sprintf("PR #%d %s", pr.Number, what), PR: watchedToAPI(pr), At: s.prWatch.now(),
	})
	woke, err := s.prTell(ctx, a, prFixMessage(pr, problems))
	if err != nil {
		s.logf("pull request watch: telling %s: %v", a.Ref(), err)
		s.prLead(ctx, p.Name, fmt.Sprintf(
			"AgentBox's pull request watch: %s's PR #%d (%s) %s, and %s couldn't be told (%v). Get it fixed: tell_agent once it can be reached, or another agent.",
			a.Name, pr.Number, pr.URL, what, a.Name, err), true)
		return
	}
	started := ""
	if woke {
		started = ", starting its machine, which was stopped"
	}
	s.prLead(ctx, p.Name, fmt.Sprintf(
		"AgentBox's pull request watch: %s's PR #%d (%s) %s. AgentBox told %s to fix it%s. Nothing to do unless it can't.",
		a.Name, pr.Number, pr.URL, what, a.Name, started), false)
}

// prProblemWords says what broke, for the chat and the agent's thread.
func prProblemWords(pr github.WatchedPR, problems []prProblem) string {
	var parts []string
	for _, problem := range problems {
		switch problem {
		case prConflict:
			parts = append(parts, fmt.Sprintf("conflicts with %s", pr.BaseBranch))
		case prChecks:
			if names := failingNames(pr); names != "" {
				parts = append(parts, "has failing checks ("+names+")")
			} else {
				parts = append(parts, "has failing checks")
			}
		case prReview:
			parts = append(parts, "has changes requested by a reviewer")
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

func failingNames(pr github.WatchedPR) string {
	names := make([]string, 0, len(pr.Failing))
	for _, c := range pr.Failing {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

// prFixMessage is what the agent is sent: what broke, and how to go about
// fixing each. It asks for the push outright, since an agent's brief tells it
// not to push unless asked.
func prFixMessage(pr github.WatchedPR, problems []prProblem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AgentBox is watching your pull request #%d (%s), and it needs you:\n", pr.Number, pr.URL)
	for _, problem := range problems {
		switch problem {
		case prConflict:
			fmt.Fprintf(&b, "\n- It conflicts with `%s`. `git fetch origin`, rebase your branch on `origin/%s`, resolve the conflicts keeping both sides' intent, and run the tests.", pr.BaseBranch, pr.BaseBranch)
		case prChecks:
			fmt.Fprintf(&b, "\n- Its checks are failing on %s:", shortSHA(pr.HeadSHA))
			for _, c := range pr.Failing {
				if c.URL != "" {
					fmt.Fprintf(&b, " `%s` (%s)", c.Name, c.URL)
				} else {
					fmt.Fprintf(&b, " `%s`", c.Name)
				}
			}
			fmt.Fprintf(&b, ". Read the failing job's log (`gh pr checks %d`, then `gh run view <run-id> --log-failed`), reproduce it here, and fix the cause rather than the test.", pr.Number)
		case prReview:
			fmt.Fprintf(&b, "\n- A reviewer asked for changes. Read the review (`gh pr view %d --comments`) and address it.", pr.Number)
		}
	}
	fmt.Fprintf(&b, "\n\nThen push the fix to the pull request's branch, `%s` (with `--force-with-lease` after a rebase): you're asked to, for this. Say in a line what it was.", pr.HeadBranch)
	return b.String()
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func watchedToAPI(pr github.WatchedPR) *api.PullRequest {
	updated := pr.UpdatedAt
	return &api.PullRequest{
		Number: pr.Number, Title: pr.Title, State: pr.State, Checks: pr.Checks, URL: pr.URL, Draft: pr.Draft,
		UpdatedAt: &updated, BaseBranch: pr.BaseBranch, HeadBranch: pr.HeadBranch, HeadSHA: pr.HeadSHA,
		Conflict: pr.Mergeable == "conflicting", Review: pr.Review, Watched: true,
	}
}

// wakeAndTell sends an agent a message from the watch, starting its machine
// first when it was stopped, or resuming it when paused. It reports whether
// it had to.
func (s *Server) wakeAndTell(ctx context.Context, a state.Agent, text string) (bool, error) {
	if a.Status != state.AgentReady {
		return false, fmt.Errorf("%s is %s", a.Ref(), a.Status)
	}
	m := s.manager(s.cfg.Log)
	inst, err := s.cfg.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return false, err
	}
	woke := false
	switch inst.Status {
	case "Running":
	case "Frozen":
		if err := m.Resume(ctx, a); err != nil {
			return false, err
		}
		woke = true
	default:
		if err := s.serveAgentAPI(a.Instance); err != nil {
			return false, err
		}
		if _, err := m.Start(ctx, a); err != nil {
			return false, err
		}
		woke = true
	}
	if woke {
		if err := m.RecomputeCPUCaps(ctx); err != nil {
			s.logf("pull request watch: %v", err)
		}
		s.refreshAgents(ctx)
	}
	_, err = s.chat.Send(a, text)
	return woke, err
}
