package daemon

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// The fleet: a project's agents in one answer, so you can follow what each one
// is doing without opening it. It folds together what the daemon already knows
// (state, chat, media) with two things it has to work out: the size of the
// agent's diff, and where its branch stands on GitHub.
//
// Neither of those is allowed to hold the answer up. Pull requests come from
// the shared per-repository cache in pulls.go, matched to agents' commits rather than
// asked for one agent at a time, and the diffs are measured concurrently
// (D54).

// changesConcurrency bounds how many agents' diffs are measured at once. Each
// one is two git processes, so a large fleet shouldn't fork one per agent at
// the same moment.
const changesConcurrency = 8

// fleet answers with every agent of a project, and the create jobs still running.
func (s *Server) fleet(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	project := r.PathValue("project")
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return err
	}
	statuses, err := s.manager(nil).List(ctx, project)
	if err != nil {
		return err
	}
	// The project's chat is not one of its agents, so it never appears here.
	statuses = slices.DeleteFunc(statuses, func(st agent.Status) bool { return st.IsLead() })

	account, _ := s.githubAccountFor(p)
	out := api.Fleet{Project: project, GitHubAccount: account, Agents: []api.FleetAgent{}, Creating: []api.Job{}}

	agents := make([]state.Agent, len(statuses))
	for i, st := range statuses {
		agents[i] = st.Agent
	}
	repo, entry, heads, refreshing, err := s.projectPulls(p, agents)
	var prs map[string]*api.PullRequest
	if err == nil {
		out.GitHub = repo.String()
		out.GitHubError = s.githubErrorFrom(p, repo, entry.listErr)
		out.PullsRefreshing = refreshing
		if at := entry.readAt(); !at.IsZero() {
			out.PullsFetchedAt = &at
		}
		prs = entry.byAgent(heads)
	} else {
		out.GitHubError = s.githubErrorFor(p, repo, err)
	}

	changes := agentChanges(agents, prs, polledChangesOf(statuses))
	topics := map[string]string{}
	if sessions, err := s.store.LeadSessions(ctx, project); err == nil {
		for _, ls := range sessions {
			topics[ls.ID] = ls.Topic
		}
	}

	for i, st := range statuses {
		row := api.FleetAgent{Agent: s.agentInfo(st)}
		row.SessionTopic = topics[row.LeadSession]
		row.Changes = changes[i]
		if items, err := s.store.Media(ctx, st.Project, st.Name); err == nil {
			row.Media = len(items)
		}
		row.PR = prs[st.Name]
		s.prWatch.overlay(project, row.PR)
		row.Busy, row.Idle, row.LastActive, row.Retire = s.idleOf(ctx, st, row.Changes)
		if row.Idle {
			out.Idle++
		}
		out.Agents = append(out.Agents, row)
	}

	// An agent still being made has no row worth showing yet; its job does, so
	// you can watch it appear.
	if jobs, err := s.jobs.list(ctx, 30); err == nil {
		for _, j := range jobs {
			if j.Kind == "create" && j.Status == api.JobRunning && j.Target == project {
				out.Creating = append(out.Creating, j)
			}
		}
	}
	out.Remote = s.remoteAgents(ctx, project)
	return writeJSON(w, http.StatusOK, out)
}

// agentChanges measures every agent's diff at once. Each one is a git process
// or two against a different worktree, so they run concurrently: a project
// with fifteen agents took as long as fifteen diffs in a row before, on every
// poll of a tab that polls. Each is measured onto its pull request's base,
// from prs, when it has one.
func agentChanges(agents []state.Agent, prs map[string]*api.PullRequest, measure func(state.Agent, ...string) api.AgentChanges) []api.AgentChanges {
	out := make([]api.AgentChanges, len(agents))
	var g errgroup.Group
	g.SetLimit(changesConcurrency)
	for i, a := range agents {
		g.Go(func() error {
			out[i] = measure(a, prBase(prs[a.Name])...)
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// prBase is the branch an agent's pull request would merge into, which its
// changes are measured onto as GitHub measures the pull request's, or none
// without one (gitrepo.DiffBase).
func prBase(pr *api.PullRequest) []string {
	if pr == nil || pr.BaseBranch == "" {
		return nil
	}
	return []string{pr.BaseBranch}
}

// cachedPR is a's pull request as the project's cache has it, without
// waiting on GitHub, or nil.
func (s *Server) cachedPR(ctx context.Context, a state.Agent) *api.PullRequest {
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return nil
	}
	_, entry, heads, _, err := s.projectPulls(p, []state.Agent{a})
	if err != nil {
		return nil
	}
	return entry.byAgent(heads)[a.Name]
}

// changesOf is an agent's diff against its base (gitrepo.DiffBase), onto its
// pull request's base branch when given one (prBase), for showing: when git
// can't read it now, the last it could, so a fleet doesn't flash every agent
// to "no changes" while the machine is out of files. What decides anything
// from the changes uses measureChanges, and its error.
func changesOf(a state.Agent, onto ...string) api.AgentChanges {
	out, err := measureChanges(a, onto...)
	if err != nil {
		if last, ok := knownChanges.Load(a.Worktree); ok {
			return last.(api.AgentChanges)
		}
		return out
	}
	if a.Worktree != "" {
		knownChanges.Store(a.Worktree, out)
	}
	return out
}

// polledChanges is changesOf for a list a tab polls. A worktree already being
// measured, by a poll whose git hasn't returned yet, isn't measured again
// alongside it but shown as last read: polls stacking up on a slow disk kept
// git running nonstop.
func polledChanges(a state.Agent, onto ...string) api.AgentChanges {
	if a.Worktree != "" {
		if _, busy := measuring.LoadOrStore(a.Worktree, true); busy {
			if last, ok := knownChanges.Load(a.Worktree); ok {
				return last.(api.AgentChanges)
			}
		} else {
			defer measuring.Delete(a.Worktree)
		}
	}
	return changesOf(a, onto...)
}

// polledChangesOf is polledChanges for a list of statuses: an agent whose
// machine isn't running can't have changed its worktree, so it is shown as
// last read, and measured (stoppedChanges) only when nothing was read yet,
// when its HEAD, index or branch moved since, or when it ran since it was
// last read. Every project's fleet is asked for whenever the app is
// focused; stopped agents were most of the git it ran.
func polledChangesOf(statuses []agent.Status) func(state.Agent, ...string) api.AgentChanges {
	idle := make(map[string]agent.Status, len(statuses))
	for _, st := range statuses {
		if machineIdle(st) {
			idle[st.Name] = st
		}
	}
	return func(a state.Agent, onto ...string) api.AgentChanges {
		if a.Worktree == "" {
			return polledChanges(a, onto...)
		}
		st, ok := idle[a.Name]
		if !ok {
			ranSince.Store(a.Worktree, true)
			return polledChanges(a, onto...)
		}
		if last, ok := idleCached(a, st.StartedAt, onto); ok {
			return last
		}
		if _, busy := measuring.LoadOrStore(a.Worktree, true); busy {
			if last, ok := knownChanges.Load(a.Worktree); ok {
				return last.(api.AgentChanges)
			}
		} else {
			defer measuring.Delete(a.Worktree)
		}
		out, err := stoppedChanges(a, st.StartedAt, onto...)
		if err != nil {
			if last, ok := knownChanges.Load(a.Worktree); ok {
				return last.(api.AgentChanges)
			}
		}
		return out
	}
}

// machineIdle reports whether st's machine can't be changing its worktree:
// stopped, or paused (frozen).
func machineIdle(st agent.Status) bool { return st.State == "stopped" || st.State == "paused" }

// changesFor is measureChanges for a sweep over a project's agents: a
// stopped or paused agent's changes come from stoppedChanges, read again
// only when its worktree moved or it ran since; a running one's are read,
// and it is marked as having run.
func changesFor(st agent.Status) (api.AgentChanges, error) {
	if !machineIdle(st) || st.Worktree == "" {
		if st.Worktree != "" {
			ranSince.Store(st.Worktree, true)
		}
		return measureChanges(st.Agent)
	}
	return stoppedChanges(st.Agent, st.StartedAt)
}

// freshChanges is measureChanges for st read now, whatever was kept: for a
// check right before removing it. A stopped one still waits its turn among
// idleMeasureSlots, at the lowest priority.
func freshChanges(st agent.Status) (api.AgentChanges, error) {
	if machineIdle(st) {
		return measureIdleChanges(st.Agent)
	}
	return measureChanges(st.Agent)
}

// Stopped agents' worktrees are measured once after they stop and then only
// when they change. On 2026-11-06 the user's PC froze while `git add --all`
// ran in every stopped worktree of a project at the same moment: the cleanup
// pass (every 5 minutes) and the stopped-machines pass (hourly) each measured
// every agent from scratch, every time, the stopped ones included. Now:
//
//   - a worktree's measurement is kept with its stamp (gitrepo.WorktreeStamp:
//     its HEAD, index and branch, with its base, onto and when its machine
//     last started), and read again only when that moved or it was seen
//     running since;
//   - at most idleMeasureSlots are measured at once, whoever asks, and their
//     git runs at nice 19 and the lowest best-effort I/O priority
//     (gitrepo.MeasureWorktree).
//
// What decides to remove an agent reads its changes fresh right before
// doing it (cleanupFactsOf's fresh, the stopped judge's), still bounded.

// idleMeasureSlots is how many idle worktrees are measured at once.
const idleMeasureSlots = 2

var idleMeasures = make(chan struct{}, idleMeasureSlots)

// idleStamp is what an idle worktree's measurement is kept against; "" when
// it can't be read.
func idleStamp(a state.Agent, started time.Time, onto []string) string {
	w := gitrepo.WorktreeStamp(a.Worktree)
	if w == "" {
		return ""
	}
	return w + "|" + a.BaseCommit + "|" + strings.Join(onto, ",") + "|" + strconv.FormatInt(started.UnixNano(), 10)
}

// idleCached is the measurement kept for an idle worktree, when nothing
// moved since it was taken.
func idleCached(a state.Agent, started time.Time, onto []string) (api.AgentChanges, bool) {
	last, ok := knownChanges.Load(a.Worktree)
	if !ok {
		return api.AgentChanges{}, false
	}
	if _, ran := ranSince.Load(a.Worktree); ran {
		return api.AgentChanges{}, false
	}
	at, ok := measuredAt.Load(a.Worktree)
	if !ok || at != idleStamp(a, started, onto) {
		return api.AgentChanges{}, false
	}
	return last.(api.AgentChanges), true
}

// stoppedChanges is measureChanges for an agent whose machine isn't
// running (started being when it last started): the kept measurement when
// nothing moved, else measured in the background and kept.
func stoppedChanges(a state.Agent, started time.Time, onto ...string) (api.AgentChanges, error) {
	if a.Worktree == "" || a.BaseCommit == "" {
		return api.AgentChanges{}, nil
	}
	if last, ok := idleCached(a, started, onto); ok {
		return last, nil
	}
	out, err := measureIdleChanges(a, onto...)
	if err != nil {
		return out, err
	}
	knownChanges.Store(a.Worktree, out)
	// Taken after: git status refreshes the index it reads.
	if stamp := idleStamp(a, started, onto); stamp != "" {
		measuredAt.Store(a.Worktree, stamp)
		ranSince.Delete(a.Worktree)
	}
	return out, nil
}

// measureIdleChanges is measureChanges in the background: one of
// idleMeasureSlots, its git at the lowest priority.
func measureIdleChanges(a state.Agent, onto ...string) (api.AgentChanges, error) {
	idleMeasures <- struct{}{}
	defer func() { <-idleMeasures }()
	return measureWith(a, onto, measureIdleWorktree)
}

// measureIdleWorktree is gitrepo.MeasureWorktree, or a test's.
var measureIdleWorktree = gitrepo.MeasureWorktree

// measuredAt is the gitrepo.WorktreeStamp (with base and onto) of each
// worktree's knownChanges, and ranSince the worktrees seen running since.
var (
	measuredAt sync.Map // worktree -> string
	ranSince   sync.Map // worktree -> true
)

// knownChanges is the last changes measureChanges read for each worktree.
var knownChanges sync.Map // worktree -> api.AgentChanges

// measuring is the worktrees polledChanges is measuring now.
var measuring sync.Map // worktree -> true

// measureChanges measures an agent's diff against its base, onto the
// branches in onto when given (gitrepo.DiffBase), and whether it has
// uncommitted work. A worktree that has been removed, or an agent still
// being created, has none. An error is git failing to read it (ENFILE, say),
// and the changes are then unknown: never "none", and never "clean".
func measureChanges(a state.Agent, onto ...string) (api.AgentChanges, error) {
	return measureWith(a, onto, func(worktree, base string) (string, bool, error) {
		stat, err := gitrepo.DiffWorktree(worktree, base, true)
		if err != nil {
			return "", false, err
		}
		dirty, err := gitrepo.Dirty(worktree)
		return stat, dirty, err
	})
}

// measureWith is measureChanges with measure reading the diff's --stat and
// whether the worktree is dirty.
func measureWith(a state.Agent, onto []string, measure func(worktree, base string) (string, bool, error)) (api.AgentChanges, error) {
	var out api.AgentChanges
	if a.Worktree == "" || a.BaseCommit == "" {
		return out, nil
	}
	if _, err := os.Stat(a.Worktree); errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	stat, dirty, err := measure(a.Worktree, gitrepo.DiffBase(a.Worktree, a.BaseRef, a.BaseCommit, onto...))
	if err != nil {
		return api.AgentChanges{}, err
	}
	out = statOf(stat)
	out.Dirty = dirty
	return out, nil
}

// projectMedia is every agent's media for one project, newest first, each item
// labelled with the agent it came from so the app can filter without a lookup.
// ?limit= pages it (media_list.go).
func (s *Server) projectMedia(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	return s.writeMediaList(w, r, state.MediaFilter{Project: project}, true)
}

// projectMediaCounts is the counts on a project's Media filters.
func (s *Server) projectMediaCounts(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	return s.writeMediaCounts(w, r, state.MediaFilter{Project: project})
}

func (s *Server) agentTitles(ctx context.Context, project string) (map[string]string, error) {
	agents, err := s.store.Agents(ctx, project)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]string, len(agents))
	for _, a := range agents {
		titles[a.Name] = a.Title
	}
	return titles, nil
}

// statOf reads git's diff --stat or --shortstat, whose last line is
// " N files changed, N insertions(+), N deletions(-)".
func statOf(stat string) api.AgentChanges {
	var out api.AgentChanges
	lines := strings.Split(strings.TrimSpace(stat), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return out
	}
	for _, part := range strings.Split(lines[len(lines)-1], ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(fields[1], "file"):
			out.Files = n
		case strings.HasPrefix(fields[1], "insertion"):
			out.Insertions = n
		case strings.HasPrefix(fields[1], "deletion"):
			out.Deletions = n
		}
	}
	return out
}
