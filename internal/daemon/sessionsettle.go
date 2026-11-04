package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A chat session is tied to what it made: its agents (Agent.CreatedBySession,
// and archived_agents.created_by_session once their machines are gone) and
// their pull requests, which the app shows above its chat (sessionOutput).
//
// Settling: a session closes into the history by itself, as the tab's close
// does (closeLeadSession), once its work is in: it has at least one pull
// request, every one of them merged or closed, none of its agents works, its
// own lead isn't busy, and nothing has happened in it for the project's
// SettleHours (a day unless the project says otherwise, or never). "Nothing
// has happened" counts its last message, when it was last reopened or woken,
// and its pull requests' last change, so a merge an hour ago gives it a day
// from then. A session that never had a pull request is left alone: a
// conversation that made nothing is not finished by having gone quiet. The
// app is told (EventSessionsSettled) and offers Undo, which is reopening it.
//
// Snoozing: an open session hidden from the tabs until a time of the user's
// choosing, when it comes back (WakeLeadSessions); sooner when one of its
// agents asks the chat something or needs a permission (wakeSessionOf), since
// that is the user's to see.

// sessionSweepInterval is how often the daemon wakes snoozed sessions whose
// time came, which is as precise as a snooze is; settling, counted in hours,
// is checked every settleEvery sweeps.
const (
	sessionSweepInterval = time.Minute
	settleEvery          = 5
)

// sweepSessions wakes snoozed sessions and settles finished ones, until ctx
// ends.
func (s *Server) sweepSessions(ctx context.Context) {
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			s.wakeSnoozed(ctx, now)
			if n%settleEvery == 0 {
				s.settleSessions(ctx, now)
			}
		}
	}
}

// wakeSnoozed brings back every snoozed session whose time came by now.
func (s *Server) wakeSnoozed(ctx context.Context, now time.Time) {
	woke, err := s.store.WakeLeadSessions(ctx, now)
	if err != nil {
		s.logf("waking snoozed sessions: %v", err)
		return
	}
	for project, ids := range woke {
		s.logf("%s's snoozed sessions %v are back", project, ids)
		s.sessionsChanged(ctx, project)
	}
}

// wakeSessionOf brings a's session back from a snooze, for why: one of its
// agents needs the user. Nothing when it has none or it isn't snoozed.
func (s *Server) wakeSessionOf(ctx context.Context, a state.Agent, why string) {
	id := a.Session()
	if id == "" || a.IsLead() {
		return
	}
	now := time.Now()
	ls, err := s.store.LeadSession(ctx, a.Project, id)
	if err != nil || ls.Closed() || !ls.Snoozed(now) {
		return
	}
	if err := s.store.SnoozeLeadSession(ctx, a.Project, id, time.Time{}, now); err != nil {
		s.logf("waking %s's %s session: %v", a.Project, id, err)
		return
	}
	s.logf("%s's %s session is back from its snooze: %s %s", a.Project, id, a.Name, why)
	s.sessionsChanged(ctx, a.Project)
}

// settleSessions settles, as of now, every open session of every project
// whose work is in, and tells the app which, a project at a time.
func (s *Server) settleSessions(ctx context.Context, now time.Time) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("settling sessions: %v", err)
		return
	}
	for _, p := range projects {
		settled := s.settleIn(ctx, p, now)
		if len(settled) == 0 {
			continue
		}
		s.sessionsChanged(ctx, p.Name)
		out := make([]api.LeadSession, 0, len(settled))
		for _, id := range settled {
			out = append(out, s.apiSession(ctx, p.Name, id))
		}
		s.events.publish(api.EventSessionsSettled, api.SessionsSettled{Project: p.Name, Sessions: out})
	}
}

// settleIn settles one project's finished sessions and returns their ids.
// The cheap facts are checked first, so that a pull request is only read
// for a session already idle long enough.
func (s *Server) settleIn(ctx context.Context, p state.Project, now time.Time) []string {
	after := p.SettleAfter()
	if after <= 0 {
		return nil
	}
	sessions, err := s.store.LeadSessions(ctx, p.Name)
	if err != nil {
		s.logf("settling %s's sessions: %v", p.Name, err)
		return nil
	}
	activity, err := s.store.LeadSessionActivity(ctx, p.Name)
	if err != nil {
		s.logf("settling %s's sessions: %v", p.Name, err)
		return nil
	}
	var settled []string
	for _, ls := range sessions {
		facts := settleFacts{IdleSince: latest(activity[ls.ID], ls.ReopenedAt), LeadBusy: s.leadBusy(agent.SessionRef(p.Name, ls.ID))}
		if !facts.mightSettle(after, now) {
			continue
		}
		out, known, err := s.sessionOutput(ctx, p, ls.ID)
		if err != nil {
			s.logf("settling %s's %s session: %v", p.Name, ls.ID, err)
			continue
		}
		facts.add(out, known)
		if !facts.settles(after, now) {
			continue
		}
		if err := s.closeLeadSession(ctx, p.Name, ls.ID, true); err != nil {
			s.logf("settling %s's %s session: %v", p.Name, ls.ID, err)
			continue
		}
		settled = append(settled, ls.ID)
	}
	return settled
}

// settleFacts is what settling a session goes by.
type settleFacts struct {
	// IdleSince is when anything last happened in it: its last message, its
	// last reopening or waking, its pull requests' last change.
	IdleSince time.Time
	// LeadBusy says its own conversation is running or waiting on the user.
	LeadBusy bool
	// Known says its pull requests have been read: false while GitHub
	// hasn't answered since the daemon started, or can't be asked.
	Known bool
	// PRs is the state of each of its agents' pull requests.
	PRs []string
	// Working says one of its agents is busy.
	Working bool
}

// add takes in what the session made.
func (f *settleFacts) add(out api.SessionOutput, known bool) {
	f.Known = known
	for _, a := range out.Agents {
		f.Working = f.Working || a.Working
		if a.PR == nil {
			continue
		}
		f.PRs = append(f.PRs, a.PR.State)
		if a.PR.UpdatedAt != nil {
			f.IdleSince = latest(f.IdleSince, *a.PR.UpdatedAt)
		}
	}
}

// mightSettle says the session could settle by now going by its own facts
// alone, before what it made is read: idle long enough and its lead quiet.
func (f settleFacts) mightSettle(after time.Duration, now time.Time) bool {
	return after > 0 && !f.LeadBusy && now.Sub(f.IdleSince) >= after
}

// settles is the rule: see the comment atop this file.
func (f settleFacts) settles(after time.Duration, now time.Time) bool {
	if !f.mightSettle(after, now) || !f.Known || f.Working || len(f.PRs) == 0 {
		return false
	}
	return !slices.ContainsFunc(f.PRs, func(st string) bool { return st != api.PRStateMerged && st != api.PRStateClosed })
}

func latest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

// leadBusy says a session's conversation is running, or waiting on the user
// (a permission it asked for).
func (s *Server) leadBusy(ref string) bool {
	return s.chatBusy(ref) || s.chat.State(ref) == api.ChatWaiting
}

// sessionOutput is what a session made: its live agents, whether each is
// working and its pull request, then its archived ones with theirs. known
// says the live agents' pull requests were read; without, an agent with none
// may yet have one.
func (s *Server) sessionOutput(ctx context.Context, p state.Project, id string) (api.SessionOutput, bool, error) {
	out := api.SessionOutput{Project: p.Name, Session: id, Agents: []api.SessionAgent{}}
	all, err := s.store.Agents(ctx, p.Name)
	if err != nil {
		return out, false, err
	}
	var live []state.Agent
	for _, a := range all {
		if !a.IsLead() && a.Session() == id {
			live = append(live, a)
		}
	}
	known := true
	if len(live) > 0 {
		waiting := map[string]bool{}
		questions, err := s.store.Questions(ctx, p.Name, true)
		if err != nil {
			return out, false, err
		}
		for _, q := range questions {
			if q.Waiting() {
				waiting[q.Agent] = true
			}
		}
		var prs map[string]*api.PullRequest
		prs, known = s.prsOf(p, live)
		for _, a := range live {
			working := waiting[a.Name] || s.chatBusy(a.Ref()) || s.chat.State(a.Ref()) == api.ChatWaiting
			if !working {
				running, _, _ := s.store.JobActivityFor(ctx, a.Ref())
				working = running
			}
			out.Agents = append(out.Agents, api.SessionAgent{
				Ref: a.Ref(), Name: a.Name, Title: a.Title, Branch: a.Branch, Working: working, PR: prs[a.Name],
			})
		}
	}
	archived, err := s.store.ArchivedOfSession(ctx, p.Name, id)
	if err != nil {
		return out, false, err
	}
	for _, a := range archived {
		row := api.SessionAgent{Ref: a.Ref(), Name: a.Name, Title: a.Title, Branch: a.Branch, Archived: true}
		if a.PR.Number != 0 {
			row.PR = &api.PullRequest{Number: a.PR.Number, Title: a.PR.Title, URL: a.PR.URL, State: a.PR.State}
		}
		out.Agents = append(out.Agents, row)
	}
	return out, known, nil
}

// prsOf is the live agents' pull requests by name, from the pull request
// cache without waiting on GitHub, and whether the cache has read them.
func (s *Server) prsOf(p state.Project, agents []state.Agent) (map[string]*api.PullRequest, bool) {
	if s.sessionPRs != nil {
		return s.sessionPRs(p, agents)
	}
	_, entry, heads, _, err := s.projectPulls(p, agents)
	if err != nil {
		return nil, false
	}
	return entry.byAgent(heads), !entry.at.IsZero()
}

// sessionOutputRoute answers GET /v1/projects/{project}/sessions/{session}/output.
func (s *Server) sessionOutputRoute(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	if _, err := s.store.LeadSession(r.Context(), project, id); err != nil {
		return err
	}
	p, err := s.store.Project(r.Context(), project)
	if errors.Is(err, state.ErrNotFound) && project == state.HomeProject {
		// The Home chat's sessions make agents in other projects, which
		// created_by_session doesn't follow: nothing to show.
		return writeJSON(w, http.StatusOK, api.SessionOutput{Project: project, Session: id, Agents: []api.SessionAgent{}})
	}
	if err != nil {
		return err
	}
	out, _, err := s.sessionOutput(r.Context(), p, id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// snoozeSession answers POST /v1/projects/{project}/sessions/{session}/snooze:
// the session hides from the tabs until the time given, or with none comes
// back now.
func (s *Server) snoozeSession(w http.ResponseWriter, r *http.Request) error {
	project, id := r.PathValue("project"), r.PathValue("session")
	var req api.LeadSessionSnooze
	if err := readJSON(r, &req); err != nil {
		return err
	}
	now := time.Now()
	var until time.Time
	if req.Until != nil {
		until = *req.Until
		if !until.After(now) {
			return (fmt.Errorf("snooze until %s: that has passed", until.Format(time.RFC3339)))
		}
	}
	if err := s.store.SnoozeLeadSession(r.Context(), project, id, until, now); err != nil {
		return err
	}
	if until.IsZero() {
		s.logf("%s's %s session is back from its snooze", project, id)
	} else {
		s.logf("Snoozed %s's %s session until %s", project, id, until.Format(time.RFC3339))
	}
	s.sessionsChanged(r.Context(), project)
	return writeJSON(w, http.StatusOK, s.apiSession(r.Context(), project, id))
}
