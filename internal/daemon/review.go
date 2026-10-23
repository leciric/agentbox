package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// "Ready to review" is the agents whose work is done and waiting for the user
// to look at it: each one that finished, and that the user hasn't marked
// reviewed or dismissed since. A verdict is kept against a fingerprint of the
// work (reviewMark), so an agent that changes again — finishes again, gets new
// commits, has its pull request's checks or conflict fixed — comes back.
//
// Whether an agent has finished at all is finishOf's to say, and only its:
// agent_finished is being reworked, and this list should follow it by
// changing that one function.

// finishOf is the finish "Ready to review" counts: a's newest finished
// event with something to show (outcome done; an event from before outcomes
// has none), unless a turn ended differently since — it asked, failed, lost
// its chat or stalled — or a turn of its chat is running now, which means
// it's at work again.
func (s *Server) finishOf(ctx context.Context, a state.Agent) (api.AgentEvent, bool) {
	if a.IsLead() || s.chat.Busy(a) {
		return api.AgentEvent{}, false
	}
	rows, err := s.store.AgentEventsOf(ctx, a.Project, a.Name)
	if err != nil {
		return api.AgentEvent{}, false
	}
	for _, row := range rows {
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) != nil {
			continue
		}
		switch ev.Kind {
		case api.AgentFinished:
			return ev, ev.Outcome != api.AgentOutcomeIdle
		case api.AgentAsking, api.AgentFailed, api.AgentChatLost, api.AgentStalled, api.AgentLimited:
			return api.AgentEvent{}, false
		}
	}
	return api.AgentEvent{}, false
}

// reviewMark is what a verdict is recorded against, sent to the app as the
// item's Fingerprint and back with the verdict: the finish, where the
// agent's branch is, and its pull request as the cache has it.
type reviewMark struct {
	Finish   string `json:"f"`
	Tip      string `json:"t,omitempty"`
	PR       int    `json:"p,omitempty"`
	PRHead   string `json:"h,omitempty"`
	Failing  bool   `json:"x,omitempty"`
	Conflict bool   `json:"c,omitempty"`
}

func markOf(finish api.AgentEvent, tip string, pr *api.PullRequest) reviewMark {
	m := reviewMark{Finish: finish.ID, Tip: tip}
	if pr != nil {
		m.PR, m.PRHead, m.Failing, m.Conflict = pr.Number, pr.HeadSHA, pr.Checks == "failing", pr.Conflict
	}
	return m
}

func (m reviewMark) String() string {
	data, _ := json.Marshal(m)
	return string(data)
}

// movedTo reports whether the work changed between a verdict (then) and now,
// enough to be looked at again: a new finish, new commits, another pull
// request or new commits on it, or its failing checks or conflict fixed. A
// pull request that started failing isn't work to review. Nor is one the
// cache doesn't know yet, as after the daemon restarts: that's no news.
//
// No tip now, when there was one, is no news either: it is what a branch git
// couldn't read looks like (ENFILE once brought back every reviewed agent in
// eight projects at once), and a branch can't lose its commits by moving on.
func (then reviewMark) movedTo(now reviewMark) bool {
	if now.Finish != then.Finish || (now.Tip != then.Tip && now.Tip != "") {
		return true
	}
	if now.PR == 0 {
		return false
	}
	if now.PR != then.PR || now.PRHead != then.PRHead {
		return true
	}
	return (then.Failing && !now.Failing) || (then.Conflict && !now.Conflict)
}

// reviewItems is what of agents, all of project p, finished, each with its
// verdict when the user gave one and the work hasn't moved since: those are
// Reviewed, the rest ready to review. Pull requests come from the project's
// cache, as the fleet's do, so this never waits on GitHub.
//
// With listing, it is the review list, which leaves out an agent the cache
// can't yet say has no merged pull request (pullsEntry.answered): the cache
// was emptied, or never read for its commits. Offered meanwhile, an agent
// whose pull request was merged long ago, only a lookup knows that, came back
// as work to review across every project and vanished again once GitHub
// answered, by which time clicking it opened nothing. The read this starts
// announces itself (EventPulls), and the app reads the list again.
func (s *Server) reviewItems(ctx context.Context, p state.Project, agents []state.Agent, listing bool) []api.ReviewItem {
	var out []api.ReviewItem
	for _, e := range s.liveWork(ctx, p, agents, false, listing) {
		if e.item.Verdict == "" && e.ended {
			// Merged or closed is done with, whoever did it and whenever:
			// never work to review. The PR watch and the startup reconcile
			// free its machine (freeReviewed); one kept for work that is only
			// there stays in the sidebar, not here.
			continue
		}
		out = append(out, e.item)
	}
	return out
}

// liveEntry is one live agent's work, as the review lists and the Work page
// (work.go) read it: its review item, whether it has finished (the item has
// no finish or fingerprint until it has), and whether its pull request was
// merged or closed.
type liveEntry struct {
	agent    state.Agent
	item     api.ReviewItem
	finished bool
	ended    bool
}

// liveWork is the work of agents, all of project p: the finished ones only,
// or with unfinished every worker, at work or not. With listing, an agent
// whose pull request the cache can't yet answer for is unknown (see
// reviewItems).
func (s *Server) liveWork(ctx context.Context, p state.Project, agents []state.Agent, unfinished, listing bool) []liveEntry {
	var kept []state.Agent
	finishes := map[string]api.AgentEvent{}
	for _, a := range agents {
		if a.Status == state.AgentDestroying || a.IsLead() {
			continue // a past agent in a moment
		}
		ev, ok := s.finishOf(ctx, a)
		if ok {
			finishes[a.Name] = ev
		}
		if ok || unfinished {
			kept = append(kept, a)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	// An agent is unknown when what its item is made of can't be read now:
	// its branches (git failing), or, for the list, whether its work has a
	// pull request (#447). An unknown agent is shown as it was last read in
	// full, for the same finish, or not at all: a read error never adds an
	// item, nor brings one back that the user reviewed.
	_, entry, heads, _, err := s.projectPulls(p, kept)
	var prs map[string]*api.PullRequest
	unknown := map[string]bool{}
	switch {
	case err == nil:
		prs = entry.byAgent(heads)
		for _, h := range heads {
			if h.failed || (listing && !entry.answered(h)) {
				unknown[h.agent] = true
			}
		}
	case pullsAbsent(err):
		// No GitHub to ask: the branches alone.
		heads = agentHeads(p.Root, kept)
		unknown = unreadHeads(heads)
	default:
		for _, a := range kept {
			unknown[a.Name] = true
		}
	}
	// An agent with several branches has moved when any of them has: its
	// tips, first branch first, which for one branch is that branch's tip.
	tips := make(map[string]string, len(heads))
	for _, h := range heads {
		if tips[h.agent] != "" && h.tip != "" {
			tips[h.agent] += ","
		}
		tips[h.agent] += h.tip
	}
	out := make([]liveEntry, 0, len(kept))
	for _, a := range kept {
		ev, finished := finishes[a.Name]
		e := liveEntry{agent: a, finished: finished}
		if finished && unknown[a.Name] {
			known, ok := s.knownReviewOf(a.Ref(), ev.ID)
			if !ok {
				if !unfinished {
					continue
				}
				// The Work page still lists it, as work whose finish isn't read.
				e.finished = false
				e.item = api.ReviewItem{Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title, Branch: a.Branch, AI: a.AI}
				out = append(out, e)
				continue
			}
			e.item, e.ended = known.item, known.ended
		} else {
			pr := prs[a.Name]
			s.prWatch.overlay(p.Name, pr)
			e.ended = prEnded(pr, a)
			e.item = api.ReviewItem{Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title, Branch: branchOf(heads, a, pr), AI: a.AI, PR: pr}
			if !finished {
				out = append(out, e)
				continue
			}
			mark := markOf(ev, tips[a.Name], pr)
			if pr == nil {
				e.item.PR = ev.PR
			}
			e.item.Summary, e.item.Changes, e.item.FinishedAt, e.item.Fingerprint = ev.Summary, ev.Changes, ev.At, mark.String()
			if !unknown[a.Name] {
				s.rememberReview(a.Ref(), ev.ID, e.item, e.ended)
			}
		}
		r, ok, err := s.store.AgentReviewOf(ctx, a.Project, a.Name)
		if err != nil {
			continue // its verdict unread, it may well be one the user gave
		}
		if ok {
			var then, mark reviewMark
			if json.Unmarshal([]byte(r.Fingerprint), &then) == nil && json.Unmarshal([]byte(e.item.Fingerprint), &mark) == nil && !then.movedTo(mark) {
				at := r.At
				e.item.Verdict, e.item.ReviewedAt = r.Verdict, &at
				e.item.MachineKept = r.Verdict == api.ReviewReviewed && s.isMachineKept(a.Ref())
			}
		}
		out = append(out, e)
	}
	return out
}

// knownReview is an agent's review item, before its verdict, as last read
// with everything it is made of readable: what the review list shows of it
// while some of that can't be read.
type knownReview struct {
	finish string // the finish it was read for; another finish is other work
	item   api.ReviewItem
	ended  bool // its pull request was merged or closed
}

func (s *Server) rememberReview(ref, finish string, item api.ReviewItem, ended bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviewKnown == nil {
		s.reviewKnown = map[string]knownReview{}
	}
	s.reviewKnown[ref] = knownReview{finish: finish, item: item, ended: ended}
}

// knownReviewOf is what rememberReview last kept of ref, if it was for this
// finish.
func (s *Server) knownReviewOf(ref, finish string) (knownReview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.reviewKnown[ref]
	return k, ok && k.finish == finish
}

// branchOf is the branch of a's that pr, its pull request, came from: a
// follow-up's own branch rather than the one a was made with.
func branchOf(heads []agentHead, a state.Agent, pr *api.PullRequest) string {
	if pr != nil {
		for _, h := range heads {
			if h.agent == a.Name && h.accepts(*pr, pr.HeadSHA) {
				return h.branch
			}
		}
	}
	return a.Branch
}

// reviewedLimit is how many reviewed agents the unpaged Reviewed list keeps.
const reviewedLimit = 50

// reviewPageMax is the most items a page holds, whatever ?limit= asks.
const reviewPageMax = 200

// reviewKeyOf is what a review list sorts by, newest first: the finish of
// what's ready to review, the verdict of what's reviewed.
func reviewKeyOf(i api.ReviewItem) int64 {
	if i.Verdict != "" && i.ReviewedAt != nil {
		return i.ReviewedAt.UnixNano()
	}
	return i.FinishedAt.UnixNano()
}

// reviewList is the answer to both review routes: what's ready to review,
// newest finish first, or with ?reviewed=1 what the user already reviewed or
// dismissed, the latest verdict first. With ?limit= it is a page of that
// (ReviewPage), after ?cursor=; without, every item as a plain list, the
// reviewed ones capped at reviewedLimit.
func reviewList(w http.ResponseWriter, r *http.Request, items []api.ReviewItem) error {
	q := r.URL.Query()
	reviewed := q.Get("reviewed") != ""
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("limit must be a positive number")
		}
		limit = min(n, reviewPageMax)
	}
	out := []api.ReviewItem{}
	for _, i := range items {
		if (i.Verdict != "") == reviewed {
			out = append(out, i)
		}
	}
	// The ref breaks a tie, so a cursor always names one place in the list.
	slices.SortStableFunc(out, func(a, b api.ReviewItem) int {
		return cmp.Or(cmp.Compare(reviewKeyOf(b), reviewKeyOf(a)), cmp.Compare(a.Ref, b.Ref))
	})
	if limit == 0 {
		if reviewed && len(out) > reviewedLimit {
			out = out[:reviewedLimit]
		}
		return writeJSON(w, http.StatusOK, out)
	}
	page := api.ReviewPage{Total: len(out)}
	if cursor := q.Get("cursor"); cursor != "" {
		at, ref, ok := strings.Cut(cursor, ":")
		key, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil {
			return fmt.Errorf("cursor %q isn't one this list gave", cursor)
		}
		// Whatever left the list since (reviewed, merged) doesn't move the
		// place: it is the first item sorting after the cursor.
		out = slices.DeleteFunc(out, func(i api.ReviewItem) bool {
			return cmp.Or(cmp.Compare(reviewKeyOf(i), key), cmp.Compare(ref, i.Ref)) >= 0
		})
	}
	if len(out) > limit {
		last := out[limit-1]
		page.Next = fmt.Sprintf("%d:%s", reviewKeyOf(last), last.Ref)
		out = out[:limit]
	}
	page.Items = out
	return writeJSON(w, http.StatusOK, page)
}

// projectReview is GET /v1/projects/{project}/review: the project's agents
// ready to review, newest first, or reviewed (reviewList).
func (s *Server) projectReview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := s.store.Project(ctx, r.PathValue("project"))
	if err != nil {
		return err
	}
	items, err := s.projectReviewItems(ctx, p)
	if err != nil {
		return err
	}
	return reviewList(w, r, items)
}

// allReview is GET /v1/review: every project's agents ready to review,
// newest first, or reviewed (reviewList).
func (s *Server) allReview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return err
	}
	var items []api.ReviewItem
	for _, p := range projects {
		more, err := s.projectReviewItems(ctx, p)
		if err != nil {
			return err
		}
		items = append(items, more...)
	}
	return reviewList(w, r, items)
}

func (s *Server) projectReviewItems(ctx context.Context, p state.Project) ([]api.ReviewItem, error) {
	if s.prReconciling(p.Name) {
		// Not before GitHub was asked what was merged while the daemon was
		// down (prreconcile.go): the list would offer every one of those.
		return nil, nil
	}
	agents, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	return s.reviewItems(ctx, p, agents, true), nil
}

// reviewAgent is POST /v1/agents/{project}/{agent}/review: the user reviewed
// or dismissed an agent's work, the work they saw by its fingerprint, so a
// change that landed while they looked still brings it back. With none, it
// is the work as it is now.
func (s *Server) reviewAgent(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := s.agentFromPath(r)
	if errors.Is(err, state.ErrNotFound) {
		return s.reviewPastAgent(w, r)
	}
	if err != nil {
		return err
	}
	var req api.ReviewRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Verdict != api.ReviewReviewed && req.Verdict != api.ReviewDismissed {
		return fmt.Errorf("verdict must be %q or %q", api.ReviewReviewed, api.ReviewDismissed)
	}
	fingerprint := req.Fingerprint
	if fingerprint == "" {
		p, err := s.store.Project(ctx, a.Project)
		if err != nil {
			return err
		}
		items := s.reviewItems(ctx, p, []state.Agent{a}, false)
		if len(items) == 0 {
			return fmt.Errorf("%s hasn't finished", a.Ref())
		}
		fingerprint = items[0].Fingerprint
	} else if json.Unmarshal([]byte(fingerprint), &reviewMark{}) != nil {
		return fmt.Errorf("fingerprint %q isn't one this list gave", fingerprint)
	}
	if err := s.store.SetAgentReview(ctx, state.AgentReview{
		Project: a.Project, Agent: a.Name, Verdict: req.Verdict, Fingerprint: fingerprint, At: time.Now(),
	}); err != nil {
		return err
	}
	s.events.publish(api.EventReview, a.Project)
	if req.Verdict == api.ReviewReviewed {
		s.freeReviewed(ctx, a, "marked reviewed", nil)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// reviewPastAgent is reviewAgent for an agent that is no longer live: merging
// its pull request from the review freed its machine (freeReviewed), so it is
// a past agent by the time the app saves the verdict. The verdict is kept
// against the fingerprint the user saw; with none there is nothing to compare
// and the work is already done with, so it succeeds without writing.
func (s *Server) reviewPastAgent(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	project, name := r.PathValue("project"), r.PathValue("agent")
	if _, err := s.store.ArchivedAgentByName(ctx, project, name); err != nil {
		return err
	}
	var req api.ReviewRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Verdict != api.ReviewReviewed && req.Verdict != api.ReviewDismissed {
		return fmt.Errorf("verdict must be %q or %q", api.ReviewReviewed, api.ReviewDismissed)
	}
	if req.Fingerprint != "" {
		if json.Unmarshal([]byte(req.Fingerprint), &reviewMark{}) != nil {
			return fmt.Errorf("fingerprint %q isn't one this list gave", req.Fingerprint)
		}
		if err := s.store.SetAgentReview(ctx, state.AgentReview{
			Project: project, Agent: name, Verdict: req.Verdict, Fingerprint: req.Fingerprint, At: time.Now(),
		}); err != nil {
			return err
		}
		s.events.publish(api.EventReview, project)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// requestChanges is POST /v1/agents/{project}/{agent}/request-changes: the
// user looked at an agent's work and wants something changed. The text goes
// to the agent the way the lead's tell_agent sends it (tellAgent, which
// wakes a stopped machine), and is recorded as the verdict "changes" on the
// work they saw: the agent leaves Ready to review while it works, the app
// shows what was asked (Agent.ChangesRequested), and its next finish is new
// work, back on the list.
func (s *Server) requestChanges(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.RequestChangesRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	text := strings.TrimSpace(req.Text)
	notes, err := s.draftNotes(ctx, a, req.Notes)
	if err != nil {
		return err
	}
	if text == "" && len(notes) == 0 {
		return errors.New("say what to change")
	}
	fingerprint := req.Fingerprint
	if fingerprint == "" {
		p, err := s.store.Project(ctx, a.Project)
		if err != nil {
			return err
		}
		if items := s.reviewItems(ctx, p, []state.Agent{a}, false); len(items) > 0 {
			fingerprint = items[0].Fingerprint
		}
	} else if json.Unmarshal([]byte(fingerprint), &reviewMark{}) != nil {
		return fmt.Errorf("fingerprint %q isn't one this list gave", fingerprint)
	}
	s.setFreeWhenIdle(a.Ref(), false) // it has work to do again
	// Recorded first, so the agent is never back in Ready to review on the
	// work the user just turned down while the message is on its way.
	if err := s.store.SetAgentReview(ctx, state.AgentReview{
		Project: a.Project, Agent: a.Name, Verdict: api.ReviewChanges, Fingerprint: fingerprint, Note: notesSummary(text, notes), At: time.Now(),
	}); err != nil {
		return err
	}
	t, err := s.tellAgent(ctx, a, changesMessage(text, notes))
	s.events.publish(api.EventReview, a.Project)
	if err != nil {
		return err
	}
	// Sent once the agent has them: on failure they stay drafts, to send again.
	if err := s.markNotesSent(ctx, a, notes); err != nil {
		return err
	}
	s.refreshAgents(ctx)
	return writeJSON(w, http.StatusAccepted, api.TellResult{ChatItem: t.item, Woke: t.woke})
}

// changesMessage is what an agent is told when the user asks for changes
// from the review: what they wrote, then their notes on the diff.
func changesMessage(text string, notes []state.ReviewNote) string {
	var parts []string
	if text != "" {
		parts = append(parts, text)
	}
	if len(notes) > 0 {
		parts = append(parts, "Notes on lines of your diff:\n\n"+notesText(notes))
	}
	return "The user reviewed your work and asks for changes:\n\n" + strings.Join(parts, "\n\n") +
		"\n\nMake them on the same branch, update the pull request, and finish as before. " +
		"Delete media the changes outdate (agentbox media rm <id>) and record it again under the same names."
}

// changesRequested is what the user asked a working agent to change from the
// review, while its chat is still on it: once the turn ends the agent is
// done, asking or stopped, and the request is behind it.
func (s *Server) changesRequested(ctx context.Context, project, name, chatState string) string {
	switch chatState {
	case "running", "starting", "waiting":
	default:
		return ""
	}
	r, ok, err := s.store.AgentReviewOf(ctx, project, name)
	if err != nil || !ok || r.Verdict != api.ReviewChanges {
		return ""
	}
	return r.Note
}
