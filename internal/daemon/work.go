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
	var all []workEntry
	for _, p := range projects {
		more, err := s.projectWorkEntries(ctx, p)
		if err != nil {
			return err
		}
		all = append(all, more...)
	}
	q := strings.ToLower(strings.TrimSpace(v.Get("q")))
	page := api.WorkPage{Items: []api.WorkItem{}, Counts: map[string]int{}}
	var kept []workEntry
	for _, e := range all {
		if !e.matches(q) {
			continue
		}
		page.Counts[""]++
		for _, f := range []string{api.WorkFilterReview, api.WorkFilterOpen, api.WorkFilterMerged, api.WorkFilterClosed, api.WorkFilterNone} {
			if e.in(f) {
				page.Counts[f]++
			}
		}
		if filter == "" || e.in(filter) {
			kept = append(kept, e)
		}
	}
	page.Total = len(kept)
	// The ref breaks a tie, so a cursor always names one place in the list.
	key := func(e workEntry) int64 { return e.item.At.UnixNano() }
	slices.SortStableFunc(kept, func(a, b workEntry) int {
		return cmp.Or(cmp.Compare(key(b), key(a)), cmp.Compare(a.item.Item.Ref, b.item.Item.Ref))
	})
	if cursor := v.Get("cursor"); cursor != "" {
		at, ref, ok := strings.Cut(cursor, ":")
		n, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil {
			return fmt.Errorf("cursor %q isn't one this list gave", cursor)
		}
		kept = slices.DeleteFunc(kept, func(e workEntry) bool {
			return cmp.Or(cmp.Compare(key(e), n), cmp.Compare(ref, e.item.Item.Ref)) >= 0
		})
	}
	if len(kept) > limit {
		last := kept[limit-1]
		page.Next = fmt.Sprintf("%d:%s", key(last), last.item.Item.Ref)
		kept = kept[:limit]
	}
	// What costs a query or a git process each is read for the page only.
	for _, e := range kept {
		page.Items = append(page.Items, s.filledWork(ctx, e))
	}
	return writeJSON(w, http.StatusOK, page)
}

// workEntry is one agent's work before its page is filled in: past agents
// keep what they were archived as, for their changes and report.
type workEntry struct {
	item api.WorkItem
	past *state.ArchivedAgent
	root string // the project's repository
}

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

// projectWorkEntries is every agent's work in p, live and past, but not
// filled in (filledWork).
func (s *Server) projectWorkEntries(ctx context.Context, p state.Project) ([]workEntry, error) {
	var out []workEntry
	if !s.prReconciling(p.Name) {
		agents, err := s.store.Agents(ctx, p.Name)
		if err != nil {
			return nil, err
		}
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
	}
	past, err := s.store.ArchivedAgents(ctx, state.ArchivedQuery{Project: p.Name})
	if err != nil {
		return nil, err
	}
	past = s.refreshArchivedPRs(ctx, past)
	for i := range past {
		a := &past[i]
		item := api.WorkItem{
			Item: api.ReviewItem{Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title, Branch: a.Branch, AI: a.AI, FinishedAt: a.ArchivedAt},
			Task: a.Task, Past: true, Status: api.WorkDone, At: a.ArchivedAt,
		}
		if a.PR.Number > 0 {
			item.Item.PR = &api.PullRequest{Number: a.PR.Number, Title: a.PR.Title, URL: a.PR.URL, State: a.PR.State}
		}
		if r, ok, err := s.store.AgentReviewOf(ctx, a.Project, a.Name); err == nil && ok {
			at := r.At
			item.Item.Verdict, item.Item.ReviewedAt, item.Status = r.Verdict, &at, api.WorkReviewed
		}
		out = append(out, workEntry{item: item, past: a, root: p.Root})
	}
	return out, nil
}

// filledWork is e with its report, its media and, for a past agent, what its
// kept branch changed.
func (s *Server) filledWork(ctx context.Context, e workEntry) api.WorkItem {
	item := e.item
	i := item.Item
	if reports, err := s.memory().Reports(ctx, i.Project, i.Agent); err == nil && len(reports) > 0 {
		r := apiReport(reports[0])
		item.Report = &r
		if item.Item.Summary == "" {
			item.Item.Summary = r.Summary
		}
	}
	if tallies, err := s.store.MediaTallies(ctx, state.MediaFilter{Project: i.Project, Agent: i.Agent}); err == nil {
		for _, t := range tallies {
			switch t.Kind {
			case "screenshot":
				item.Screenshots += t.Count
			case "recording":
				item.Recordings += t.Count
			}
		}
	}
	if e.past != nil && item.Item.Changes == nil {
		if commit, base, err := archivedRange(e.root, *e.past); err == nil && base != "" {
			if stat, err := gitrepo.DiffCommits(e.root, base, commit, true); err == nil {
				c := statOf(stat)
				item.Item.Changes = &c
			}
		}
	}
	return item
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
