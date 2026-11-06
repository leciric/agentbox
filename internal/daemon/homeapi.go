package daemon

import (
	"cmp"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The Home chat's API: the lead socket of state.HomeProject, served like a
// project's (serveLeadAPI) but reaching every project. Its routes are the
// user's own, at the same paths, so `agentbox mcp` reaches them with the
// client's ordinary methods — but only the few its tools need: it reads
// projects, their agents and their memory, keeps AgentBox-wide memory, makes
// agents, asks a project's lead, and adds projects. It can't change a project's settings, retire an
// agent or reach the daemon itself. Each of the chat's sessions has its own
// socket, and session is the one this is: what it asks a project's lead is
// answered there (homeTellLead).
func (s *Server) homeRoutes(session string) http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			s.projectBySlug(r)
			r = r.WithContext(withLeadSession(r.Context(), session))
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
	h("PUT /v1/agents/{project}/{agent}/goal", s.putGoal(s.agentFromPath))
	h("DELETE /v1/agents/{project}/{agent}/goal", s.deleteGoal(s.agentFromPath))
	h("GET /v1/archived-agents", s.listArchived)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusForbidden, api.Error{Error: "the Home chat can't do that"})
	})
	return traceAPI("home", mux, false)
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

// homeTellLead asks a project's lead something for the Home chat, and the
// answer comes back to the Home chat's session that asked. It is a thread
// like one between two projects' leads (projectmessages.go), started by
// state.HomeProject, with both ends' sessions pinned: the lead's session most
// recently active hears it (one is opened for it in a project that has none,
// making its chat if it never had one), answers it from there with
// message_project reply, and the answer wakes the asking session, whether the
// Home chat runs in the VM or on the user's computer. Each chat shows the
// question and the answer as cards, and the lead reads the question fenced.
// Unlike two leads' first message, it needs no approval: the Home chat is the
// user's own.
func (s *Server) homeTellLead(w http.ResponseWriter, r *http.Request) error {
	var req api.ChatMessageRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	project := r.PathValue("project")
	if project == state.HomeProject {
		return errors.New("that is the Home chat itself: name a project")
	}
	if _, err := s.store.Project(ctx, project); err != nil {
		return err
	}
	text, err := projectMessageText(req.Text, maxProjectMessage)
	if err != nil {
		return err
	}
	lead, err := s.eventLead(ctx, project, eventTopic(text), true)
	if err != nil {
		return err
	}
	s.rolloverIfNeeded(ctx, lead)
	if lead, err = s.manager(s.cfg.Log).SyncLead(ctx, lead); err != nil {
		return err
	}
	now := time.Now()
	t := state.ProjectThread{
		ID: newID(), Project: state.HomeProject, Peer: project, Kind: state.ThreadMessage, Status: state.ThreadOpen,
		ProjectSession: cmp.Or(leadSessionOf(ctx), state.MigratedSession), PeerSession: lead.Session(),
		CreatedAt: now, UpdatedAt: now,
	}
	m := state.ProjectMessage{
		ID: newID(), Thread: t.ID, Project: state.HomeProject, Peer: project, Text: text, ExpectsReply: true, CreatedAt: now,
	}
	if err := s.store.AddProjectThread(ctx, t, m); err != nil {
		return err
	}
	s.deliverProjectMessage(ctx, t, m, api.ProjectMessageSent)
	return writeJSON(w, http.StatusAccepted, s.toAPIThread(ctx, state.HomeProject, t))
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
	add, err := s.cloneRequest(r.Context(), strings.TrimSpace(req.URL), add)
	if err != nil {
		return err
	}
	p, err := s.cloneAndAdd(r.Context(), strings.TrimSpace(req.URL), add, io.Discard)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, s.projectInfo(r.Context(), p))
}
