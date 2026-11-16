package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
	"agentbox/internal/worktreefs"
)

// The Work page is every piece of work agents did, live agents' and past
// ones', newest first: each agent's review item (review.go), its report, its
// pull request, its changes and how many screenshots and recordings it kept.
// Live agents come from the same reading as "Ready to review" (liveWork), so
// what waits for review here is what the review lists; past agents from
// archived_agents, whose changes are measured on their kept branch.

// workPageSize is a page's size when ?limit= doesn't say.
const workPageSize = 50

// allWork is GET /v1/work: every project's work, or ?project='s.
func (s *Server) allWork(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var projects []state.Project
	if name := r.URL.Query().Get("project"); name != "" {
		p, err := s.store.Project(ctx, name)
		if err != nil {
			return err
		}
		projects = []state.Project{p}
	} else {
		var err error
		if projects, err = s.store.Projects(ctx); err != nil {
			return err
		}
	}
	return s.writeWork(w, r, projects)
}

// projectWork is GET /v1/projects/{project}/work.
func (s *Server) projectWork(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	return s.writeWork(w, r, []state.Project{p})
}

// writeWork answers both routes: ?filter= (WorkFilter*) and ?q= narrow it,
// ?limit= and ?cursor= page it.
func (s *Server) writeWork(w http.ResponseWriter, r *http.Request, projects []state.Project) error {
	ctx := r.Context()
	v := r.URL.Query()
	filter := v.Get("filter")
	switch filter {
	case "", api.WorkFilterReview, api.WorkFilterOpen, api.WorkFilterMerged, api.WorkFilterClosed, api.WorkFilterNone:
	default:
		return fmt.Errorf("filter must be review, open, merged, closed or none, not %q", filter)
	}
	limit := workPageSize
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return errors.New("limit must be a positive number")
		}
		limit = min(n, reviewPageMax)
	}
	// Live agents are few and read as "Ready to review" reads them; past ones
	// are every agent the install ever had, so the archive counts them and
	// answers one page (state.ArchivedPage) instead of being read whole.
	q := strings.ToLower(strings.TrimSpace(v.Get("q")))
	filters := []string{api.WorkFilterReview, api.WorkFilterOpen, api.WorkFilterMerged, api.WorkFilterClosed, api.WorkFilterNone}
	page := api.WorkPage{Items: []api.WorkItem{}, Counts: map[string]int{}}
	roots := map[string]string{}
	var kept []workEntry
	for _, p := range projects {
		roots[p.Name] = p.Root
		live, err := s.liveWorkEntries(ctx, p)
		if err != nil {
			return err
		}
		for _, e := range live {
			if !e.matches(q) {
				continue
			}
			page.Counts[""]++
			for _, f := range filters {
				if e.in(f) {
					page.Counts[f]++
				}
			}
			if filter == "" || e.in(filter) {
				kept = append(kept, e)
			}
		}
	}
	slices.SortFunc(kept, workEntry.compare)
	var after *workKey
	if cursor := v.Get("cursor"); cursor != "" {
		k, ok := parseWorkCursor(cursor)
		if !ok {
			return fmt.Errorf("cursor %q isn't one this list gave", cursor)
		}
		after = &k
		kept = slices.DeleteFunc(kept, func(e workEntry) bool { return e.key().compare(k) <= 0 })
	}
	// One project's, or every project's: a removed project's past agents went
	// with it.
	archived := state.ArchivedPageQuery{Query: q}
	if len(projects) == 1 {
		archived.Project = projects[0].Name
	}
	if len(projects) > 0 && filter != api.WorkFilterReview {
		// No past agent waits for review. One more than the page says whether
		// another follows.
		archived.PRState, archived.Limit = filter, limit+1
		if after != nil {
			sec := time.Unix(after.at/int64(time.Second), 0)
			archived.After = &state.ArchivedCursor{At: sec, Through: after.at%int64(time.Second) != 0, Project: after.project, Name: after.agent}
		}
	}
	var past []state.ArchivedAgent
	var counts state.ArchivedCounts
	if len(projects) > 0 {
		var err error
		if past, counts, err = s.store.ArchivedPage(ctx, archived); err != nil {
			return err
		}
	}
	// A filter that keeps nothing has no count, as before.
	for _, f := range append([]string{""}, filters[1:]...) {
		if n := counts.Of(f); n > 0 {
			page.Counts[f] += n
		}
	}
	page.Total = page.Counts[filter]
	if after == nil {
		// Behind the first page only: the ones after it are the same list.
		go s.refreshPastPRs(archived.Project)
	}
	for i := range past {
		kept = append(kept, pastWorkEntry(&past[i], roots[past[i].Project]))
	}
	// Each half is in order already; the ref breaks a tie, so a cursor always
	// names one place in the list.
	slices.SortStableFunc(kept, workEntry.compare)
	if len(kept) > limit {
		page.Next = kept[limit-1].key().cursor()
		kept = kept[:limit]
	}
	page.Items = s.filledWork(ctx, kept, v.Get("brief") != "")
	return writeJSON(w, http.StatusOK, page)
}

// workKey is where a piece of work sorts: newest first, then by project and
// agent, as the archive orders past agents (state.ArchivedPage).
type workKey struct {
	at             int64 // WorkItem.At, in nanoseconds
	project, agent string
}

func (k workKey) compare(o workKey) int {
	return cmp.Or(cmp.Compare(o.at, k.at), cmp.Compare(k.project, o.project), cmp.Compare(k.agent, o.agent))
}

// cursor is the key as ?cursor= takes it.
func (k workKey) cursor() string { return fmt.Sprintf("%d:%s/%s", k.at, k.project, k.agent) }

func parseWorkCursor(cursor string) (workKey, bool) {
	at, ref, ok := strings.Cut(cursor, ":")
	n, err := strconv.ParseInt(at, 10, 64)
	project, agent, found := strings.Cut(ref, "/")
	return workKey{n, project, agent}, ok && found && err == nil
}

// workEntry is one agent's work before its page is filled in: past agents
// keep what they were archived as, for their changes and report.
type workEntry struct {
	item api.WorkItem
	past *state.ArchivedAgent
	root string // the project's repository
}

func (e workEntry) key() workKey {
	return workKey{e.item.At.UnixNano(), e.item.Item.Project, e.item.Item.Agent}
}

func (e workEntry) compare(o workEntry) int { return e.key().compare(o.key()) }

// matches is whether the search q (lower case) is in the work's title, task,
// branch or pull request title, or is its pull request's number.
func (e workEntry) matches(q string) bool {
	if q == "" {
		return true
	}
	i := e.item.Item
	if n, ok := state.PRNumberQuery(q); ok && i.PR != nil && i.PR.Number == n {
		return true
	}
	fields := []string{i.Title, i.Agent, i.Branch, e.item.Task}
	if i.PR != nil {
		fields = append(fields, i.PR.Title)
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// in is whether the work is in filter f.
func (e workEntry) in(f string) bool {
	pr := e.item.Item.PR
	switch f {
	case api.WorkFilterReview:
		return e.item.Status == api.WorkReview
	case api.WorkFilterNone:
		return pr == nil || pr.Number == 0
	default:
		return pr != nil && pr.Number > 0 && pr.State == f
	}
}

// liveWorkEntries is the work of p's live agents, not filled in (filledWork).
func (s *Server) liveWorkEntries(ctx context.Context, p state.Project) ([]workEntry, error) {
	if s.prReconciling(p.Name) {
		return nil, nil
	}
	agents, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	var out []workEntry
	for _, e := range s.liveWork(ctx, p, agents, true, false) {
		item := api.WorkItem{Item: e.item, Status: api.WorkWorking, At: e.agent.CreatedAt}
		switch {
		case !e.finished:
		case e.item.Verdict != "":
			item.Status = api.WorkReviewed
		case e.ended:
			item.Status = api.WorkDone
		default:
			item.Status = api.WorkReview
		}
		if e.finished {
			item.At = e.item.FinishedAt
		}
		if task, err := s.store.FirstMessage(ctx, p.Name, e.agent.Name); err == nil {
			item.Task = task
		}
		out = append(out, workEntry{item: item, root: p.Root})
	}
	return out, nil
}

// pastWorkEntry is a past agent's work, not filled in (filledWork).
func pastWorkEntry(a *state.ArchivedAgent, root string) workEntry {
	item := api.WorkItem{
		Item: api.ReviewItem{Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title, Branch: a.Branch, BaseRef: a.BaseRef, AI: a.AI, FinishedAt: a.ArchivedAt},
		Task: a.Task, Past: true, Status: api.WorkDone, At: a.ArchivedAt,
	}
	if a.PR.Number > 0 {
		item.Item.PR = &api.PullRequest{Number: a.PR.Number, Title: a.PR.Title, URL: a.PR.URL, State: a.PR.State}
	}
	return workEntry{item: item, past: a, root: root}
}

// refreshPastPRs re-reads from GitHub the pull requests of past agents that
// were open, of project or of every one, behind the page that listed them:
// waiting for it held the page half a second for each project, every few
// minutes. When one moved, the app is told as for any pull request's, and
// reads the page again.
func (s *Server) refreshPastPRs(project string) {
	ctx := s.background()
	past, err := s.store.ArchivedAgents(ctx, state.ArchivedQuery{Project: project, PRState: state.PROpen})
	if err != nil || len(past) == 0 {
		return
	}
	before := make([]state.ArchivedPR, len(past))
	for i, a := range past {
		before[i] = a.PR
	}
	moved := map[string]bool{}
	for i, a := range s.refreshArchivedPRs(ctx, past) {
		if a.PR != before[i] && !moved[a.Project] {
			moved[a.Project] = true
			s.events.publish(api.EventPulls, api.PullsChange{Project: a.Project})
		}
	}
}

// filledWork is a page's entries as its items: each with its report, its
// media, a past agent's verdict, merge mark and what its kept branch changed.
// What the store holds is one query for the page, not one for each row, and
// only the changes cost a git process each, a few at once, against each
// repository's branches read once. With brief an item is what a list's row
// shows: no task, and of its report only the status; whoever opens one reads
// the rest (GET /v1/projects/{project}/memory/reports).
func (s *Server) filledWork(ctx context.Context, entries []workEntry, brief bool) []api.WorkItem {
	keys := make([]state.AgentKey, len(entries))
	refs := make([][2]string, len(entries))
	var pastKeys []state.AgentKey
	tips := map[string]map[string]string{}
	marks := map[string]map[int]state.MergeWhenGreen{}
	for i, e := range entries {
		keys[i] = state.AgentKey{Project: e.item.Item.Project, Agent: e.item.Item.Agent}
		refs[i] = [2]string{keys[i].Project, keys[i].Agent}
		if e.past == nil {
			continue
		}
		pastKeys = append(pastKeys, keys[i])
		if _, ok := tips[e.root]; !ok {
			tips[e.root] = s.tipsOf(e.root)
		}
		if _, ok := marks[keys[i].Project]; !ok && e.past.PR.Number > 0 {
			marks[keys[i].Project] = s.mergeMarks(ctx, keys[i].Project)
		}
	}
	reports, _ := s.memory().LatestReports(ctx, refs)
	media, _ := s.store.MediaKindCounts(ctx, keys)
	reviews, _ := s.store.AgentReviewsOf(ctx, pastKeys)
	items := make([]api.WorkItem, len(entries))
	var g errgroup.Group
	g.SetLimit(changesConcurrency)
	for i, e := range entries {
		item := e.item
		if e.past != nil {
			markItem(&item.Item, marks[keys[i].Project])
			if r, ok := reviews[keys[i]]; ok {
				at := r.At
				item.Item.Verdict, item.Item.ReviewedAt, item.Status = r.Verdict, &at, api.WorkReviewed
			}
		}
		if r, ok := reports[refs[i]]; ok {
			if brief {
				item.Report = &api.AgentReport{Status: r.Status}
			} else {
				full := apiReport(r)
				item.Report = &full
				if item.Item.Summary == "" {
					item.Item.Summary = r.Summary
				}
			}
		}
		if brief {
			item.Task = ""
		}
		item.Screenshots, item.Recordings = media[keys[i]]["screenshot"], media[keys[i]]["recording"]
		items[i] = item
		if e.past != nil && item.Item.Changes == nil {
			g.Go(func() error {
				items[i].Item.Changes = s.pastChangesOf(e.root, *e.past, tips[e.root])
				return nil
			})
		}
	}
	_ = g.Wait()
	return items
}

// tipsRead is a repository's branches (worktreefs.BranchTips) and when they
// were read.
type tipsRead struct {
	tips map[string]string
	at   time.Time
}

// tipsTTL is how long a repository's branches are taken as read. Reading
// them is a git process for each project on a page, which was most of what a
// page of every project's work still waited for; a past agent's kept branch
// moves about never, and its changes are only this much behind when it does.
const tipsTTL = 5 * time.Second

// tipsOf is the branches of the repository at root, nil when they can't be
// read, which isn't kept.
func (s *Server) tipsOf(root string) map[string]string {
	s.mu.Lock()
	read, ok := s.branchTips[root]
	s.mu.Unlock()
	if ok && time.Since(read.at) < tipsTTL {
		return read.tips
	}
	tips, err := worktreefs.BranchTips(root)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	if s.branchTips == nil {
		s.branchTips = map[string]tipsRead{}
	}
	s.branchTips[root] = tipsRead{tips, time.Now()}
	s.mu.Unlock()
	return tips
}

// pastChangesKey is what a past agent's changes are measured from: its kept
// branch's commit and where it started. While the branch stays put, so does
// what it changed.
type pastChangesKey struct{ root, commit, baseCommit, baseRef string }

// pastChangesOf is what a past agent's kept branch changed, nil when that
// can't be told, from tips (or git, when tips is nil). It is measured once
// per commit the branch is at: a git diff for every past agent on a page, on
// every read of it, was most of what the Work page waited for, from 3 ms to
// a second each.
func (s *Server) pastChangesOf(root string, a state.ArchivedAgent, tips map[string]string) *api.AgentChanges {
	commit := tips[a.Branch]
	if tips == nil {
		commit, _ = worktreefs.BranchCommit(root, a.Branch)
	}
	if commit == "" || a.Branch == "" || strings.HasPrefix(a.Branch, "-") {
		return nil
	}
	key := pastChangesKey{root, commit, a.BaseCommit, a.BaseRef}
	s.mu.Lock()
	c, ok := s.pastChanges[key]
	s.mu.Unlock()
	if ok {
		return c
	}
	base := worktreefs.BaseAt(root, a.BaseCommit, a.BaseRef, commit)
	if base == "" {
		return nil
	}
	stat, err := gitrepo.DiffCommits(root, base, commit, true)
	if err != nil {
		return nil // not kept: the next read tries again
	}
	measured := statOf(stat)
	s.mu.Lock()
	if s.pastChanges == nil {
		s.pastChanges = map[pastChangesKey]*api.AgentChanges{}
	}
	s.pastChanges[key] = &measured
	s.mu.Unlock()
	return &measured
}

// archivedRange is where a past agent's kept branch is, and what it is
// measured against ("" when that isn't known).
func archivedRange(root string, a state.ArchivedAgent) (commit, base string, err error) {
	if a.Branch == "" {
		return "", "", fmt.Errorf("%w: %s had no branch", state.ErrNotFound, a.Name)
	}
	commit, err = worktreefs.BranchCommit(root, a.Branch)
	if errors.Is(err, worktreefs.ErrNoBranch) {
		return "", "", fmt.Errorf("%w: branch %s is gone, from the project's repository and from origin", state.ErrNotFound, a.Branch)
	}
	if err != nil {
		return "", "", err
	}
	return commit, worktreefs.BaseAt(root, a.BaseCommit, a.BaseRef, commit), nil
}

// archivedDiff is GET /v1/archived-agents/{project}/{agent}/diff: a past
// agent's whole diff, its kept branch against where it started, as the live
// route gives a live agent's.
func (s *Server) archivedDiff(w http.ResponseWriter, r *http.Request) error {
	root, commit, base, _, err := s.archivedCommit(r)
	if err != nil {
		return err
	}
	if base == "" {
		return fmt.Errorf("%w: where this agent's branch started is no longer known", state.ErrNotFound)
	}
	stat, _ := strconv.ParseBool(r.URL.Query().Get("stat"))
	text, err := gitrepo.DiffCommits(root, base, commit, stat)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, text)
	return nil
}
