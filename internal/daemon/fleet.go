package daemon

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
		if !entry.at.IsZero() {
			at := entry.at
			out.PullsFetchedAt = &at
		}
		prs = entry.byAgent(heads)
	} else {
		out.GitHubError = s.githubErrorFor(p, repo, err)
	}

	changes := agentChanges(agents, changesOf)

	for i, st := range statuses {
		row := api.FleetAgent{Agent: s.agentInfo(st)}
		row.Changes = changes[i]
		if items, err := s.store.Media(ctx, st.Project, st.Name); err == nil {
			row.Media = len(items)
		}
		row.PR = prs[st.Name]
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
	g.Wait()
	return out
}

// changesOf measures an agent's diff against the commit it started from. A
// worktree that has been removed, or an agent still being created, has none.
func changesOf(a state.Agent) api.AgentChanges {
	var out api.AgentChanges
	stat, err := gitrepo.DiffWorktree(a.Worktree, a.BaseCommit, true)
	if err != nil {
		return out
	}
	// The last line of --stat is " N files changed, N insertions(+), N deletions(-)".
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
	if dirty, err := gitrepo.Dirty(a.Worktree); err == nil {
		out.Dirty = dirty
	}
	return out
}

// projectMedia is every agent's media for one project, newest first, each item
// labelled with the agent it came from so the app can filter without a lookup.
func (s *Server) projectMedia(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	project := r.PathValue("project")
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return err
	}
	items, err := s.store.ProjectMedia(ctx, project)
	if err != nil {
		return err
	}
	titles, err := s.agentTitles(ctx, project)
	if err != nil {
		return err
	}
	m := s.manager(nil)
	wanted := r.URL.Query().Get("agent")
	kind := r.URL.Query().Get("kind")
	out := make([]api.MediaItem, 0, len(items))
	for _, it := range items {
		if (wanted != "" && it.Agent != wanted) || (kind != "" && it.Kind != kind) {
			continue
		}
		item := toAPIMedia(it, m.MediaPath(it))
		item.AgentName = it.Agent
		if title, ok := titles[it.Agent]; ok {
			item.AgentTitle = title
		} else {
			// Its agent is gone: it was kept, not deleted, at destroy time.
			item.AgentGone = true
		}
		if !it.OrphanedAt.IsZero() {
			// Matches Store.ExpiredMedia's own arithmetic, so what's shown here
			// is exactly when the sweeper will remove the item.
			expires := it.OrphanedAt.Add(time.Duration(p.MediaRetentionDays) * 24 * time.Hour)
			item.ExpiresAt = &expires
		}
		out = append(out, item)
	}
	return writeJSON(w, http.StatusOK, out)
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
