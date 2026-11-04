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
// last read and measured (git add --all, diff, status, over virtiofs) only
// when nothing was read yet, when its HEAD, index or branch moved since
// (gitrepo.WorktreeStamp), or when it ran since it was last read, so what it
// did before stopping is counted once. Every project's fleet is asked for
// whenever the app is focused; stopped agents were most of the git it ran.
func polledChangesOf(statuses []agent.Status) func(state.Agent, ...string) api.AgentChanges {
	idle := make(map[string]bool, len(statuses))
	for _, st := range statuses {
		if st.State == "stopped" || st.State == "paused" {
			idle[st.Name] = true
		}
	}
	return func(a state.Agent, onto ...string) api.AgentChanges {
		if a.Worktree == "" {
			return polledChanges(a, onto...)
		}
		if !idle[a.Name] {
			ranSince.Store(a.Worktree, true)
			return polledChanges(a, onto...)
		}
		stampOf := func() string {
			return gitrepo.WorktreeStamp(a.Worktree) + "|" + a.BaseCommit + "|" + strings.Join(onto, ",")
		}
		if last, ok := knownChanges.Load(a.Worktree); ok {
			if at, ok := measuredAt.Load(a.Worktree); ok && at == stampOf() {
				if _, ran := ranSince.Load(a.Worktree); !ran {
					return last.(api.AgentChanges)
				}
			}
		}
		out := polledChanges(a, onto...)
		// Only a read that stuck (knownChanges holds it) is worth keeping. The
		// stamp is taken after: git status refreshes the index it reads.
		if last, ok := knownChanges.Load(a.Worktree); ok && last.(api.AgentChanges) == out {
			if stamp := stampOf(); !strings.HasPrefix(stamp, "|") {
				measuredAt.Store(a.Worktree, stamp)
				ranSince.Delete(a.Worktree)
			}
		}
		return out
	}
}

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
	var out api.AgentChanges
	if a.Worktree == "" || a.BaseCommit == "" {
		return out, nil
	}
	if _, err := os.Stat(a.Worktree); errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	stat, err := gitrepo.DiffWorktree(a.Worktree, gitrepo.DiffBase(a.Worktree, a.BaseRef, a.BaseCommit, onto...), true)
	if err != nil {
		return out, err
	}
	out = statOf(stat)
	dirty, err := gitrepo.Dirty(a.Worktree)
	if err != nil {
		return api.AgentChanges{}, err
	}
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
