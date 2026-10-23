package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// "Remove stopped agents' machines" (off by default) removes the machine and
// worktree of an agent that has been stopped for the setting's days, keeping
// it as a past agent with its branch, pull request and chat, as retiring it
// with destroy does. A stopped agent holds no memory but a whole machine of
// disk, 5–12 GB each on btrfs, and the app hides the ones whose chat
// finished, so they pile up unseen.
//
// The cleanup pass (cleanup.go) already removes finished agents whose pull
// request was merged; this one is for what it leaves: anything stopped long
// enough, when nothing would be lost. What decides it is stoppedVerdict. As
// there, a read that fails is unknown, never "clean" or "nothing" (#463): the
// agent is kept. Every removal reads everything again, that agent alone, right
// before destroying.

// removeStoppedInterval is how often the daemon looks for stopped machines to
// remove, when the setting is on. Its wait is counted in days.
const removeStoppedInterval = time.Hour

// sweepStoppedMachines periodically removes the machines stoppedVerdict says
// can go, while the setting is on.
func (s *Server) sweepStoppedMachines(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(cleanupDelay):
	}
	s.removeStoppedMachines(ctx, time.Now())
	ticker := time.NewTicker(removeStoppedInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.removeStoppedMachines(ctx, time.Now())
		}
	}
}

// removeStoppedMachines does one pass over every project, as of now.
func (s *Server) removeStoppedMachines(ctx context.Context, now time.Time) {
	on, days, err := s.store.RemoveStopped(ctx)
	if err != nil {
		s.logf("remove stopped machines: %v", err)
		return
	}
	// Judging an agent asks git about its worktree, and removing one walks
	// it: not while the shared home is short of files (share.go).
	if !on || s.shareBusy() {
		return
	}
	machines, _, err := s.stoppedMachines(ctx, "", true, now, days)
	if err != nil {
		s.logf("remove stopped machines: %v", err)
		return
	}
	var refs []string
	for _, sm := range machines {
		if sm.Remove != "" {
			refs = append(refs, sm.Ref)
		}
	}
	if len(refs) == 0 {
		return
	}
	res := s.removeStopped(ctx, refs, now, days)
	for _, k := range res.Kept {
		s.logf("not removing %s's stopped machine: %s", k.Ref, k.Keep)
	}
}

// stoppedFacts is what stoppedVerdict decides from.
type stoppedFacts struct {
	// Ready is an agent that is neither being created nor destroyed.
	Ready bool
	// State is its machine's: running, stopped, paused...
	State string
	// Busy is a chat that is running, starting or waiting on somebody.
	Busy         bool
	StoppedSince time.Time
	// Dirty is uncommitted or untracked work in its worktree.
	Dirty bool
	// Unpushed is a branch with commits that are neither merged, nor on a
	// remote, nor in its pull request's head.
	Unpushed bool
	// Changed is a diff against the commit the agent started from.
	Changed   bool
	PR        *api.PullRequest
	CreatedAt time.Time
}

// stoppedVerdict says why a stopped agent's machine can be removed now, or,
// when it can't, why it is kept. Exactly one of the two is set.
func stoppedVerdict(f stoppedFacts, now time.Time, days int) (remove, keep string) {
	switch {
	case !f.Ready:
		return "", "it is being created or removed"
	case f.State != "stopped":
		return "", "its machine is " + f.State
	case f.Busy:
		return "", "its chat is busy"
	case now.Sub(f.StoppedSince) < time.Duration(days)*24*time.Hour:
		return "", fmt.Sprintf("stopped for less than %d days", days)
	case f.Dirty:
		return "", "it has uncommitted or untracked changes"
	case f.Unpushed:
		return "", "it has commits that aren't pushed"
	}
	if pr := f.PR; pr != nil {
		// A pull request last touched before this agent existed can't be one
		// this agent made, whatever it carries.
		if pr.UpdatedAt != nil && pr.UpdatedAt.Before(f.CreatedAt) {
			return "", fmt.Sprintf("pull request #%d is older than the agent", pr.Number)
		}
		switch pr.State {
		case "merged", "closed":
			return fmt.Sprintf("its pull request #%d was %s", pr.Number, pr.State), ""
		}
		return "", fmt.Sprintf("its pull request #%d is open", pr.Number)
	}
	if f.Changed {
		return "", "it has changes and no pull request"
	}
	return "it changed nothing", ""
}

// stoppedSince is when st's machine stopped: when AgentBox stopped it, or,
// when it stopped some other way (the VM shut down, or before AgentBox kept
// count), the last thing known to have happened on it — its start, its
// pause, its chat. A machine can do nothing while stopped, so the latest of
// these is never later than the stop; a StoppedAt older than the last start
// is a stop since undone, and the start wins.
func (s *Server) stoppedSince(ctx context.Context, st agent.Status) (time.Time, error) {
	since := st.CreatedAt
	for _, t := range []time.Time{st.StartedAt, st.PausedAt, st.StoppedAt} {
		if t.After(since) {
			since = t
		}
	}
	item, ok, err := s.store.LastChatItem(ctx, st.Project, st.Name)
	if err != nil {
		return time.Time{}, fmt.Errorf("its chat can't be read: %w", err)
	}
	if ok {
		var stored struct {
			UpdatedAt time.Time `json:"updatedAt"`
		}
		if json.Unmarshal(item.Data, &stored) == nil && stored.UpdatedAt.After(since) {
			since = stored.UpdatedAt
		}
	}
	return since, nil
}

// stoppedJudge reads what a project's agents share — its pull requests and
// its repository — once, for the facts of each of them.
type stoppedJudge struct {
	s       *Server
	prs     map[string]*api.PullRequest
	unread  map[string]bool
	prErr   error
	repo    gitrepo.Repo
	repoErr error
}

// newStoppedJudge reads p's pull requests for agents, its agents with a
// branch, which is what the cache matches pull requests against.
func (s *Server) newStoppedJudge(p state.Project, agents []state.Agent) *stoppedJudge {
	j := &stoppedJudge{s: s}
	j.prs, j.unread, j.prErr = s.knownPulls(p, agents)
	j.repo, j.repoErr = gitrepo.Open(p.Root)
	return j
}

// facts reads stoppedVerdict's facts for st. Any read that fails is an
// error, never a "no": nothing uncommitted, nothing unpushed and no changes
// are each half of a reason to remove.
func (j *stoppedJudge) facts(ctx context.Context, st agent.Status) (stoppedFacts, error) {
	switch {
	case j.prErr != nil:
		return stoppedFacts{}, fmt.Errorf("its project's pull requests can't be read: %w", j.prErr)
	case j.repoErr != nil:
		return stoppedFacts{}, fmt.Errorf("its repository can't be read: %w", j.repoErr)
	case j.unread[st.Name]:
		return stoppedFacts{}, errors.New("its branch can't be read")
	case st.BaseCommit == "":
		return stoppedFacts{}, errors.New("where it started isn't known")
	}
	since, err := j.s.stoppedSince(ctx, st)
	if err != nil {
		return stoppedFacts{}, err
	}
	// measureChanges takes a missing worktree for no changes; here it is
	// unknown, like one that can't be looked at.
	if _, err := os.Stat(st.Worktree); err != nil {
		return stoppedFacts{}, fmt.Errorf("its worktree can't be read: %w", err)
	}
	changes, err := measureChanges(st.Agent)
	if err != nil {
		return stoppedFacts{}, fmt.Errorf("its changes can't be read: %w", err)
	}
	pr := j.prs[st.Name]
	out, err := unpushed(j.repo, st.Agent, pr)
	if err != nil {
		return stoppedFacts{}, fmt.Errorf("its branch can't be read: %w", err)
	}
	return stoppedFacts{
		Ready:        st.Status == state.AgentReady,
		State:        st.State,
		Busy:         j.s.chatBusy(st.Ref()),
		StoppedSince: since,
		Dirty:        changes.Dirty,
		Unpushed:     out,
		Changed:      changes.Files > 0,
		PR:           pr,
		CreatedAt:    st.CreatedAt,
	}, nil
}

// verdict is stoppedVerdict for st from fresh facts, an unreadable fact
// keeping it.
func (j *stoppedJudge) verdict(ctx context.Context, st agent.Status, now time.Time, days int) (f stoppedFacts, remove, keep string) {
	f, err := j.facts(ctx, st)
	if err != nil {
		return f, "", err.Error()
	}
	remove, keep = stoppedVerdict(f, now, days)
	return f, remove, keep
}

// stoppedMachines lists the agents holding a stopped machine, in project or
// every project when it is "", oldest stop first. check adds each one's
// verdict, which reads git and the pull requests; pending says some were
// kept only because a project's pull requests haven't been read yet.
func (s *Server) stoppedMachines(ctx context.Context, project string, check bool, now time.Time, days int) (out []api.StoppedMachine, pending bool, err error) {
	var projects []state.Project
	if project != "" {
		p, err := s.store.Project(ctx, project)
		if err != nil {
			return nil, false, err
		}
		projects = []state.Project{p}
	} else if projects, err = s.store.Projects(ctx); err != nil {
		return nil, false, err
	}
	m := s.manager(s.cfg.Log)
	out = []api.StoppedMachine{}
	for _, p := range projects {
		statuses, err := m.List(ctx, p.Name)
		if err != nil {
			return nil, false, err
		}
		var stopped []agent.Status
		var branched []state.Agent
		for _, st := range statuses {
			if st.IsLead() {
				continue
			}
			if st.Status == state.AgentReady && st.Branch != "" {
				branched = append(branched, st.Agent)
			}
			if st.State == "stopped" {
				stopped = append(stopped, st)
			}
		}
		if len(stopped) == 0 {
			continue
		}
		var judge *stoppedJudge
		if check {
			judge = s.newStoppedJudge(p, branched)
			pending = pending || errors.Is(judge.prErr, errPullsUnread)
		}
		for _, st := range stopped {
			sm := api.StoppedMachine{Ref: st.Ref(), Project: st.Project, Name: st.Name, Title: st.Title, Branch: st.Branch}
			since, err := s.stoppedSince(ctx, st)
			if err == nil {
				sm.StoppedSince = since.UTC()
			}
			if n, err := m.MachineDisk(ctx, st.Agent); err == nil {
				sm.Machine = &n
			}
			if judge != nil {
				_, sm.Remove, sm.Keep = judge.verdict(ctx, st, now, days)
			}
			out = append(out, sm)
		}
	}
	slices.SortStableFunc(out, func(a, b api.StoppedMachine) int { return a.StoppedSince.Compare(b.StoppedSince) })
	return out, pending, nil
}

// removeStopped removes the machines of the agents refs names, each only if,
// read again from scratch right now, stoppedVerdict still says it can go.
func (s *Server) removeStopped(ctx context.Context, refs []string, now time.Time, days int) api.RemoveStoppedResult {
	s.removingStopped.Lock()
	defer s.removingStopped.Unlock()
	res := api.RemoveStoppedResult{Removed: []string{}, Kept: []api.StoppedMachine{}}
	keep := func(ref, why string) {
		project, name, _ := agent.ParseRef(ref)
		res.Kept = append(res.Kept, api.StoppedMachine{Ref: ref, Project: project, Name: name, Keep: why})
	}
	m := s.manager(s.cfg.Log)
	for _, ref := range refs {
		if s.shareBusy() {
			keep(ref, "the shared home is short of files; try again later")
			continue
		}
		st, j, err := s.freshStopped(ctx, m, ref)
		if err != nil {
			keep(ref, err.Error())
			continue
		}
		f, remove, why := j.verdict(ctx, st, now, days)
		if remove == "" {
			keep(ref, why)
			continue
		}
		s.logf("removing %s's machine: stopped since %s and %s", ref, f.StoppedSince.Format(time.DateOnly), remove)
		// Not Force: Destroy reads the worktree's status once more itself,
		// and refuses one it can't read.
		if err := s.destroyAgentNow(ctx, m, st.Agent, agent.DestroyOptions{PR: archivedPR(f.PR)}); err != nil {
			s.logf("removing %s's machine: %v", ref, err)
			keep(ref, "removing it failed: "+err.Error())
			continue
		}
		s.captureEvent(ctx, st.Project, st.Name, "agent_retired", map[string]any{"how": "auto", "branch": st.Branch, "reason": "stopped since " + f.StoppedSince.Format(time.DateOnly) + " and " + remove}, "")
		res.Removed = append(res.Removed, ref)
	}
	if len(res.Removed) > 0 {
		s.refreshAgents(ctx)
	}
	return res
}

// freshStopped reads ref's agent and its machine again, and a judge over its
// project as it is now.
func (s *Server) freshStopped(ctx context.Context, m *agent.Manager, ref string) (agent.Status, *stoppedJudge, error) {
	project, name, err := agent.ParseRef(ref)
	if err != nil {
		return agent.Status{}, nil, err
	}
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return agent.Status{}, nil, fmt.Errorf("its project can't be read: %w", err)
	}
	statuses, err := m.List(ctx, project)
	if err != nil {
		return agent.Status{}, nil, fmt.Errorf("its machine can't be read: %w", err)
	}
	var branched []state.Agent
	var found *agent.Status
	for i, st := range statuses {
		if st.IsLead() {
			continue
		}
		if st.Status == state.AgentReady && st.Branch != "" {
			branched = append(branched, st.Agent)
		}
		if st.Name == name {
			found = &statuses[i]
		}
	}
	if found == nil {
		return agent.Status{}, nil, errors.New("it is gone")
	}
	return *found, s.newStoppedJudge(p, branched), nil
}

// getStoppedMachines serves GET /v1/stopped-machines: ?project= narrows it to
// one project, and ?check=1 adds each agent's verdict under the setting's
// days, whether or not the setting is on, so the user can see what it would
// do before turning it on.
func (s *Server) getStoppedMachines(w http.ResponseWriter, r *http.Request) error {
	_, days, err := s.store.RemoveStopped(r.Context())
	if err != nil {
		return err
	}
	check := r.URL.Query().Get("check") == "1"
	if check && s.shareBusy() {
		return errors.New("the shared home is short of files right now; try again in a minute")
	}
	project := r.URL.Query().Get("project")
	machines, pending, err := s.stoppedMachines(r.Context(), project, check, time.Now(), days)
	// The pull request cache may never have been asked yet, and the read it
	// starts takes a few seconds: wait for it a while, rather than answer
	// that every agent is kept for that.
	for tries := 0; err == nil && pending && tries < 20; tries++ {
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		case <-time.After(500 * time.Millisecond):
		}
		machines, pending, err = s.stoppedMachines(r.Context(), project, check, time.Now(), days)
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.StoppedMachines{Days: days, Machines: machines})
}

// postRemoveStopped serves POST /v1/stopped-machines/remove: "Remove these
// now", for the agents the user was shown.
func (s *Server) postRemoveStopped(w http.ResponseWriter, r *http.Request) error {
	var req api.RemoveStoppedRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	_, days, err := s.store.RemoveStopped(r.Context())
	if err != nil {
		return err
	}
	res := s.removeStopped(context.WithoutCancel(r.Context()), slices.Compact(slices.Sorted(slices.Values(req.Agents))), time.Now(), days)
	for _, k := range res.Kept {
		s.logf("not removing %s's stopped machine: %s", k.Ref, cmp.Or(k.Keep, "kept"))
	}
	return writeJSON(w, http.StatusOK, res)
}
