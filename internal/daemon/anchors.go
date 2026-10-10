package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Closing open items when what they wait on is over (internal/memory/anchors.go).
//
// An issue memory that names pull requests, branches or a question is closed
// once every one of them is: a pull request merged or closed, a branch whose
// pull request was, a question answered. The daemon is what can look those
// up — the questions are its own table, the pull requests one GraphQL request
// per project — and it does so on the hourly sweep, and straight away when it
// sees one of them end: a merge from the app, the pull request watch seeing
// one merged or closed, an answer to a question.
//
// No model is asked. What can't be looked up is left open, and ages out of
// the lead's recap instead (memory.StaleAfter).

// closeAnchored resolves the project's open items whose anchors are all over,
// and answers how many it closed.
func (s *Server) closeAnchored(ctx context.Context, p state.Project) int {
	store := s.memory()
	items, err := store.OpenAnchored(ctx, p.Name)
	if err != nil {
		s.logf("closing %s's open items: %v", p.Name, err)
		return 0
	}
	if len(items) == 0 {
		return 0
	}
	var numbers []int
	var branches, questions []string
	for _, it := range items {
		for _, a := range it.Closers() {
			switch a.Kind {
			case memory.AnchorPR:
				if n, err := strconv.Atoi(a.Value); err == nil && !slices.Contains(numbers, n) {
					numbers = append(numbers, n)
				}
			case memory.AnchorBranch:
				if !slices.Contains(branches, a.Value) {
					branches = append(branches, a.Value)
				}
			case memory.AnchorQuestion:
				if !slices.Contains(questions, a.Value) {
					questions = append(questions, a.Value)
				}
			}
		}
	}
	closed := map[memory.Anchor]memory.Closed{}
	s.questionsClosed(ctx, p.Name, questions, closed)
	// GitHub is asked only where the pull request watch is on: a user who
	// switched it off to spare their account's budget meant this too.
	if len(numbers)+len(branches) > 0 && s.prWatchOn(ctx, p) {
		s.pullRequestsClosed(ctx, p, numbers, branches, closed)
	}
	if len(closed) == 0 {
		return 0
	}
	resolved, err := store.ResolveAnchored(ctx, p.Name, closed)
	if err != nil {
		s.logf("closing %s's open items: %v", p.Name, err)
	}
	for _, m := range resolved {
		s.logf("memory: %s: closed %q (%s)", p.Name, m.Title, m.ResolvedBy)
	}
	return len(resolved)
}

// closeAnchoredSoon is closeAnchored off the caller's stack, for the moments
// one anchor has just ended: nothing that merged a pull request or answered a
// question waits on memory.
func (s *Server) closeAnchoredSoon(project string) {
	go func() {
		ctx := s.background()
		p, err := s.store.Project(ctx, project)
		if err != nil || p.Consolidation == state.ConsolidationOff {
			return
		}
		s.closeAnchored(ctx, p)
	}()
}

// questionsClosed marks the questions that were answered, or that nobody will
// answer now.
func (s *Server) questionsClosed(ctx context.Context, project string, ids []string, closed map[memory.Anchor]memory.Closed) {
	for _, id := range ids {
		q, err := s.store.Question(ctx, id)
		if err != nil || q.Project != project {
			continue // another project's, or no question at all
		}
		a := memory.Anchor{Kind: memory.AnchorQuestion, Value: id}
		switch q.Status {
		case state.QuestionAnswered:
			closed[a] = memory.Closed{At: q.AnsweredAt, How: "answered"}
		case state.QuestionCancelled:
			closed[a] = memory.Closed{At: q.AnsweredAt, How: "cancelled"}
		}
	}
}

// pullRequestsClosed marks the pull requests merged or closed, and the
// branches whose pull requests all were. A project with no GitHub repository
// or no token has nothing looked up, and its items wait for the recap to age
// them out.
func (s *Server) pullRequestsClosed(ctx context.Context, p state.Project, numbers []int, branches []string, closed map[memory.Anchor]memory.Closed) {
	client, repo, err := s.githubFor(p)
	if err != nil {
		return
	}
	prs, byBranch, err := client.PullRequestStates(ctx, repo, numbers, branches)
	if err != nil {
		if !errors.Is(err, errNoGitHubToken) {
			s.logf("closing %s's open items: %v", p.Name, err)
		}
		return
	}
	for n, pr := range prs {
		if pr.State == "merged" || pr.State == "closed" {
			closed[memory.Anchor{Kind: memory.AnchorPR, Value: strconv.Itoa(n)}] = memory.Closed{At: pr.ClosedAt, How: pr.State}
		}
	}
	for branch, list := range byBranch {
		var last time.Time
		how := "closed"
		open := false
		for _, pr := range list {
			switch pr.State {
			case "open":
				open = true
			case "merged":
				how = "merged"
			}
			if pr.ClosedAt.After(last) {
				last = pr.ClosedAt
			}
		}
		if !open && len(list) > 0 {
			closed[memory.Anchor{Kind: memory.AnchorBranch, Value: branch}] = memory.Closed{At: last, How: how}
		}
	}
}

// mentionedIn are the anchors an event's payload names: what MentionAnchors
// keeps in the lead's "Still open" when an agent's work touches it.
func mentionedIn(payload []byte) []memory.Anchor {
	var doc any
	if json.Unmarshal(payload, &doc) != nil {
		return nil
	}
	var b strings.Builder
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				walk(k, x)
			}
		case []any:
			for _, x := range v {
				walk(key, x)
			}
		case string:
			if key == "questionId" {
				v = "question " + v
			}
			b.WriteString(" " + v)
		case float64:
			// A pull request's number is a number in a payload, and only a
			// number: "#" is what says it is one in prose.
			if key == "number" || key == "pr" {
				b.WriteString(" #" + strconv.Itoa(int(v)))
			}
		}
	}
	walk("", doc)
	return memory.ExtractAnchors(b.String())
}

// unmentioning are the event types whose words don't count as anybody
// bringing an issue up: the project chat's own turns, which are where a
// recap's stale issues get repeated, and AgentBox's own bookkeeping.
var unmentioning = []string{"lead_turn", memory.EventMemoryConsolidated, memory.EventConversationCompacted}

// mentionAnchors marks the open items an event names as mentioned.
func (s *Server) mentionAnchors(ctx context.Context, project, eventType string, payload []byte) {
	if slices.Contains(unmentioning, eventType) {
		return
	}
	anchors := mentionedIn(payload)
	if len(anchors) == 0 {
		return
	}
	if err := s.memory().MentionAnchors(ctx, project, time.Now(), anchors...); err != nil {
		s.logf("memory: %s: marking what a %s event names: %v", project, eventType, err)
	}
}
