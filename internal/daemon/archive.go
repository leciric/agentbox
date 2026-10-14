package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// Past agents: an agent whose machine is removed — destroyed by the user,
// retired by the lead, removed by cleanup — is archived (state.ArchiveAgent)
// rather than deleted, with its chat, until the user deletes it here.

// archivedPRTTL is how long an archived agent's open pull request is believed
// before a list reads it from GitHub again.
const archivedPRTTL = 5 * time.Minute

func (s *Server) listArchived(w http.ResponseWriter, r *http.Request) error {
	q, err := archivedQueryOf(r)
	if err != nil {
		return err
	}
	if p := r.PathValue("project"); p != "" {
		q.Project = p // a lead's own project, whatever it asked
	}
	if q.Project != "" {
		p, err := s.store.Project(r.Context(), q.Project)
		if err != nil {
			return err
		}
		q.Project = p.Name
	}
	found, err := s.store.ArchivedAgents(r.Context(), q)
	if err != nil {
		return err
	}
	found = s.refreshArchivedPRs(r.Context(), found)
	out, err := s.apiArchived(r.Context(), found)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// archivedQueryOf reads GET /v1/archived-agents' filters: project, q, pr (a
// state or none), since and until (RFC 3339 times or YYYY-MM-DD dates, until
// a date meaning through it), and limit.
func archivedQueryOf(r *http.Request) (state.ArchivedQuery, error) {
	v := r.URL.Query()
	q := state.ArchivedQuery{Project: v.Get("project"), Query: v.Get("q"), PRState: v.Get("pr")}
	var err error
	if q.Since, err = archivedTime(v.Get("since"), false); err != nil {
		return q, err
	}
	if q.Until, err = archivedTime(v.Get("until"), true); err != nil {
		return q, err
	}
	if s := v.Get("limit"); s != "" {
		if q.Limit, err = strconv.Atoi(s); err != nil || q.Limit < 0 {
			return q, fmt.Errorf("limit must be a number of agents, not %q", s)
		}
	}
	return q, nil
}

func archivedTime(s string, through bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.ParseInLocation(time.DateOnly, s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("a date is like 2026-10-11 or 2026-10-11T09:00:00Z, not %q", s)
	}
	if through {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}

func (s *Server) getArchived(w http.ResponseWriter, r *http.Request) error {
	a, err := s.archivedFromPath(r)
	if err != nil {
		return err
	}
	found := s.refreshArchivedPRs(r.Context(), []state.ArchivedAgent{a})
	out, err := s.apiArchived(r.Context(), found)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out[0])
}

func (s *Server) deleteArchived(w http.ResponseWriter, r *http.Request) error {
	a, err := s.archivedFromPath(r)
	if err != nil {
		return err
	}
	if err := s.deleteArchivedNow(r.Context(), a.Project, a.Name); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteArchivedMany(w http.ResponseWriter, r *http.Request) error {
	var req api.DeleteArchivedRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	out := api.DeleteArchivedResult{Deleted: []string{}}
	for _, ref := range req.Refs {
		i := strings.LastIndex(ref, "/")
		err := errors.New("a past agent's ref is project/name")
		if i > 0 && i < len(ref)-1 {
			err = s.deleteArchivedNow(r.Context(), s.projectSlug(r.Context(), ref[:i]), ref[i+1:])
		}
		if err != nil {
			if out.Failed == nil {
				out.Failed = map[string]string{}
			}
			out.Failed[ref] = err.Error()
			continue
		}
		out.Deleted = append(out.Deleted, ref)
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteArchivedNow(ctx context.Context, project, name string) error {
	if err := s.manager(nil).DeleteArchived(ctx, project, name); err != nil {
		return err
	}
	s.chat.Forget(project + "/" + name)
	return nil
}

// continueArchived starts a new agent where a past one left off: its branch
// starts at the past agent's (local, or the remote's when only that is left),
// it is based on what the past agent was based on, and its first message is
// a short summary of the past agent's chat, then the user's message.
func (s *Server) continueArchived(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.ContinueArchivedRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	a, err := s.archivedFromPath(r)
	if err != nil {
		return err
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return err
	}
	create := api.CreateAgentRequest{
		Project: a.Project, Title: a.Title, AI: cmp.Or(req.AI, a.AI), Interface: state.InterfaceChat,
		Branch: strings.TrimPrefix(a.Branch, p.BranchPrefix),
	}
	start := ""
	if repo, err := gitrepo.Open(p.Root); err == nil {
		if a.BaseRef != "" {
			if _, err := repo.ResolveCommit(a.BaseRef); err == nil {
				create.From = a.BaseRef
			}
		}
		for _, ref := range []string{"refs/heads/" + a.Branch, "refs/remotes/origin/" + a.Branch} {
			if a.Branch == "" {
				break
			}
			if commit, err := repo.ResolveCommit(ref); err == nil {
				create.StartAt, start = commit, ref
				break
			}
		}
	}
	if agent.CheckBranchSlug(create.Branch) != nil {
		create.Branch = ""
	}
	last, err := s.store.LastMessage(ctx, a.Project, a.Name, "assistant")
	if err != nil {
		return err
	}
	create.Task = continueBrief(a, start, last, strings.TrimSpace(req.Message))
	j, err := s.createAgentJob(ctx, create, false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, j)
}

// continueBrief is the first message of an agent that continues a past one:
// who that was, where its work is, what it was asked and what it said last,
// each cut short, then what the user asks now. It is made from the chat
// rather than by a model: free, instant, and nothing to fail.
func continueBrief(a state.ArchivedAgent, start, last, message string) string {
	var b strings.Builder
	name := a.Name
	if a.Title != "" {
		name = fmt.Sprintf("%s (%q)", a.Name, a.Title)
	}
	fmt.Fprintf(&b, "[AgentBox: you are continuing the work of %s, a past agent of this project whose machine was removed on %s.", name, a.ArchivedAt.Format(time.DateOnly))
	switch {
	case start != "":
		fmt.Fprintf(&b, " Your branch starts where its branch %s ended.", a.Branch)
	case a.Branch != "":
		fmt.Fprintf(&b, " Its branch %s is gone, so you start from %s: its work was merged or is only on GitHub.", a.Branch, cmp.Or(a.BaseRef, "the project's branch"))
	}
	if a.PR.Number > 0 {
		fmt.Fprintf(&b, " Its pull request is #%d, %q (%s) %s.", a.PR.Number, a.PR.Title, a.PR.State, a.PR.URL)
	}
	b.WriteString(" A summary of its chat follows; the whole chat is in the app's Past agents list.]\n")
	if a.Task != "" {
		task, _ := shorten(a.Task, maxSummary)
		fmt.Fprintf(&b, "\nWhat it was asked:\n\n%s\n", quote(task))
	}
	if summary, _ := summaryOf(last); summary != "" {
		fmt.Fprintf(&b, "\nWhat it said last:\n\n%s\n", quote(summary))
	}
	if message != "" {
		fmt.Fprintf(&b, "\n---\n\n%s", message)
	} else {
		b.WriteString("\n---\n\nRead the branch, check where the work stands, and carry it on.")
	}
	return b.String()
}

// refreshArchivedPRs reads again, from GitHub, the pull requests of these
// archived agents that were open when last read, at most every archivedPRTTL
// each, and records what changed. Anything that fails leaves what was known.
func (s *Server) refreshArchivedPRs(ctx context.Context, found []state.ArchivedAgent) []state.ArchivedAgent {
	byProject := map[string][]int{}
	now := time.Now()
	s.mu.Lock()
	if s.archivedPRChecked == nil {
		s.archivedPRChecked = map[string]time.Time{}
	}
	for i, a := range found {
		if a.PR.Number == 0 || a.PR.State != state.PROpen || now.Sub(s.archivedPRChecked[a.Ref()]) < archivedPRTTL {
			continue
		}
		s.archivedPRChecked[a.Ref()] = now
		byProject[a.Project] = append(byProject[a.Project], i)
	}
	s.mu.Unlock()
	if len(byProject) == 0 {
		return found
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	m := s.manager(nil)
	for project, idx := range byProject {
		p, err := s.store.Project(ctx, project)
		if err != nil {
			continue
		}
		account, err := m.GitHubAccountFor(p, found[idx[0]].GitHubAccount)
		if err != nil {
			continue
		}
		token, err := m.Creds.GitHubToken(account)
		if err != nil || token == "" {
			continue
		}
		repo, err := github.RepoOf(p.Root)
		if err != nil {
			continue
		}
		numbers := make([]int, len(idx))
		for k, i := range idx {
			numbers[k] = found[i].PR.Number
		}
		states, _, err := s.gitHub(token).PullRequestStates(ctx, repo, numbers, nil)
		if err != nil {
			s.logf("reading past agents' pull requests in %s: %v", project, err)
			continue
		}
		for _, i := range idx {
			st, ok := states[found[i].PR.Number]
			if !ok || st.State == found[i].PR.State && st.Title == found[i].PR.Title {
				continue
			}
			found[i].PR.State, found[i].PR.Title = st.State, st.Title
			if err := s.store.SetArchivedPR(ctx, found[i].Project, found[i].Name, found[i].PR); err != nil {
				s.logf("recording %s's pull request: %v", found[i].Ref(), err)
			}
		}
	}
	return found
}

func (s *Server) apiArchived(ctx context.Context, found []state.ArchivedAgent) ([]api.ArchivedAgent, error) {
	ids := make([]string, 0, len(found))
	for _, a := range found {
		if a.ID != "" {
			ids = append(ids, a.ID)
		}
	}
	tokens, err := s.store.ArchivedTokens(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]api.ArchivedAgent, len(found))
	for i, a := range found {
		out[i] = api.ArchivedAgent{
			Ref: a.Ref(), Project: a.Project, Name: a.Name, Title: a.Title, Task: a.Task,
			AI: a.AI, Interface: a.Interface, Branch: a.Branch, BaseRef: a.BaseRef, BaseCommit: a.BaseCommit,
			CreatedAt: a.CreatedAt, ArchivedAt: a.ArchivedAt,
		}
		if a.PR.Number > 0 {
			out[i].PR = &api.PullRequest{Number: a.PR.Number, Title: a.PR.Title, URL: a.PR.URL, State: a.PR.State}
		}
		if a.ID != "" {
			out[i].Tokens, out[i].CostUSD = tokens[a.ID].Tokens, tokens[a.ID].CostUSD
		}
	}
	return out, nil
}

// agentOrArchivedFromPath is the agent a chat's read-only routes read: a live
// one, or else an archived one, whose chat stays readable.
func (s *Server) agentOrArchivedFromPath(r *http.Request) (state.Agent, error) {
	a, err := s.agentFromPath(r)
	if !errors.Is(err, state.ErrNotFound) {
		return a, err
	}
	past, perr := s.archivedFromPath(r)
	if perr != nil {
		return state.Agent{}, err
	}
	return past.AsAgent(), nil
}

// archivedFromPath is the past agent a route's {project} and {agent} name;
// the project may be named by what it is called, as for a live agent.
func (s *Server) archivedFromPath(r *http.Request) (state.ArchivedAgent, error) {
	return s.store.ArchivedAgentByName(r.Context(), s.projectSlug(r.Context(), r.PathValue("project")), r.PathValue("agent"))
}

// projectSlug is the slug of the project named, by slug or by what it is
// called; a name no project has is passed on as it is, to be not found.
func (s *Server) projectSlug(ctx context.Context, name string) string {
	if p, err := s.store.Project(ctx, name); err == nil {
		return p.Name
	}
	return name
}
