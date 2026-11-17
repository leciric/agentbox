package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// "Resolved — merge when CI is green" is a verdict in the review: the user
// has looked at an agent's work and wants its pull request merged once its
// checks pass, without coming back for it. The daemon keeps the mark
// (state.MergeWhenGreen) and the pull request watch (prwatch.go), which reads
// the pull request's checks and mergeability anyway, acts on it: green and
// mergeable, it merges as the review's Merge does (mergePR), with the method
// the user had picked there. Red checks, a conflict and new commits don't
// give it up: the watch tells the agent to fix them, as for any pull request,
// and the fix is new commits, which the user resolved in advance. The item
// stays out of the review meanwhile, saying what it waits on (mergeGreen).
// It goes back to the review, with why, only when the pull request is
// closed, GitHub refuses the merge for a reason no fix changes, nobody is left
// to fix it, or it stays unmergeable past the limits. It is the daemon's and
// not GitHub's auto-merge, which a repository has to allow.
//
// The mark records the verdict "reviewed" as Mark reviewed does, but leaves
// the agent's machine up for those fixes, and the merge frees it
// (afterMerge) as any other merge does.

// mergeWhenGreen is POST /v1/projects/{project}/pulls/{number}/merge-when-green.
func (s *Server) mergeWhenGreen(w http.ResponseWriter, r *http.Request) error {
	ctx := github.WithCaller(r.Context(), "merge")
	if on, err := s.store.Flag(ctx, state.SettingMergeWhenGreen); err != nil {
		return err
	} else if !on {
		return fmt.Errorf("merge when CI is green is off: turn it on in Settings")
	}
	p, number, err := s.pullFromPath(r)
	if err != nil {
		return err
	}
	var req api.MergeWhenGreenRequest
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
		return noGitHubToken(err)
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

	mark := state.MergeWhenGreen{Project: p.Name, Number: number, Agent: req.Agent, Method: string(method), HeadSHA: pr.HeadSHA, At: time.Now()}
	if req.Agent != "" {
		fingerprint, err := s.resolveReview(ctx, p, req.Agent, req.Fingerprint)
		if err != nil {
			return err
		}
		mark.Fingerprint = fingerprint
	}
	if err := s.store.SetMergeWhenGreen(ctx, mark); err != nil {
		return err
	}
	s.logf("%s: #%d is to be merged (%s) when its checks pass", p.Name, number, method)
	s.events.publish(api.EventReview, p.Name)
	// Green already, it merges at once: the watch looks now rather than when
	// the project is next due.
	s.prWatch.repo(p.Name)
	s.prWatch.poke(p.Name)
	if s.prWatch.every > 0 {
		go s.pollPRWatch(s.background())
	}
	return writeJSON(w, http.StatusOK, mergeWhenGreenToAPI(mark))
}

// resolveReview records the verdict "reviewed" on a live agent's work, the
// work the user saw by its fingerprint (as reviewAgent), and answers the
// fingerprint recorded. A past agent's work is done with: nothing is
// recorded. Unlike Mark reviewed, the agent's machine stays until the merge.
func (s *Server) resolveReview(ctx context.Context, p state.Project, name, fingerprint string) (string, error) {
	a, err := s.store.Agent(ctx, p.Name, name)
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if fingerprint == "" {
		items := s.reviewItems(ctx, p, []state.Agent{a}, false)
		if len(items) == 0 {
			return "", fmt.Errorf("%s hasn't finished", a.Ref())
		}
		fingerprint = items[0].Fingerprint
	} else if json.Unmarshal([]byte(fingerprint), &reviewMark{}) != nil {
		return "", fmt.Errorf("fingerprint %q isn't one this list gave", fingerprint)
	}
	return fingerprint, s.store.SetAgentReview(ctx, state.AgentReview{
		Project: a.Project, Agent: a.Name, Verdict: api.ReviewReviewed, Fingerprint: fingerprint, At: time.Now(),
	})
}

// cancelMergeWhenGreen is DELETE /v1/projects/{project}/pulls/{number}/merge-when-green:
// a pending mark cancelled, its verdict taken back so the agent is in the
// review again, or a given-up one dismissed.
func (s *Server) cancelMergeWhenGreen(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, number, err := s.pullFromPath(r)
	if err != nil {
		return err
	}
	mark, ok, err := s.store.MergeWhenGreenOf(ctx, p.Name, number)
	if err != nil {
		return err
	}
	if ok {
		if err := s.store.ForgetMergeWhenGreen(ctx, p.Name, number); err != nil {
			return err
		}
		if mark.Pending() && mark.Agent != "" && mark.Fingerprint != "" {
			if err := s.store.TakeBackReview(ctx, p.Name, mark.Agent, mark.Fingerprint); err != nil {
				return err
			}
		}
		s.events.publish(api.EventReview, p.Name)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) pullFromPath(r *http.Request) (state.Project, int, error) {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return state.Project{}, 0, err
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		return state.Project{}, 0, fmt.Errorf("invalid pull request number %q", r.PathValue("number"))
	}
	return p, number, nil
}

// pendingMerges is what of a project waits to be merged when green, for the
// watch: none while the setting is off, so turning it off stops every merge
// it would make; the marks stay, and come back with it.
func (s *Server) pendingMerges(ctx context.Context, project string) []state.MergeWhenGreen {
	if on, err := s.store.Flag(ctx, state.SettingMergeWhenGreen); err != nil || !on {
		return nil
	}
	marks, err := s.store.MergesWhenGreen(ctx, project)
	if err != nil {
		s.logf("merge when green in %s: %v", project, err)
		return nil
	}
	out := marks[:0]
	for _, m := range marks {
		if m.Pending() {
			out = append(out, m)
		}
	}
	return out
}

// The limits past which a pull request that can't be merged is given up and
// goes back to the review: unmergeable (failing, conflicting or a draft) for
// mergeGiveUpAfter since first seen so (or refused by GitHub for now), or mergeFixRounds fixes pushed that
// came out red or conflicting again, whichever comes first.
var (
	mergeGiveUpAfter = 72 * time.Hour
	mergeFixRounds   = 5
)

// mergeGreen acts on the marks of a project's pull requests from what one
// look of the watch read of them. A mark stays armed through red checks,
// conflicts and new commits: the watch tells the agent to fix them
// (prBroken), and the first green, mergeable head is merged. It is given up,
// back to the review with why, only when the pull request was closed, GitHub
// refuses the merge for a reason no fix changes, nobody is left to fix it, or
// it stayed unmergeable past the limits. It answers whether one still waits on
// something GitHub is working out (checks running, mergeability unknown), for
// the watch's pace.
func (s *Server) mergeGreen(ctx context.Context, p state.Project, client github.Client, repo github.Repo, marks []state.MergeWhenGreen, read map[int]github.WatchedPR) (waiting bool) {
	now := s.prWatch.now()
	for _, m := range marks {
		pr, ok := read[m.Number]
		if !ok {
			continue // not read this time: the next look
		}
		wait := m.MergeWait
		wait.Detail = ""
		switch {
		case pr.State == "merged":
			// Merged by somebody: nothing left to do, and the watch or the
			// cleanup frees the agent as for any other.
			if err := s.store.ForgetMergeWhenGreen(ctx, p.Name, m.Number); err != nil {
				s.logf("merge when green in %s: %v", p.Name, err)
			}
			s.events.publish(api.EventReview, p.Name)
			continue
		case pr.State != "open":
			s.giveUpMerge(ctx, p, m, pr, "the pull request was closed")
			continue
		case pr.Draft:
			wait.Waiting = api.MergeWaitDraft
		case pr.Mergeable == "conflicting":
			wait.Waiting = api.MergeWaitConflict
			wait.Detail = pr.BaseBranch
		case pr.Checks == "failing":
			wait.Waiting = api.MergeWaitFailing
			wait.Detail = failingNames(pr)
		case pr.Checks == "pending" || pr.Mergeable == "unknown":
			waiting = true
			wait.Waiting = api.MergeWaitChecks
		default:
			_, err := s.mergePR(github.InBackground(ctx, "merge"), p, client, repo, m.Number, github.MergeMethod(m.Method))
			if err == nil {
				s.logf("%s: #%d merged (%s), its checks green", p.Name, m.Number, m.Method)
				s.events.publish(api.EventReview, p.Name)
				continue
			}
			why := strings.TrimSpace(err.Error())
			if !mergeRefusedForNow(err) {
				s.giveUpMerge(ctx, p, m, pr, "GitHub refused the merge: "+why)
				continue
			}
			s.logf("%s: #%d not merged yet, trying again: %s", p.Name, m.Number, why)
			wait.Waiting, wait.Detail = api.MergeWaitRetry, why
		}
		if why := s.mergeTrouble(ctx, p, m, pr, &wait, now); why != "" {
			s.giveUpMerge(ctx, p, m, pr, why)
			continue
		}
		if wait != m.MergeWait {
			if err := s.store.SetMergeWait(ctx, p.Name, m.Number, wait); err != nil {
				s.logf("merge when green in %s: %v", p.Name, err)
			}
			s.events.publish(api.EventReview, p.Name)
		}
	}
	return waiting
}

// mergeTrouble counts an unmergeable look against the limits, into wait, and
// answers why to give the mark up, or "" to keep waiting.
func (s *Server) mergeTrouble(ctx context.Context, p state.Project, m state.MergeWhenGreen, pr github.WatchedPR, wait *state.MergeWait, now time.Time) string {
	what := ""
	switch wait.Waiting {
	case api.MergeWaitFailing:
		what = "its checks failing"
	case api.MergeWaitConflict:
		what = "conflicting with " + pr.BaseBranch
	case api.MergeWaitDraft:
		what = "a draft"
	case api.MergeWaitRetry:
		what = "refused by GitHub (" + wait.Detail + ")"
	default:
		return ""
	}
	if wait.TroubleSince.IsZero() {
		wait.TroubleSince = now
	}
	// A red or conflicting head is the agent's to fix: one more of them is
	// one more round, and with its agent gone nobody is left to.
	if fix := wait.Waiting == api.MergeWaitFailing || wait.Waiting == api.MergeWaitConflict; fix {
		if pr.HeadSHA != wait.BadHead {
			wait.BadHeads++
			wait.BadHead = pr.HeadSHA
		}
		if !s.mergeFixable(ctx, p, m) {
			return fmt.Sprintf("its agent is gone, so nobody can fix it (%s)", what)
		}
	}
	if rounds := wait.BadHeads - 1; rounds >= mergeFixRounds {
		return fmt.Sprintf("still %s after %d fixes", what, rounds)
	}
	if now.Sub(wait.TroubleSince) >= mergeGiveUpAfter {
		return fmt.Sprintf("still %s after %s", what, humanHours(mergeGiveUpAfter))
	}
	return ""
}

// mergeFixable is whether the agent whose work the mark is still lives to be
// told to fix it.
func (s *Server) mergeFixable(ctx context.Context, p state.Project, m state.MergeWhenGreen) bool {
	if m.Agent == "" {
		return false
	}
	a, err := s.store.Agent(ctx, p.Name, m.Agent)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	return err != nil || a.Status != state.AgentDestroying // unread is not gone
}

// mergeRefusedForNow is whether GitHub's refusal of a merge is one the next
// look can get past: the branch moved under it, its mergeability or a
// required check isn't worked out yet, GitHub or the network failing, the
// rate limit. These still count against the time limit (mergeTrouble). A missing approving review, branch protection or rulesets, and
// no access are for good: no fix of the agent's changes them.
func mergeRefusedForNow(err error) bool {
	var limited *github.RateLimitError
	if errors.As(err, &limited) {
		return true
	}
	if errors.Is(err, github.ErrNoAccess) || errors.Is(err, github.ErrBadToken) {
		return false
	}
	var refused *github.StatusError
	if !errors.As(err, &refused) {
		return true // not GitHub's answer: the network, a timeout
	}
	if refused.Code == http.StatusConflict || refused.Code >= 500 {
		return true
	}
	msg := strings.ToLower(refused.Message)
	for _, now := range []string{"was modified", "not mergeable", "merge conflict", "status check", "in progress"} {
		if strings.Contains(msg, now) {
			return true
		}
	}
	return false
}

func humanHours(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		if n := int(d / (24 * time.Hour)); n != 1 {
			return fmt.Sprintf("%d days", n)
		}
		return "a day"
	}
	return d.String()
}

// giveUpMerge clears a mark that can't be merged, keeping why for the item,
// and takes its verdict back: the agent is in the review again, with the
// reason on it.
func (s *Server) giveUpMerge(ctx context.Context, p state.Project, m state.MergeWhenGreen, pr github.WatchedPR, why string) {
	failed, err := s.store.FailMergeWhenGreen(ctx, p.Name, m.Number, why, time.Now())
	if err != nil {
		s.logf("merge when green in %s: %v", p.Name, err)
		return
	}
	if !failed {
		return // cancelled meanwhile
	}
	if m.Agent != "" && m.Fingerprint != "" {
		if err := s.store.TakeBackReview(ctx, p.Name, m.Agent, m.Fingerprint); err != nil {
			s.logf("merge when green in %s: %v", p.Name, err)
		}
	}
	s.logf("%s: #%d not merged when green: %s", p.Name, m.Number, why)
	s.captureEvent(ctx, p.Name, m.Agent, "pr_merge_when_green_failed", map[string]any{
		"number": m.Number, "url": pr.URL, "reason": why,
	}, "")
	s.events.publish(api.EventReview, p.Name)
}

// mergeMarks is every mark of a project's, by pull request, for the review's
// items.
func (s *Server) mergeMarks(ctx context.Context, project string) map[int]state.MergeWhenGreen {
	marks, err := s.store.MergesWhenGreen(ctx, project)
	if err != nil || len(marks) == 0 {
		return nil
	}
	out := make(map[int]state.MergeWhenGreen, len(marks))
	for _, m := range marks {
		out[m.Number] = m
	}
	return out
}

// markItem puts its pull request's mark on a review item.
func markItem(item *api.ReviewItem, marks map[int]state.MergeWhenGreen) {
	if item.PR == nil {
		return
	}
	if m, ok := marks[item.PR.Number]; ok {
		item.MergeWhenGreen = mergeWhenGreenToAPI(m)
	}
}

func mergeWhenGreenToAPI(m state.MergeWhenGreen) *api.MergeWhenGreen {
	out := &api.MergeWhenGreen{Number: m.Number, Method: m.Method, At: m.At, Error: m.Error}
	if m.Pending() {
		out.Waiting, out.Detail, out.FixRounds = m.Waiting, m.Detail, max(m.BadHeads-1, 0)
	}
	if !m.FailedAt.IsZero() {
		at := m.FailedAt
		out.FailedAt = &at
	}
	return out
}
