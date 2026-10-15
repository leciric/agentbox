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
		case api.AgentAsking, api.AgentFailed, api.AgentChatLost, api.AgentStalled:
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
func (then reviewMark) movedTo(now reviewMark) bool {
	if now.Finish != then.Finish || now.Tip != then.Tip {
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
func (s *Server) reviewItems(ctx context.Context, p state.Project, agents []state.Agent) []api.ReviewItem {
	var finished []state.Agent
	var finishes []api.AgentEvent
	for _, a := range agents {
		if ev, ok := s.finishOf(ctx, a); ok {
			finished, finishes = append(finished, a), append(finishes, ev)
		}
	}
	if len(finished) == 0 {
		return nil
	}
	_, entry, heads, _, err := s.projectPulls(p, finished)
	var prs map[string]*api.PullRequest
	if err == nil {
		prs = entry.byAgent(heads)
	} else {
		heads = agentHeads(p.Root, finished)
	}
	tips := make(map[string]string, len(heads))
	for _, h := range heads {
		tips[h.agent] = h.tip
	}
	var out []api.ReviewItem
	for i, a := range finished {
		ev := finishes[i]
		pr := prs[a.Name]
		s.prWatch.overlay(p.Name, pr)
		mark := markOf(ev, tips[a.Name], pr)
		if pr == nil {
			pr = ev.PR
		}
		item := api.ReviewItem{
			Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title, Branch: a.Branch, AI: a.AI,
			Summary: ev.Summary, Changes: ev.Changes, PR: pr, FinishedAt: ev.At, Fingerprint: mark.String(),
		}
		if r, ok, err := s.store.AgentReviewOf(ctx, a.Project, a.Name); err == nil && ok {
			var then reviewMark
			if json.Unmarshal([]byte(r.Fingerprint), &then) == nil && !then.movedTo(mark) {
				at := r.At
				item.Verdict, item.ReviewedAt = r.Verdict, &at
			}
		}
		out = append(out, item)
	}
	return out
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
	agents, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	return s.reviewItems(ctx, p, agents), nil
}

// reviewAgent is POST /v1/agents/{project}/{agent}/review: the user reviewed
// or dismissed an agent's work, the work they saw by its fingerprint, so a
// change that landed while they looked still brings it back. With none, it
// is the work as it is now.
func (s *Server) reviewAgent(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := s.agentFromPath(r)
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
		items := s.reviewItems(ctx, p, []state.Agent{a})
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
	w.WriteHeader(http.StatusNoContent)
	return nil
}
