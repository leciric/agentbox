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
		if !entry.at.IsZero() {
			at := entry.at
			out.PullsFetchedAt = &at
		}
		prs = entry.byAgent(heads)
	} else {
		out.GitHubError = s.githubErrorFor(p, repo, err)
	}

	changes := agentChanges(agents, polledChanges)

	for i, st := range statuses {
		row := api.FleetAgent{Agent: s.agentInfo(st)}
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
	return writeJSON(w, http.StatusOK, out)
}

// agentChanges measures every agent's diff at once. Each one is a git process
// or two against a different worktree, so they run concurrently: a project
// with fifteen agents took as long as fifteen diffs in a row before, on every
// poll of a tab that polls.
func agentChanges(agents []state.Agent, measure func(state.Agent) api.AgentChanges) []api.AgentChanges {
	out := make([]api.AgentChanges, len(agents))
	var g errgroup.Group
	g.SetLimit(changesConcurrency)
	for i, a := range agents {
		g.Go(func() error {
			out[i] = measure(a)
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// changesOf is an agent's diff against the commit it started from, for
// showing: when git can't read it now, the last it could, so a fleet doesn't
// flash every agent to "no changes" while the machine is out of files. What
// decides anything from the changes uses measureChanges, and its error.
func changesOf(a state.Agent) api.AgentChanges {
	out, err := measureChanges(a)
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
func polledChanges(a state.Agent) api.AgentChanges {
	if a.Worktree != "" {
		if _, busy := measuring.LoadOrStore(a.Worktree, true); busy {
			if last, ok := knownChanges.Load(a.Worktree); ok {
				return last.(api.AgentChanges)
			}
		} else {
			defer measuring.Delete(a.Worktree)
		}
	}
	return changesOf(a)
}

// knownChanges is the last changes measureChanges read for each worktree.
var knownChanges sync.Map // worktree -> api.AgentChanges

// measuring is the worktrees polledChanges is measuring now.
var measuring sync.Map // worktree -> true

// measureChanges measures an agent's diff against the commit it started
// from, and whether it has uncommitted work. A worktree that has been
// removed, or an agent still being created, has none. An error is git
// failing to read it (ENFILE, say), and the changes are then unknown: never
// "none", and never "clean".
func measureChanges(a state.Agent) (api.AgentChanges, error) {
	var out api.AgentChanges
	if a.Worktree == "" || a.BaseCommit == "" {
		return out, nil
	}
	if _, err := os.Stat(a.Worktree); errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	stat, err := gitrepo.DiffWorktree(a.Worktree, a.BaseCommit, true)
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
