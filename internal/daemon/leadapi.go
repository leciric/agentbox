package daemon

import (
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
	srv := &http.Server{Handler: s.leadRoutes(project)}
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
// project, the daemon itself, or the host.
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
	mux.HandleFunc("GET /v1/project/accounts", withProject(s.leadAccounts))
	mux.HandleFunc("POST /v1/project/retire", withProject(s.retire))
	mux.HandleFunc("GET /v1/project/agents/{agent}/chat", withProject(s.leadAgentChat))
	mux.HandleFunc("POST /v1/project/agents/{agent}/chat/messages", withProject(s.leadTellAgent))
	mux.HandleFunc("GET /v1/project/agents/{agent}/diff", withProject(s.leadAgentDiff))
	mux.HandleFunc("POST /v1/project/agents/{agent}/run", withProject(s.leadRunInAgent))
	mux.HandleFunc("POST /v1/project/copy", withProject(s.leadCopy))
	mux.HandleFunc("GET /v1/project/secrets", withProject(s.leadSecrets))
	mux.HandleFunc("GET /v1/project/notes", withProject(s.getNotes))
	mux.HandleFunc("POST /v1/project/notes", withProject(s.appendNote))
	mux.HandleFunc("POST /v1/project/notes/edit", withProject(s.editNote))
	mux.HandleFunc("POST /v1/project/notes/remove", withProject(s.removeNote))
	// The project's memory: everything the user's routes offer, on the socket
	// that says which project it is.
	for _, route := range memoryRoutes {
		mux.HandleFunc(route.method+" /v1/project/memory"+route.path,
			withProject(s.memoryHandler(route.action, s.projectMemoryScope)))
	}
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
	return writeJSON(w, http.StatusOK, projectInfo(p))
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
	ctx := r.Context()
	project := r.PathValue("project")
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return err
	}
	m := s.manager(nil)
	accounts, err := m.Creds.ClaudeAccounts()
	if err != nil {
		return err
	}
	statuses, err := m.List(ctx, project)
	if err != nil {
		return err
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
		return err
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
	return writeJSON(w, http.StatusOK, out)
}

// leadAgentChat is one agent's conversation, so the chat can read what it did.
func (s *Server) leadAgentChat(w http.ResponseWriter, r *http.Request) error {
	a, err := s.leadAgent(r)
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
	item, err := s.chat.Send(a, req.Text)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, item)
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
