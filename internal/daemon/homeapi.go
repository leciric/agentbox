package daemon

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// The Home chat's API: the lead socket of state.HomeProject, served like a
// project's (serveLeadAPI) but reaching every project. Its routes are the
// user's own, at the same paths, so `agentbox mcp` reaches them with the
// client's ordinary methods — but only the few its tools need: it reads
// projects, their agents and their memory, keeps AgentBox-wide memory, makes
// agents, tells a project's lead, and adds projects. It can't change a project's settings, retire an
// agent or reach the daemon itself.
func (s *Server) homeRoutes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			s.projectBySlug(r)
			if err := fn(w, r); err != nil {
				writeError(w, err)
			}
		})
	}
	h("GET /v1/projects", s.listProjects)
	h("POST /v1/projects", s.homeAddProject)
	h("GET /v1/projects/{project}/fleet", s.fleet)
	h("POST /v1/projects/{project}/memory/search", s.memoryHandler("search", s.projectMemoryScope))
	// AgentBox-wide memory is the Home chat's own: what it remembers is for
	// every project, since it belongs to none.
	for _, route := range globalMemoryRoutes {
		if route.home {
			h(route.method+" /v1/global/memory"+route.path, s.memoryHandler(route.action, globalMemoryScope(state.HomeProject)))
		}
	}
	h("POST /v1/projects/{project}/lead/messages", s.homeTellLead)
	h("POST /v1/agents", s.homeCreateAgent)
	h("GET /v1/agents/{project}/{agent}/chat", s.leadAgentChat)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusForbidden, api.Error{Error: "the Home chat can't do that"})
	})
	return mux
}

// homeCreateAgent makes an agent in the project the Home chat names, held to
// the same choices a project's lead is (createAgentFrom's byLead).
func (s *Server) homeCreateAgent(w http.ResponseWriter, r *http.Request) error {
	var req api.CreateAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Project == "" || req.Project == state.HomeProject {
		return errors.New("name the project to create the agent in")
	}
	return s.createAgentFrom(w, r, req, true)
}

// homeTellLead passes the Home chat's message to a project's own chat, making
// that chat if the project has never had one, and wakes it. The notice is
// shown in the project's chat, so the user sees what was asked there too.
func (s *Server) homeTellLead(w http.ResponseWriter, r *http.Request) error {
	var req api.ChatMessageRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return errors.New("the message is empty")
	}
	ctx := r.Context()
	project := r.PathValue("project")
	if _, err := s.store.Project(ctx, project); err != nil {
		return err
	}
	m := s.manager(s.cfg.Log)
	lead, err := m.EnsureLead(ctx, project)
	if err != nil {
		return err
	}
	s.rolloverIfNeeded(ctx, lead)
	if lead, err = m.SyncLead(ctx, lead); err != nil {
		return err
	}
	if err := s.chat.Notice(lead, "From the user's Home chat:\n\n"+text, chat.NoticeOptions{Act: true}); err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, map[string]string{"project": project})
}

// homeAddProject adds a project for the Home chat: a folder on this machine,
// as POST /v1/projects does, or a repository cloned from a URL first.
func (s *Server) homeAddProject(w http.ResponseWriter, r *http.Request) error {
	var req api.HomeAddProjectRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	add := api.AddProjectRequest{Path: req.Path, Name: req.Name}
	if req.URL == "" {
		if req.Path == "" {
			return errors.New("give a folder on this machine, or a git URL to clone")
		}
		return s.addProjectFrom(w, r, add)
	}
	add.Name = strings.TrimSpace(add.Name)
	if add.Name == "" {
		add.Name = strings.TrimSuffix(path.Base(strings.TrimRight(req.URL, "/")), ".git")
	}
	if err := s.projectNameFree(r.Context(), add.Name); err != nil {
		return err
	}
	add.Path = req.Path
	if add.Path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		slug, err := s.store.ProjectSlug(r.Context(), add.Name)
		if err != nil {
			return err
		}
		add.Path = filepath.Join(home, "src", slug)
	}
	if err := s.manager(s.cfg.Log).CloneForHome(r.Context(), req.URL, add.Path); err != nil {
		return err
	}
	// What was cloned here goes again if it can't be added, so asking twice
	// doesn't find the folder in the way.
	if err := s.addProjectFrom(w, r, add); err != nil {
		_ = os.RemoveAll(add.Path)
		return err
	}
	return nil
}
