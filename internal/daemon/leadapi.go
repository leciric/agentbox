package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// The lead API. A project's lead runs on the host, so it can't be told apart by
// an Incus proxy device the way an agent is ([D17](decisions.md#d17)). It gets
// the same shape instead: one unix socket per project, mode 0600, serving only
// that project's routes. The lead never opens it itself — `agentbox mcp` does,
// started by Claude Code with AGENTBOX_SOCKET pointed at it, from a
// configuration file the lead cannot write.

// leadSocketPath is short (a hash of the project name) because unix socket
// paths are limited to 108 bytes.
func (s *Server) leadSocketPath(project string) string {
	sum := sha256.Sum256([]byte("lead:" + project))
	return filepath.Join(s.cfg.Paths.Data, "run", "leads", hex.EncodeToString(sum[:6])+".sock")
}

// serveLeadAPI starts the socket a project's lead reaches AgentBox through.
func (s *Server) serveLeadAPI(project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.leadAPIs[project]; ok {
		return nil
	}
	path := s.leadSocketPath(project)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	handler := s.leadRoutes(project)
	if project == state.HomeProject {
		handler = s.homeRoutes()
	}
	srv := &http.Server{Handler: handler}
	s.leadAPIs[project] = srv
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (s *Server) stopLeadAPI(project string) {
	s.mu.Lock()
	srv := s.leadAPIs[project]
	delete(s.leadAPIs, project)
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	_ = os.Remove(s.leadSocketPath(project))
}

func (s *Server) closeLeadAPIs() {
	s.mu.Lock()
	projects := make([]string, 0, len(s.leadAPIs))
	for project := range s.leadAPIs {
		projects = append(projects, project)
	}
	s.mu.Unlock()
	for _, project := range projects {
		s.stopLeadAPI(project)
	}
}

// leadRoutes are everything a lead may do, and nothing else: its own project's
// agents, its notes, and the questions they asked it. It cannot reach another
// project, the daemon itself, or the host, except to message another
// project's lead.
func (s *Server) leadRoutes(project string) http.Handler {
	mux := http.NewServeMux()
	// The lead's requests carry no project; the socket says which one it is.
	withProject := func(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("project", project)
			if err := fn(w, r); err != nil {
				writeError(w, err)
			}
		}
	}
	mux.HandleFunc("GET /v1/project", withProject(s.leadProject))
	mux.HandleFunc("GET /v1/project/settings", withProject(s.leadSettings))
	mux.HandleFunc("GET /v1/project/agents", withProject(s.fleet))
	mux.HandleFunc("POST /v1/project/agents", withProject(s.leadCreateAgent))
	mux.HandleFunc("POST /v1/project/groups", withProject(s.leadFanOut))
	mux.HandleFunc("GET /v1/project/groups", withProject(s.leadListGroups))
	mux.HandleFunc("GET /v1/project/groups/{id}", withProject(s.getGroup))
	mux.HandleFunc("POST /v1/project/groups/{id}/recommend", withProject(s.leadRecommend))
	mux.HandleFunc("GET /v1/project/accounts", withProject(s.leadAccounts))
	mux.HandleFunc("GET /v1/project/tool-accounts", withProject(s.leadToolAccounts))
	mux.HandleFunc("POST /v1/project/retire", withProject(s.retire))
	mux.HandleFunc("GET /v1/project/agents/{agent}/chat", withProject(s.leadAgentChat))
	mux.HandleFunc("GET /v1/project/archived-agents", withProject(s.listArchived))
	mux.HandleFunc("POST /v1/project/agents/{agent}/chat/messages", withProject(s.leadTellAgent))
	mux.HandleFunc("GET /v1/project/agents/{agent}/diff", withProject(s.leadAgentDiff))
	mux.HandleFunc("POST /v1/project/agents/{agent}/run", withProject(s.leadRunInAgent))
	mux.HandleFunc("GET /v1/project/agents/{agent}/checkpoints", withProject(s.listCheckpoints))
	mux.HandleFunc("POST /v1/project/agents/{agent}/rollback", withProject(s.rollback))
	mux.HandleFunc("POST /v1/project/agents/{agent}/fork", withProject(s.fork))
	mux.HandleFunc("POST /v1/project/copy", withProject(s.leadCopy))
	mux.HandleFunc("POST /v1/project/copy-from-host", withProject(s.leadCopyFromHost))
	mux.HandleFunc("GET /v1/project/secrets", withProject(s.leadSecrets))
	// Other projects' leads (projectmessages.go): the one boundary a lead
	// crosses, and only to talk, lead to lead.
	mux.HandleFunc("GET /v1/project/threads", withProject(s.leadProjectThreads))
	mux.HandleFunc("POST /v1/project/threads", withProject(s.leadSendToProject))
	mux.HandleFunc("POST /v1/project/threads/{thread}/replies", withProject(s.leadReplyToProject))
	mux.HandleFunc("POST /v1/project/threads/{thread}/settle", withProject(s.leadSettleAlignment))
	// The project's connectors: listed for list_connectors, and relayed to
	// for the chat's own Claude Code, which is given them as MCP servers.
	mux.HandleFunc("GET /v1/project/connectors", withProject(func(w http.ResponseWriter, r *http.Request) error {
		return s.listConnectors(w, r, s.projectConnectorScope)
	}))
	mux.HandleFunc("GET /v1/self/connectors", withProject(s.leadConnectors))
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		mux.HandleFunc(method+" /v1/self/connectors/{name}/mcp", s.leadConnectorMCP(project))
	}
	mux.HandleFunc("GET /v1/project/notes", withProject(s.getNotes))
	mux.HandleFunc("POST /v1/project/notes", withProject(s.appendNote))
	mux.HandleFunc("POST /v1/project/notes/edit", withProject(s.editNote))
	mux.HandleFunc("POST /v1/project/notes/remove", withProject(s.removeNote))
	// The project's memory: everything the user's routes offer but tidying,
	// on the socket that says which project it is.
	for _, route := range memoryRoutes {
		if route.userOnly {
			continue
		}
		mux.HandleFunc(route.method+" /v1/project/memory"+route.path,
			withProject(s.memoryHandler(route.action, s.leadMemoryScope)))
	}
	// Skills: reading and adding them is the lead's; changing, deleting or
	// turning off one waits for the user's approval (leadskills.go).
	mux.HandleFunc("GET /v1/project/skills", withProject(s.listProjectSkills))
	mux.HandleFunc("POST /v1/project/skills", withProject(s.leadCreateSkill))
	mux.HandleFunc("GET /v1/project/skills/{name}", withProject(s.getSkill))
	mux.HandleFunc("PUT /v1/project/skills/{name}", withProject(s.leadEditSkill))
	mux.HandleFunc("DELETE /v1/project/skills/{name}", withProject(s.leadRemoveSkill))
	mux.HandleFunc("PUT /v1/project/skills/{name}/switch", withProject(s.leadSwitchSkill))
	// Scheduled tasks (schedules.go): the lead's schedule_task and the rest.
	s.scheduleRoutes("/v1/project", "lead", func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, withProject(fn))
	})
	mux.HandleFunc("GET /v1/project/questions", withProject(s.leadQuestions))
	mux.HandleFunc("POST /v1/project/questions/{id}/answer", withProject(s.leadAnswerQuestion))
	mux.HandleFunc("POST /v1/project/questions/{id}/escalate", withProject(s.leadEscalateQuestion))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusForbidden, api.Error{Error: "a project's chat can only reach its own project"})
	})
	return mux
}

// leadAgent finds one agent of the lead's project, refusing the lead itself.
func (s *Server) leadAgent(r *http.Request) (state.Agent, error) {
	a, err := s.store.Agent(r.Context(), r.PathValue("project"), r.PathValue("agent"))
	if err != nil {
		return state.Agent{}, err
	}
	if a.IsLead() {
		return state.Agent{}, errNotAnAgent
	}
	return a, nil
}

// leadProject describes the project to its chat.
func (s *Server) leadProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.projectInfo(r.Context(), p))
}

// leadSettings tells a project's chat what new agents start on, and which
// models and effort levels Claude Code last advertised for this account. Read
// only: a chat may choose settings for the agent it is about to make, not
// change what every other agent starts on.
//
// It exists so `agentbox mcp` can describe its own create_agent parameters
// with this account's real choices instead of a list AgentBox made up. Nothing
// here is new to the lead — it is Claude Code, running on that very account.
func (s *Server) leadSettings(w http.ResponseWriter, r *http.Request) error {
	out, err := s.currentSettings(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// leadCreateAgent makes an agent for a task the chat decided on, and gives it
// the task as its first message.
func (s *Server) leadCreateAgent(w http.ResponseWriter, r *http.Request) error {
	var req api.CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Project = r.PathValue("project")
	return s.createAgentFrom(w, r, req, true)
}

// leadAccounts answers GET /v1/project/accounts: this machine's Claude Code
// accounts, so a project's chat can spread its agents across them without
// ever seeing a token. Each account's limit reading is converted the same way
// GET /v1/limits converts one (toClaudeLimit, limits.go), so the two never
// disagree about what a reading means.
func (s *Server) leadAccounts(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	out, err := s.leadAccountRows(r.Context(), p)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// leadToolAccounts are the machine's Codex, OpenCode and Cursor accounts, for
// create_agent's codex_account, opencode_accounts and cursor_account.
func (s *Server) leadToolAccounts(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.store.Project(r.Context(), r.PathValue("project")); err != nil {
		return err
	}
	out := []api.ToolAccount{}
	for _, tool := range credentials.AccountTools {
		accounts, err := toolAccounts(s.manager(nil).Creds, tool)
		if err != nil {
			return err
		}
		out = append(out, accounts...)
	}
	return writeJSON(w, http.StatusOK, out)
}

// leadAccountRows are the Claude Code accounts a project may use, each with
// how many of its agents run on it and its latest limit reading.
func (s *Server) leadAccountRows(ctx context.Context, p state.Project) ([]api.LeadAccount, error) {
	project := p.Name
	m := s.manager(nil)
	accounts, err := m.Creds.ClaudeAccounts()
	if err != nil {
		return nil, err
	}
	// How many agents run on each account is worth knowing, not worth the
	// list: with Incus unreachable, the accounts are still the accounts.
	statuses, err := m.List(ctx, project)
	if err != nil {
		s.logf("counting %s's agents per Claude Code account: %v", project, err)
	}
	agentsOn := map[string]int{}
	for _, st := range statuses {
		if st.IsLead() || st.AI != "claude" || st.State != "running" {
			continue
		}
		agentsOn[st.ClaudeAccount]++
	}
	readings, err := s.store.ClaudeLimits(ctx)
	if err != nil {
		return nil, err
	}
	byAccount := make(map[string]state.ClaudeLimitReading, len(readings))
	for _, reading := range readings {
		byAccount[reading.Account] = reading
	}
	// A project's own account is stored as "" when it defers to the machine's
	// default, the same fallback ClaudeAccountOf already applies when an agent
	// is made, so this asks it rather than repeating the rule.
	projectAccount, err := m.Creds.ClaudeAccountOf(p.ClaudeAccount)
	if err != nil {
		projectAccount = ""
	}
	out := make([]api.LeadAccount, 0, len(accounts))
	for _, a := range accounts {
		// Only what the lead may hand out: an account it can't use is
		// text in its context for nothing.
		if len(p.ClaudeAccounts) > 0 && !slices.Contains(p.ClaudeAccounts, a.Name) {
			continue
		}
		row := api.LeadAccount{Name: a.Name, Default: a.Default, Project: a.Name == projectAccount, Agents: agentsOn[a.Name]}
		if reading, ok := byAccount[a.Name]; ok {
			if limit, ok := toClaudeLimit(reading, a.Default); ok {
				row.Limit = &limit
			}
		}
		out = append(out, row)
	}
	slices.SortStableFunc(out, func(a, b api.LeadAccount) int {
		switch {
		case a.Default && !b.Default:
			return -1
		case b.Default && !a.Default:
			return 1
		}
		return 0
	})
	return out, nil
}

// leadAgentChat is one agent's conversation, so the chat can read what it did.
func (s *Server) leadAgentChat(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadAgent(r)
	if errors.Is(err, state.ErrNotFound) {
		// A past agent's chat is still there to read.
		if past, perr := s.archivedFromPath(r); perr == nil {
			a, err = past.AsAgent(), nil
		}
	}
	if err != nil {
		return err
	}
	thread, err := s.chat.Thread(a)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, thread)
}

// leadTellAgent sends an agent a message: a new task, an answer, a correction.
func (s *Server) leadTellAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadAgent(r)
	if err != nil {
		return err
	}
	var req api.ChatMessageRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	s.leadAsked(a)
	t, err := s.tellAgent(r.Context(), a, req.Text)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, api.TellResult{ChatItem: t.item, Woke: t.woke})
}

func (s *Server) leadAgentDiff(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadAgent(r)
	if err != nil {
		return err
	}
	// ?path= narrows it to files or directories, so a chat looking at one
	// part of a large change needn't read all of it (D87).
	diff, err := s.manager(nil).Diff(a, r.URL.Query().Get("stat") == "true", r.URL.Query()["path"]...)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
}

// leadRunInAgent runs a command in one of the project's agents' machines, for
// the chat, and gives it back the tail of what it printed (D89).
func (s *Server) leadRunInAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadAgent(r)
	if err != nil {
		return err
	}
	var req api.LeadRunRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	res, err := s.manager(nil).RunForLead(r.Context(), a, req.Command, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.LeadRunResult{ExitCode: res.ExitCode, Output: res.Output, Bytes: res.Bytes, TimedOut: res.TimedOut})
}

// leadCopy copies a file or directory between two of the project's agents'
// machines, for the chat (D89).
func (s *Server) leadCopy(w http.ResponseWriter, r *http.Request) error {
	var req api.LeadCopyRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	project := r.PathValue("project")
	var ends [2]state.Agent
	for i, name := range []string{req.From, req.To} {
		if name == "" {
			return errors.New("name both agents: the one to copy from and the one to copy to")
		}
		a, err := s.store.Agent(r.Context(), project, name)
		if err != nil {
			return err
		}
		if a.IsLead() {
			return errNotAnAgent
		}
		ends[i] = a
	}
	n, err := s.manager(nil).CopyForLead(r.Context(), ends[0], req.Path, ends[1], req.Into, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.LeadCopyResult{Bytes: n})
}

// leadCopyFromHost copies a file or directory out of one of the user's
// projects' repositories into one of this project's agents' machines, for a
// chat that was pointed at another project with an @mention.
func (s *Server) leadCopyFromHost(w http.ResponseWriter, r *http.Request) error {
	var req api.LeadHostCopyRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.To == "" {
		return errors.New("name the agent to copy to")
	}
	a, err := s.store.Agent(r.Context(), r.PathValue("project"), req.To)
	if err != nil {
		return err
	}
	if a.IsLead() {
		return errNotAnAgent
	}
	projects, err := s.store.Projects(r.Context())
	if err != nil {
		return err
	}
	roots := make([]string, len(projects))
	for i, p := range projects {
		roots[i] = p.Root
	}
	n, err := s.manager(nil).CopyHostForLead(r.Context(), roots, req.Path, a, req.Into, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.LeadCopyResult{Bytes: n})
}
