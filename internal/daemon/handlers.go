package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/android"
	"agentbox/internal/api"
	"agentbox/internal/brief"
	"agentbox/internal/credentials"
	"agentbox/internal/gitrepo"
	"agentbox/internal/hostos"
	"agentbox/internal/image"
	"agentbox/internal/naming"
	"agentbox/internal/notes"
	"agentbox/internal/state"
)

// maxProjectName keeps ab-<project>-<agent> within Incus' 63-character limit.
const maxProjectName = 30

func (s *Server) version(w http.ResponseWriter, _ *http.Request) error {
	groups, _ := os.Getgroups()
	reachable := s.cfg.Incus.Reachable()
	return writeJSON(w, http.StatusOK, api.VersionInfo{Version: Version, Groups: groups, Incus: &reachable})
}

func (s *Server) shutdown(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
	go s.stop()
	return nil
}

// Projects

func projectInfo(p state.Project) api.Project {
	info := api.Project{Name: p.Name, Root: p.Root, EnvFiles: []string{}, Android: android.IsProject(p.Root),
		ClaudeAccount: p.ClaudeAccount, ClaudeAccounts: nonNil(p.ClaudeAccounts), GitHubAccount: p.GitHubAccount, Autonomy: p.Autonomy,
		AgentModel: p.AgentModel, BranchPrefix: p.BranchPrefix, FinishNotices: p.FinishNotices,
		RolloverThreshold: p.RolloverThreshold, ContextBudget: p.ContextBudget,
		Consolidation: p.Consolidation, ConsolidationModel: p.ConsolidationModel,
		Section: p.Section, Position: p.Position, CreatedAt: p.CreatedAt}
	if repo, err := gitrepo.Open(p.Root); err == nil {
		info.Branch = repo.CurrentBranch()
		if files, err := repo.EnvFiles(); err == nil && files != nil {
			info.EnvFiles = files
		}
	}
	return info
}

// nonNil keeps an empty list a list in JSON, which the app's types promise.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	projects, err := s.store.Projects(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.Project, 0, len(projects))
	for _, p := range projects {
		out = append(out, projectInfo(p))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) addProject(w http.ResponseWriter, r *http.Request) error {
	var req api.AddProjectRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	repo, err := gitrepo.Open(req.Path)
	if err != nil {
		return err
	}
	diskErr := checkProjectDisk(repo.Root, hostos.WSL())
	if diskErr != nil && !req.CopyToLinux {
		return diskErr
	}
	if !repo.HasCommits() {
		return fmt.Errorf("%s has no commits yet: agents branch from a commit", repo.Root)
	}
	name := req.Name
	if name == "" {
		name = naming.Slug(filepath.Base(repo.Root))
	}
	if err := naming.Validate("project", name, maxProjectName); err != nil {
		return fmt.Errorf("%w (choose one with --name)", err)
	}
	if diskErr != nil {
		// Checked before copying, so a name that's taken doesn't leave a copy behind.
		if _, err := s.store.Project(r.Context(), name); err == nil {
			return fmt.Errorf("there's already a project called %s (choose another name)", name)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		copied, err := gitrepo.Clone(repo, filepath.Join(home, "src", name))
		if err != nil {
			return fmt.Errorf("copying %s into WSL: %w", repo.Root, err)
		}
		repo = copied
	}
	claudeAccount, githubAccount := strings.TrimSpace(req.ClaudeAccount), strings.TrimSpace(req.GitHubAccount)
	if err := s.checkClaudeAccount(claudeAccount); err != nil {
		return err
	}
	if err := s.checkGitHubAccount(githubAccount); err != nil {
		return err
	}
	p := state.Project{Name: name, Root: repo.Root, ClaudeAccount: claudeAccount, GitHubAccount: githubAccount, CreatedAt: time.Now()}
	// A new project may only use the account it was given, or the machine's
	// default when it was given none, until the user allows more. The list is
	// written out rather than left empty, which still allows every account.
	if own, err := s.manager(nil).Creds.ClaudeAccountOf(claudeAccount); err != nil {
		return err
	} else if own != "" {
		p.ClaudeAccounts = []string{own}
	}
	if err := s.store.AddProject(r.Context(), p); err != nil {
		return err
	}
	// Read it back, so the answer carries what the store filled in.
	if stored, err := s.store.Project(r.Context(), p.Name); err == nil {
		p = stored
	}
	// Its chat's socket, so the project can be talked to straight away.
	if err := s.serveLeadAPI(p.Name); err != nil {
		s.logf("lead API socket for %s: %v", p.Name, err)
	}
	s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
	return writeJSON(w, http.StatusCreated, projectInfo(p))
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, projectInfo(p))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	var req api.UpdateProjectRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	// The chat's brief states these settings, and its Claude Code account is
	// the project's, so the chat is reconfigured when one of them changes
	// rather than at whatever later moment it next starts. Failing to do that
	// doesn't undo the setting, which is stored: the brief is written, and the
	// account resolved, again from the store on the chat's next turn
	// (repairLead), so this is a delay worth logging, not a failed request.
	rewriteBrief := func() {
		if err := s.manager(nil).ReconfigureLead(r.Context(), p.Name); err != nil {
			s.logf("reconfiguring the %s chat: %v", p.Name, err)
		}
	}
	if req.Autonomy != nil {
		autonomy := strings.TrimSpace(*req.Autonomy)
		if err := s.store.SetProjectAutonomy(r.Context(), p.Name, autonomy); err != nil {
			return err
		}
		p.Autonomy = autonomy
		rewriteBrief()
	}
	if req.AgentModel != nil {
		model := strings.TrimSpace(*req.AgentModel)
		if err := s.store.SetProjectAgentModel(r.Context(), p.Name, model); err != nil {
			return err
		}
		p.AgentModel = model
		rewriteBrief()
		s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
	}
	if req.BranchPrefix != nil {
		// Not trimmed: a prefix is a literal part of a branch name, and one
		// with a space in it is refused rather than quietly changed.
		prefix := *req.BranchPrefix
		if err := gitrepo.CheckBranchPrefix(prefix); err != nil {
			return err
		}
		if repo, err := gitrepo.Open(p.Root); err == nil {
			if branch := repo.BranchInTheWay(prefix); branch != "" {
				return fmt.Errorf("%s already has a branch %s, so no branch can start with %s", p.Name, branch, prefix)
			}
		}
		if err := s.store.SetProjectBranchPrefix(r.Context(), p.Name, prefix); err != nil {
			return err
		}
		p.BranchPrefix = prefix
		s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
	}
	if req.ClaudeAccount != nil || req.ClaudeAccounts != nil {
		account, allowed := p.ClaudeAccount, p.ClaudeAccounts
		if req.ClaudeAccount != nil {
			account = strings.TrimSpace(*req.ClaudeAccount)
			if err := s.checkClaudeAccount(account); err != nil {
				return err
			}
		}
		if req.ClaudeAccounts != nil {
			allowed = *req.ClaudeAccounts
			for _, name := range allowed {
				if err := s.checkClaudeAccount(strings.TrimSpace(name)); err != nil {
					return err
				}
			}
		}
		if err := s.store.SetProjectClaudeAccounts(r.Context(), p.Name, account, allowed); err != nil {
			return err
		}
		if stored, err := s.store.Project(r.Context(), p.Name); err == nil {
			p = stored
		}
		// The lead's brief names the accounts it may spread agents across, and
		// the lead itself runs on the account the project resolves to, so
		// both follow either change (ReconfigureLead). Its session keeps the
		// token it started with until it next starts.
		rewriteBrief()
		s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
	}
	if req.GitHubAccount != nil {
		account := strings.TrimSpace(*req.GitHubAccount)
		if err := s.checkGitHubAccount(account); err != nil {
			return err
		}
		if err := s.store.SetProjectGitHubAccount(r.Context(), p.Name, account); err != nil {
			return err
		}
		p.GitHubAccount = account
		// Pull requests are read with this account, so what was read with the
		// other one says nothing about this one: a 404 there can be a list
		// here. Dropped the same way as when an account is saved or removed.
		s.pulls.reset()
		s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
	}
	if req.FinishNotices != nil {
		notices := strings.TrimSpace(*req.FinishNotices)
		if err := s.store.SetProjectFinishNotices(r.Context(), p.Name, notices); err != nil {
			return err
		}
		p.FinishNotices = notices
	}
	if req.RolloverThreshold != nil {
		if err := s.store.SetProjectRolloverThreshold(r.Context(), p.Name, *req.RolloverThreshold); err != nil {
			return err
		}
		p.RolloverThreshold = *req.RolloverThreshold
	}
	if req.ContextBudget != nil {
		if err := s.store.SetProjectContextBudget(r.Context(), p.Name, *req.ContextBudget); err != nil {
			return err
		}
		p.ContextBudget = *req.ContextBudget
		// Every brief carries a context built to this budget, so a new one
		// reaches the agents that already exist rather than only the next one
		// made — the same path a notes change takes.
		if err := s.manager(nil).RewriteBriefs(r.Context(), p.Name); err != nil {
			s.logf("rewriting %s's briefs after a context budget change: %v", p.Name, err)
		}
	}
	if req.Consolidation != nil {
		if err := s.store.SetProjectConsolidation(r.Context(), p.Name, *req.Consolidation); err != nil {
			return err
		}
		p.Consolidation = *req.Consolidation
	}
	if req.ConsolidationModel != nil {
		model := strings.TrimSpace(*req.ConsolidationModel)
		if err := s.store.SetProjectConsolidationModel(r.Context(), p.Name, model); err != nil {
			return err
		}
		p.ConsolidationModel = model
	}
	return writeJSON(w, http.StatusOK, projectInfo(p))
}

// checkClaudeAccount and checkGitHubAccount reject an account name that no new
// agent could use anyway, whether the project is being added or changed. An
// empty name is the machine's default account, which is always allowed.
func (s *Server) checkClaudeAccount(account string) error {
	if account == "" {
		return nil
	}
	ok, err := s.manager(nil).Creds.HasClaudeAccount(account)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no Claude Code account named %q: add it with agentbox auth claude --account %s", account, account)
	}
	return nil
}

func (s *Server) checkGitHubAccount(account string) error {
	if account == "" {
		return nil
	}
	ok, err := s.manager(nil).Creds.HasGitHubAccount(account)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no GitHub account named %q: add it with agentbox auth github --account %s", account, account)
	}
	return nil
}

func (s *Server) removeProject(w http.ResponseWriter, r *http.Request) error {
	// The project's chat goes with it; only its agents block removal.
	project := r.PathValue("project")
	s.chat.Stop(agent.LeadRef(project), "the project was removed")
	s.chat.Forget(agent.LeadRef(project))
	if err := s.manager(s.cfg.Log).DestroyLead(r.Context(), project); err != nil {
		return err
	}
	defer s.stopLeadAPI(project)
	name := r.PathValue("project")
	if err := s.store.RemoveProject(r.Context(), name); err != nil {
		return err
	}
	s.events.publish(api.EventProject, api.ProjectChange{Name: name, Removed: true})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) brief(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	repo, err := gitrepo.Open(p.Root)
	if err != nil {
		return err
	}
	envFiles, err := repo.EnvFiles()
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("agent")
	if name == "" {
		name = "agent-01"
	}
	// The names of the secrets such an agent would have: the project's, plus
	// anything set for this very name if the agent exists.
	secretNames, err := s.secrets().NamesForAgent(r.Context(), p.Name, name)
	if err != nil {
		return err
	}
	// The preview is the whole brief, project notes and all: what the app
	// shows is what an agent made now would read.
	projectNotes, err := notes.Read(s.cfg.Paths.ProjectNotes(p.Name))
	if err != nil {
		return err
	}
	// ...including the memory section, built for the agent named if it exists
	// and for the project's own current task if it doesn't, which is what an
	// agent made now would be given (D75).
	// An agent that exists keeps the branch it was made on, whatever the
	// project's prefix has become since.
	a, err := s.store.Agent(r.Context(), p.Name, name)
	if err != nil {
		a = state.Agent{Project: p.Name, Name: name, Branch: p.BranchPrefix + name}
	}
	knowledge, err := s.manager(nil).ProjectKnowledge(r.Context(), a)
	if err != nil {
		s.logf("building %s's memory section for a brief preview: %v", p.Name, err)
	}
	compactWindow, err := s.store.ClaudeCompactWindow(r.Context())
	if err != nil {
		return err
	}
	text, err := brief.Render(brief.Data{
		Project:  p.Name,
		Agent:    name,
		Worktree: s.cfg.Paths.Worktree(p.Name, name),
		Branch:   a.Branch,
		BaseRef:  repo.CurrentBranch(),
		EnvFiles: envFiles,
		Secrets:  secretNames,
		Android:  android.IsProject(p.Root),
		VM:       hostos.InVM(),
		Host:     hostos.Name(),
		Notes:    projectNotes,

		Knowledge:     knowledge,
		CompactWindow: compactWindow,
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	io.WriteString(w, text)
	return nil
}

// getNotes returns a project's notes: what every agent of it is told, over and
// above the brief AgentBox writes itself. They are one markdown file per
// project, so they read and diff by hand.
func (s *Server) getNotes(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	out, err := s.projectNotes(project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// setNotes replaces them, from the app or the command line.
func (s *Server) setNotes(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.NotesRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := notes.Write(s.cfg.Paths.ProjectNotes(project), req.Text); err != nil {
		return err
	}
	out, err := s.notesChanged(r, project, "set")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// appendNote adds one dated entry under the lead's own heading. It is how a
// project's chat writes down what it learned, and it only ever adds. Changing
// an entry that is already there is editNote and removeNote below, which the
// chat uses when it is asked to in the conversation ([D82](decisions.md#d82)).
func (s *Server) appendNote(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.NotesRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Text) == "" {
		return errors.New("a note needs some text")
	}
	if _, err := notes.AppendToFile(s.cfg.Paths.ProjectNotes(project), req.Text, time.Now()); err != nil {
		return err
	}
	out, err := s.notesChanged(r, project, "append")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// editNote changes what one entry says, and removeNote takes one out. Both are
// the lead carrying out an instruction from the conversation, so both name the
// entry by quoting it rather than by an id the file doesn't have, and both
// refuse a quote that doesn't pick out exactly one entry
// ([D82](decisions.md#d82)).
func (s *Server) editNote(w http.ResponseWriter, r *http.Request) error {
	return s.changeNote(w, r, func(path string, req api.EditNoteRequest) (string, notes.Change, error) {
		if strings.TrimSpace(req.Text) == "" {
			return "", notes.Change{}, errors.New("an edited note needs some text: removing one is its own route")
		}
		return notes.EditInFile(path, req.Match, req.Text)
	}, "edit")
}

func (s *Server) removeNote(w http.ResponseWriter, r *http.Request) error {
	return s.changeNote(w, r, func(path string, req api.EditNoteRequest) (string, notes.Change, error) {
		return notes.RemoveFromFile(path, req.Match)
	}, "remove")
}

func (s *Server) changeNote(w http.ResponseWriter, r *http.Request,
	apply func(string, api.EditNoteRequest) (string, notes.Change, error), how string) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.EditNoteRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Match) == "" {
		return errors.New("name the note to change by quoting it")
	}
	// A quote that matches nothing, or more than one entry, comes back as an
	// error and leaves the file alone: nothing below runs, so no brief is
	// rewritten for a change that didn't happen.
	_, change, err := apply(s.cfg.Paths.ProjectNotes(project), req)
	if err != nil {
		return err
	}
	out, err := s.notesChanged(r, project, how)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.NoteChange{
		Notes: out, Was: change.Was, Now: change.Now,
		Section: change.Section, FromLead: change.FromLead,
	})
}

// notesChanged rewrites the brief of every agent of the project that has one,
// so notes saved now reach the agents that already exist, and returns the
// notes as they stand. Every way the notes change goes through here — the
// user's own replacement, and the lead appending, editing and removing — so
// no agent is left carrying a note that has moved on.
func (s *Server) notesChanged(r *http.Request, project, how string) (api.Notes, error) {
	if err := s.manager(s.cfg.Log).RewriteBriefs(r.Context(), project); err != nil {
		// The notes are saved, and every agent made from here on has them. An
		// agent whose machine wouldn't take the file is worth a line in the
		// log, not a failed save.
		s.logf("rewriting %s's briefs after a notes change: %v", project, err)
	}
	s.events.publish(api.EventProject, api.ProjectChange{Name: project})
	out, err := s.projectNotes(project)
	if err != nil {
		return api.Notes{}, err
	}
	s.captureEvent(r.Context(), project, "", "notes_changed", map[string]any{
		"how":     how,
		"length":  len([]rune(out.Text)),
		"preview": truncateRunes(out.Text, 500),
	}, "")
	return out, nil
}

func (s *Server) projectNotes(project string) (api.Notes, error) {
	path := s.cfg.Paths.ProjectNotes(project)
	text, err := notes.Read(path)
	if err != nil {
		return api.Notes{}, err
	}
	out := api.Notes{Text: text}
	if info, err := os.Stat(path); err == nil {
		out.UpdatedAt = info.ModTime()
	}
	return out, nil
}

func (s *Server) getBase(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	m := s.manager(nil)
	base, ok, err := m.ProjectBase(r.Context(), project)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %s has no saved base: %w", project, state.ErrNotFound)
	}
	out := apiBase(base)
	// What the last save replaced, when it kept one: the app offers going back
	// to it, and says so beside the base that replaced it.
	if previous, ok, err := m.PreviousBase(r.Context(), project); err != nil {
		return err
	} else if ok {
		was := apiBase(previous)
		out.Previous = &was
	}
	return writeJSON(w, http.StatusOK, out)
}

func apiBase(base agent.Base) api.Base {
	return api.Base{Snapshot: base.SnapshotRef(), SavedFrom: base.SavedFrom, SavedAt: base.SavedAt}
}

func (s *Server) saveBase(w http.ResponseWriter, r *http.Request) error {
	var req api.SaveBaseRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	a, err := s.store.Agent(r.Context(), r.PathValue("project"), req.Agent)
	if err != nil {
		return err
	}
	return s.startJob(w, "base-save", a.Ref(), func(ctx context.Context, log io.Writer) (any, error) {
		base, err := s.manager(log).SaveBase(ctx, a)
		if err != nil {
			return nil, err
		}
		return apiBase(base), nil
	})
}

// removeBase drops the project's base, so new agents start from the plain
// image again. ?previous=1 drops only what the last save kept, which gives its
// disk back and leaves the base the project is on.
func (s *Server) removeBase(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	m := s.manager(nil)
	remove := m.RemoveBase
	if r.URL.Query().Get("previous") != "" {
		remove = m.RemovePreviousBase
	}
	if err := remove(r.Context(), project); err != nil {
		if strings.Contains(err.Error(), "no saved base") || strings.Contains(err.Error(), "no previous base") {
			return fmt.Errorf("%w: %w", err, state.ErrNotFound)
		}
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// revertBase puts back the base the last save replaced. It is two renames and
// a delete rather than a copy, so it answers in the request rather than as a
// job.
func (s *Server) revertBase(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	base, err := s.manager(nil).RevertBase(r.Context(), project)
	if err != nil {
		if strings.Contains(err.Error(), "no previous base") {
			return fmt.Errorf("%w: %w", err, state.ErrNotFound)
		}
		return err
	}
	s.events.publish(api.EventProject, api.ProjectChange{Name: project})
	return writeJSON(w, http.StatusOK, apiBase(base))
}

// Agents

func toAPIAgent(st agent.Status) api.Agent {
	a := st.Agent
	return api.Agent{
		Ref:        a.Ref(),
		Project:    a.Project,
		Name:       a.Name,
		Title:      a.Title,
		Instance:   a.Instance,
		AI:         a.AI,
		Autonomous: a.Autonomous,
		Branch:     a.Branch,
		BaseRef:    a.BaseRef,
		BaseCommit: a.BaseCommit,
		Worktree:   a.Worktree,
		Source:     a.Source,

		ClaudeAccount: a.ClaudeAccount,
		GitHubAccount: a.GitHubAccount,
		Interface:     a.Interface,

		State:     st.State,
		IP:        st.IP,
		Limits:    api.Limits{CPU: st.Limits.CPU, Allowance: st.Limits.Allowance, Memory: st.Limits.Memory},
		CreatedAt: a.CreatedAt,
	}
}

// describe returns an agent with its live state.
func (s *Server) describe(ctx context.Context, a state.Agent) (api.Agent, error) {
	statuses, err := s.manager(nil).List(ctx, a.Project)
	if err != nil {
		return api.Agent{}, err
	}
	for _, st := range statuses {
		if st.Name == a.Name {
			return s.agentInfo(st), nil
		}
	}
	return api.Agent{}, fmt.Errorf("agent %s: %w", a.Ref(), state.ErrNotFound)
}

// agentFromPath finds the agent a route is about. A project's lead is refused
// here, once, for every route that assumes a machine: its terminal, browser,
// Android, media, snapshots, diff and lifecycle. The lead has none of those.
func (s *Server) agentFromPath(r *http.Request) (state.Agent, error) {
	a, err := s.store.Agent(r.Context(), r.PathValue("project"), r.PathValue("agent"))
	if err != nil {
		return state.Agent{}, err
	}
	if a.IsLead() {
		return state.Agent{}, fmt.Errorf("%s is the project's chat, which has no machine: use /v1/projects/%s/chat", a.Ref(), a.Project)
	}
	return a, nil
}

// agentReady serves a new agent's in-agent API and describes it.
func (s *Server) agentReady(ctx context.Context, a state.Agent) (api.Agent, error) {
	if err := s.serveAgentAPI(a.Instance); err != nil {
		return api.Agent{}, err
	}
	s.refreshAgents(ctx)
	return s.describe(ctx, a)
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) error {
	statuses, err := s.manager(nil).List(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		return err
	}
	out := make([]api.Agent, 0, len(statuses))
	for _, st := range statuses {
		if st.IsLead() {
			continue // the project's chat, shown as the project, not as an agent
		}
		out = append(out, s.agentInfo(st))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	info, err := s.describe(r.Context(), a)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.UpdateAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Title != nil {
		title, err := agent.CleanTitle(*req.Title)
		if err != nil {
			return err
		}
		if err := s.store.SetAgentTitle(r.Context(), a.Project, a.Name, title); err != nil {
			return err
		}
	}
	if req.ClaudeAccount != nil {
		if _, err := s.manager(nil).SetClaudeAccount(r.Context(), a, strings.TrimSpace(*req.ClaudeAccount)); err != nil {
			return err
		}
	}
	if req.GitHubAccount != nil {
		if _, err := s.manager(nil).SetGitHubAccount(r.Context(), a, strings.TrimSpace(*req.GitHubAccount)); err != nil {
			return err
		}
	}
	if req.Interface != nil {
		updated, err := s.manager(nil).SetInterface(r.Context(), a, strings.TrimSpace(*req.Interface))
		if err != nil {
			return err
		}
		if updated.Interface == state.InterfaceCLI {
			s.chat.Stop(a.Ref(), "you switched to the command line")
		}
	}
	if req.CPU != nil || req.Memory != nil || req.CPUAllowance != nil {
		// Applied to the machine as it runs; nothing restarts.
		if _, err := s.manager(nil).SetLimits(r.Context(), a, agent.LimitChoice{
			CPU:       req.CPU,
			Allowance: req.CPUAllowance,
			Memory:    req.Memory,
		}); err != nil {
			return err
		}
	}
	info, err := s.describe(r.Context(), a)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) error {
	var req api.CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	return s.createAgentFrom(w, r, req, false)
}

// createAgentFrom starts the job that makes an agent. When the request carries
// a task, the agent is given it as its first message once it is ready, which is
// how a project's chat hands work over.
// byLead says the project's chat asked for the agent, so it is waiting to hear
// how its task went (D87).
func (s *Server) createAgentFrom(w http.ResponseWriter, r *http.Request, req api.CreateAgentRequest, byLead bool) error {
	p, err := s.store.Project(r.Context(), req.Project)
	if err != nil {
		return err
	}
	if req.AI == "" {
		req.AI = "claude"
	}
	// A model or effort this agent could never run on is a mistake worth
	// seeing now, in the reply to the request that made it, rather than in a
	// job that has already started copying a machine.
	if err := s.manager(nil).ChatChoices(r.Context(), req.AI, req.Model, req.Effort); err != nil {
		return err
	}
	// And for a branch that could never be one.
	if err := agent.CheckBranchSlug(req.Branch); err != nil {
		return err
	}
	// Same for the account: unknown, outside the project's allow-list, or
	// named for a tool that isn't Claude Code are all mistakes this can see
	// before anything starts. Caught here they come back as the tool's own
	// error; caught only inside the job, the caller has already been told the
	// agent is on its way and has nothing left to correct (see the byLead
	// notice below, for what Create can still fail at once the job is
	// running).
	if _, err := s.manager(nil).CheckLogin(req.AI, p, req.ClaudeAccount); err != nil {
		return err
	}
	// Absent means autonomous: that is what the command line, the app's dialog
	// and a project's chat have all always sent, so an omitted field keeps
	// doing what every caller already asked for.
	autonomous := req.Autonomous == nil || *req.Autonomous
	return s.startJob(w, "create", req.Project, func(ctx context.Context, log io.Writer) (any, error) {
		a, err := s.manager(log).Create(ctx, req.Project, agent.CreateOptions{
			Name:          req.Name,
			Branch:        req.Branch,
			Title:         req.Title,
			AI:            req.AI,
			Interface:     req.Interface,
			Autonomous:    autonomous,
			Model:         req.Model,
			Effort:        req.Effort,
			ContextWindow: req.ContextWindow,
			From:          req.From,

			ClaudeAccount: req.ClaudeAccount,
			GitHubAccount: req.GitHubAccount,

			CopyEnv: !req.NoEnv,
			Clean:   req.Clean,
			Limits:  agent.LimitChoice{CPU: req.CPU, Allowance: req.CPUAllowance, Memory: req.Memory},

			FinishNotice: req.FinishNotice,
			Task:         strings.TrimSpace(req.Task),
		})
		if err != nil {
			if byLead {
				// The chat was already told the job had started (D87): a
				// failure that only Create itself could find — the machine
				// couldn't be copied, the name collided — is news the chat
				// is still waiting on, the same as a finish it asked for.
				s.tellLead(ctx, req.Project, fmt.Sprintf("create_agent failed: %v", err), true)
			}
			return nil, err
		}
		task := strings.TrimSpace(req.Task)
		model := ""
		if req.Model != nil {
			model = *req.Model
		}
		s.captureEvent(ctx, a.Project, a.Name, "agent_created", map[string]any{
			"title": a.Title, "task": task, "model": model, "branch": a.Branch,
		}, "")
		s.addActiveAgent(ctx, a.Project, a.Name)
		// An agent is made for something, and that something is a row in the
		// project's plan (D77) rather than only a line in its history.
		s.captureAgentTask(ctx, a, task)
		if task != "" {
			if byLead {
				s.leadAsked(a)
			}
			if _, err := s.chat.Send(a, task); err != nil {
				fmt.Fprintf(log, "the agent was made, but its task couldn't be sent: %v\n", err)
			}
		}
		// The thread in the project chat's rail opens on this, so an agent the
		// chat made is there from the start rather than at its first finish.
		s.record(ctx, createdEvent(a, task, time.Now()))
		return s.agentReady(ctx, a)
	})
}

func (s *Server) destroyAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	force, _ := strconv.ParseBool(q.Get("force"))
	deleteBranch, _ := strconv.ParseBool(q.Get("deleteBranch"))
	deleteMedia, _ := strconv.ParseBool(q.Get("deleteMedia"))
	opts := agent.DestroyOptions{Force: force, DeleteBranch: deleteBranch, DeleteMedia: deleteMedia}
	if err := s.destroyAgentNow(r.Context(), s.manager(s.cfg.Log), a, opts); err != nil {
		return err
	}
	s.captureEvent(r.Context(), a.Project, a.Name, "agent_retired", map[string]any{"how": "destroy", "branch": a.Branch}, "")
	s.refreshAgents(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) agentAction(action string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.agentFromPath(r)
		if err != nil {
			return err
		}
		ctx, m := r.Context(), s.manager(s.cfg.Log)
		switch action {
		case "start":
			if err = s.serveAgentAPI(a.Instance); err == nil {
				_, err = m.Start(ctx, a)
			}
		case "stop":
			s.chat.Stop(a.Ref(), "the agent was stopped")
			err = m.Stop(ctx, a)
		case "pause":
			err = m.Pause(ctx, a)
		case "resume":
			err = m.Resume(ctx, a)
		case "session":
			err = m.PrepareShell(ctx, a)
		}
		if err != nil {
			return err
		}
		s.refreshAgents(ctx)
		info, err := s.describe(ctx, a)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, info)
	}
}

func (s *Server) diff(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	stat, _ := strconv.ParseBool(r.URL.Query().Get("stat"))
	text, err := s.manager(nil).Diff(a, stat)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, text)
	return nil
}

// Snapshots

func toAPISnapshot(sn agent.Snapshot) api.Snapshot {
	return api.Snapshot{Name: sn.Name, CreatedAt: sn.CreatedAt, Head: sn.Head}
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	snapshots, err := s.manager(nil).Snapshots(r.Context(), a)
	if err != nil {
		return err
	}
	out := make([]api.Snapshot, 0, len(snapshots))
	for _, sn := range snapshots {
		out = append(out, toAPISnapshot(sn))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) takeSnapshot(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.SnapshotRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	sn, err := s.manager(s.cfg.Log).Snapshot(r.Context(), a, req.Name, req.Consistent)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, toAPISnapshot(sn))
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	if err := s.manager(nil).DeleteSnapshot(r.Context(), a, r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.RestoreRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	return s.startJob(w, "restore", a.Ref(), func(ctx context.Context, log io.Writer) (any, error) {
		m := s.manager(log)
		s.chat.Stop(a.Ref(), "the agent is being restored from a snapshot")
		if err := m.Restore(ctx, a, req.Snapshot); err != nil {
			return nil, err
		}
		m.EnsureBrowser(ctx, a)
		s.refreshAgents(ctx)
		return s.describe(ctx, a)
	})
}

func (s *Server) fork(w http.ResponseWriter, r *http.Request) error {
	src, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.ForkRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	return s.startJob(w, "fork", src.Ref(), func(ctx context.Context, log io.Writer) (any, error) {
		a, err := s.manager(log).Fork(ctx, src, agent.ForkOptions{Name: req.Name, Title: req.Title, Snapshot: req.Snapshot})
		if err != nil {
			return nil, err
		}
		return s.agentReady(ctx, a)
	})
}

// Resources, image and logins

func toAPIUsage(host agent.HostUsage, agents []agent.AgentUsage) api.Usage {
	u := api.Usage{
		Host: api.HostUsage{
			CPU: host.CPU, Cores: host.Cores,
			MemUsed: host.MemUsed, MemTotal: host.MemTotal,
			PoolUsed: host.PoolUsed, PoolTotal: host.PoolTotal,
		},
		Agents: make([]api.AgentUsage, 0, len(agents)),
	}
	for _, a := range agents {
		u.Agents = append(u.Agents, api.AgentUsage{
			Ref: a.Ref(), State: a.State, CPU: a.CPU, Memory: a.Memory, Processes: a.Processes,
			Limits: api.Limits{CPU: a.Limits.CPU, Allowance: a.Limits.Allowance, Memory: a.Limits.Memory},
			Cores:  a.Cores,
		})
	}
	return u
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) error {
	interval := time.Second
	if v := r.URL.Query().Get("interval"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > 10*time.Second {
			return fmt.Errorf("invalid interval %q: use a duration up to 10s", v)
		}
		interval = d
	}
	host, agents, err := s.manager(nil).Usage(r.Context(), interval)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIUsage(host, agents))
}

func (s *Server) imageStatus(w http.ResponseWriter, r *http.Request) error {
	ready, err := image.Ready(r.Context(), s.cfg.Incus)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"ready": ready, "snapshot": image.SnapshotRef()})
}

func (s *Server) buildImage(w http.ResponseWriter, r *http.Request) error {
	if err := image.CheckHost(s.cfg.User); err != nil {
		return err
	}
	var req api.BuildImageRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	components, err := s.chooseImageComponents(r.Context(), req)
	if err != nil {
		return err
	}
	opts := image.Options{Components: components}
	return s.startJob(w, "image-build", image.SnapshotRef(), func(ctx context.Context, log io.Writer) (any, error) {
		if err := image.Build(ctx, s.cfg.Incus, s.cfg.User, opts, log); err != nil {
			return nil, err
		}
		return map[string]string{"snapshot": image.SnapshotRef()}, nil
	})
}

// chooseImageComponents works out which optional components this build
// includes, and remembers them. The ones the request names become the
// installation's choice; the ones it leaves out stay as they were, so a
// rebuild after a version bump never silently drops a component someone
// turned on.
func (s *Server) chooseImageComponents(ctx context.Context, req api.BuildImageRequest) (image.Components, error) {
	components, err := s.imageComponents(ctx)
	if err != nil {
		return image.Components{}, err
	}
	for _, c := range []struct {
		want    *bool
		on      *bool
		setting string
	}{
		{req.Android, &components.Android, state.SettingImageAndroid},
		{req.Codex, &components.Codex, state.SettingImageCodex},
		{req.OpenCode, &components.OpenCode, state.SettingImageOpenCode},
		{req.DevCaches, &components.DevCaches, state.SettingImageDevCaches},
	} {
		if c.want == nil {
			continue
		}
		*c.on = *c.want
		if err := s.store.SetFlag(ctx, c.setting, *c.want); err != nil {
			return image.Components{}, err
		}
	}
	return components, nil
}

// saveGitHubToken stores a GitHub token under an account. It is checked
// against GitHub before it is stored, so a bad paste is refused here rather
// than failing inside an agent later.
func (s *Server) saveGitHubToken(w http.ResponseWriter, r *http.Request) error {
	var req api.GitHubTokenRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return errors.New("no token: paste the one from gh auth token, or a personal access token")
	}
	login, err := s.gitHub(token).Login(r.Context())
	if err != nil {
		return err
	}
	creds := s.manager(nil).Creds
	if err := creds.SaveGitHubToken(req.Account, token); err != nil {
		return err
	}
	// Remember who the token belongs to: the app says which GitHub user a
	// project reads pull requests as, and asking GitHub on every poll to find
	// out would cost a call per account.
	if err := creds.SaveGitHubLogin(req.Account, login); err != nil {
		return err
	}
	s.pulls.reset()
	return writeJSON(w, http.StatusOK, map[string]string{"user": login})
}

// removeGitHubAccount forgets a stored GitHub account. Agents already created
// keep the token that was written into them.
func (s *Server) removeGitHubAccount(w http.ResponseWriter, r *http.Request) error {
	if err := s.manager(nil).Creds.RemoveGitHubAccount(r.PathValue("account")); err != nil {
		return err
	}
	s.pulls.reset()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) setDefaultGitHubAccount(w http.ResponseWriter, r *http.Request) error {
	if err := s.manager(nil).Creds.SetDefaultGitHubAccount(r.PathValue("account")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// renameGitHubAccount gives a stored GitHub account another name and carries
// every reference to it over: the machine default, each project's account and
// each agent's. The token doesn't change, so agents holding it keep running as
// they are.
func (s *Server) renameGitHubAccount(w http.ResponseWriter, r *http.Request) error {
	var req api.RenameGitHubAccountRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	old, name := r.PathValue("account"), strings.TrimSpace(req.Name)
	creds := s.manager(nil).Creds
	// The store's checks come first, so a refused name leaves the database
	// alone; the move itself runs again inside the transaction.
	if err := credentials.ValidateAccount(name); err != nil {
		return err
	}
	if taken, err := creds.HasGitHubAccount(name); err != nil {
		return err
	} else if taken && name != old {
		return fmt.Errorf("there is already a GitHub account named %q: remove it first, or pick another name", name)
	}
	moved := false
	done, err := s.store.RenameGitHubAccount(r.Context(), old, name, func() error {
		if err := creds.RenameGitHubAccount(old, name); err != nil {
			return err
		}
		moved = true
		return nil
	})
	if err != nil {
		if moved {
			// The transaction didn't commit, so the token goes back to the
			// name the database still has.
			if undo := creds.RenameGitHubAccount(name, old); undo != nil {
				s.logf("renaming the GitHub account %q back from %q: %v", old, name, undo)
			}
		}
		return err
	}
	s.logf("Renamed the GitHub account %q to %q", old, name)
	// Pull requests and the fleet say which account they were read with.
	s.pulls.reset()
	for _, p := range done.Projects {
		s.events.publish(api.EventProject, api.ProjectChange{Name: p})
	}
	out := api.RenamedGitHubAccount{Old: old, Name: name, Projects: done.Projects, Agents: done.Agents}
	if out.Projects == nil {
		out.Projects = []string{}
	}
	if out.Agents == nil {
		out.Agents = []string{}
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) authStatus(w http.ResponseWriter, _ *http.Request) error {
	creds := s.manager(nil).Creds
	claude, err := claudeAccounts(creds)
	if err != nil {
		return err
	}
	gh, err := githubAccounts(creds)
	if err != nil {
		return err
	}
	status := api.AuthStatus{
		Claude:         len(claude) > 0,
		Codex:          creds.HasCodexLogin(),
		OpenCode:       creds.HasOpenCodeLogin(),
		ClaudeAccounts: claude,
		GitHub:         len(gh) > 0,
		GitHubAccounts: gh,
	}
	s.refreshClaudeTokens(creds)
	if status.GitHub {
		// Who the default account's token belongs to, and whether GitHub
		// still accepts it. A stale token is worth saying so here rather than
		// in an agent.
		if token, err := creds.GitHubToken(""); err == nil && token != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if login, err := s.gitHub(token).Login(ctx); err != nil {
				status.GitHubError = err.Error()
			} else {
				status.GitHubUser = login
				s.rememberDefaultGitHubLogin(creds, gh, login)
			}
		}
	}
	return writeJSON(w, http.StatusOK, status)
}

// rememberDefaultGitHubLogin records who the default account is on GitHub,
// having just asked GitHub, and puts it on that account in the answer being
// built. It is the only place a login is learnt without a token being saved,
// and it costs nothing: the check above already made the call. An account
// stored before AgentBox kept logins has none until one of the two happens.
func (s *Server) rememberDefaultGitHubLogin(creds credentials.Store, accounts []api.GitHubAccount, login string) {
	for i := range accounts {
		if !accounts[i].Default || login == "" {
			continue
		}
		accounts[i].Login = login
		if err := creds.SaveGitHubLogin(accounts[i].Name, login); err != nil {
			s.logf("remembering the GitHub login of %q: %v", accounts[i].Name, err)
		}
	}
}

func claudeAccounts(creds credentials.Store) ([]api.ClaudeAccount, error) {
	stored, err := creds.ClaudeAccounts()
	if err != nil {
		return nil, err
	}
	accounts := make([]api.ClaudeAccount, 0, len(stored))
	for _, a := range stored {
		accounts = append(accounts, api.ClaudeAccount{Name: a.Name, Default: a.Default, SavedAt: a.SavedAt, Valid: string(a.Valid)})
	}
	return accounts, nil
}

// refreshClaudeTokens asks Anthropic about the accounts whose stored answer has
// aged out, in the background. The app polls this every few seconds and can't
// wait for the network, so the answer it gets is the last one written down and
// the next poll has the new one. The store keeps one check per account in
// flight, so polling can't pile them up.
func (s *Server) refreshClaudeTokens(creds credentials.Store) {
	accounts, err := creds.ClaudeAccounts()
	if err != nil {
		return
	}
	for _, a := range accounts {
		if !creds.ClaudeValidity(a.Name).Stale() {
			continue
		}
		go func(name string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			v, err := creds.CheckClaudeAccount(ctx, name)
			if err != nil {
				s.logf("checking the Claude Code token of %q: %v", name, err)
				return
			}
			if v.State == credentials.TokenRejected {
				s.logf("Anthropic rejected the Claude Code token of %q: %s", name, v.Detail)
			}
		}(a.Name)
	}
}

// claudeAuthFailed marks an agent's Claude Code account rejected when its own
// turn failed on its login. The token was refused where it is really used, so
// there is nothing to check: the app and `agentbox auth status` say so at once
// rather than at the next hourly check.
func (s *Server) claudeAuthFailed(a state.Agent, detail string) {
	if a.AI != "claude" {
		return
	}
	creds := s.manager(nil).Creds
	account, _ := creds.ClaudeAccountOf(a.ClaudeAccount)
	if err := creds.RejectClaudeAccount(a.ClaudeAccount, detail); err != nil {
		s.logf("recording that %s was refused: %v", a.Ref(), err)
		return
	}
	s.logf("%s was refused by Anthropic, so the Claude Code account %q is marked rejected: %s", a.Ref(), account, detail)
}

func githubAccounts(creds credentials.Store) ([]api.GitHubAccount, error) {
	stored, err := creds.GitHubAccounts()
	if err != nil {
		return nil, err
	}
	accounts := make([]api.GitHubAccount, 0, len(stored))
	for _, a := range stored {
		accounts = append(accounts, api.GitHubAccount{Name: a.Name, Default: a.Default, SavedAt: a.SavedAt, Login: a.Login})
	}
	return accounts, nil
}

// Jobs

func (s *Server) startJob(w http.ResponseWriter, kind, target string, fn func(context.Context, io.Writer) (any, error)) error {
	j, err := s.jobs.start(kind, target, func(ctx context.Context, log io.Writer) (any, error) {
		s.logf("job %s started", kind+" "+target)
		result, err := fn(ctx, log)
		if err != nil {
			s.logf("job %s failed: %v", kind+" "+target, err)
		}
		return result, err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, j.snapshot())
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) error {
	jobs, err := s.jobs.list(r.Context(), 50)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) error {
	info, _, err := s.jobs.lookup(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) jobLog(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	follow, _ := strconv.ParseBool(r.URL.Query().Get("follow"))
	if j, ok := s.jobs.get(id); ok && follow {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		j.follow(r.Context(), 0, func(line string) error {
			if _, err := io.WriteString(w, line+"\n"); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		})
		return nil
	}
	_, lines, err := s.jobs.lookup(r.Context(), id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, line := range lines {
		io.WriteString(w, line+"\n")
	}
	return nil
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	j, ok := s.jobs.get(id)
	if !ok {
		return fmt.Errorf("job %q isn't running in this daemon: %w", id, state.ErrNotFound)
	}
	j.cancel()
	// Wait for the rollback to finish, so the caller sees the final state.
	info, err := j.follow(r.Context(), math.MaxInt, func(string) error { return nil })
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}
