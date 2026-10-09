package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/connectors"
	"agentbox/internal/state"
)

// The connectors routes: remote MCP servers the daemon signs in to and relays
// to from inside agents (internal/connectors). Like the secrets routes, reading
// gives what a connector is and where it stands, never a token.

// newConnectors is the daemon's one connectors service. Every change is
// published, and one to what an agent is given rewrites its MCP servers.
func (s *Server) newConnectors() *connectors.Service {
	return &connectors.Service{
		State:    s.store,
		Secrets:  s.secrets(),
		OAuth:    connectors.OAuth{ClientName: "AgentBox", Loopback: s.cfg.ConnectorsLoopback},
		OnChange: s.connectorChanged,
		Callback: s.connectorCallback,
	}
}

// connectorCallback is where a connector's sign-in sends the browser back
// to: the preview proxy, whose port the host's browser reaches wherever the
// daemon runs (connectors/flow.go), or "" while it isn't listening on
// loopback.
func (s *Server) connectorCallback() string {
	s.mu.Lock()
	addr := s.previewAddr
	s.mu.Unlock()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified()) {
		return ""
	}
	return "http://127.0.0.1:" + port + connectors.CallbackPath
}

// connectorRoutes are the same five routes for a project's connectors and
// for one agent's own.
func (s *Server) connectorRoutes(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, scope := range []struct {
		prefix string
		of     func(*http.Request) (project, agent string, err error)
	}{
		{"/v1/projects/{project}/connectors", s.projectConnectorScope},
		{"/v1/agents/{project}/{agent}/connectors", s.agentConnectorScope},
	} {
		of := scope.of
		h("GET "+scope.prefix, func(w http.ResponseWriter, r *http.Request) error { return s.listConnectors(w, r, of) })
		h("GET "+scope.prefix+"/{name}", func(w http.ResponseWriter, r *http.Request) error { return s.getConnector(w, r, of) })
		h("PUT "+scope.prefix+"/{name}", func(w http.ResponseWriter, r *http.Request) error { return s.setConnector(w, r, of) })
		h("DELETE "+scope.prefix+"/{name}", func(w http.ResponseWriter, r *http.Request) error { return s.removeConnector(w, r, of) })
		h("POST "+scope.prefix+"/{name}/connect", func(w http.ResponseWriter, r *http.Request) error { return s.connectConnector(w, r, of) })
		h("POST "+scope.prefix+"/{name}/disconnect", func(w http.ResponseWriter, r *http.Request) error { return s.disconnectConnector(w, r, of) })
	}
}

func (s *Server) projectConnectorScope(r *http.Request) (string, string, error) {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	return p.Name, "", err
}

// agentConnectorScope refuses a project's lead: it runs on the host, with no
// machine for a connector to be relayed into.
func (s *Server) agentConnectorScope(r *http.Request) (string, string, error) {
	a, err := s.store.Agent(r.Context(), r.PathValue("project"), r.PathValue("agent"))
	if err != nil {
		return "", "", err
	}
	if a.IsLead() {
		return "", "", fmt.Errorf("%s is the project's chat, which runs on this machine rather than in an agent: give the connector to the project instead", a.Ref())
	}
	return a.Project, a.Name, nil
}

// listConnectors is a scope's connectors. An agent's list is everything it
// is given: its project's that reach it, then its own.
func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	var found []state.Connector
	if agent == "" {
		found, err = s.store.Connectors(r.Context(), project, "")
	} else {
		found, err = s.store.AgentConnectors(r.Context(), project, agent)
	}
	if err != nil {
		return err
	}
	out := make([]api.Connector, 0, len(found))
	for _, c := range found {
		info, err := s.connectorInfo(r.Context(), c)
		if err != nil {
			return err
		}
		out = append(out, info)
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) getConnector(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	c, err := s.store.Connector(r.Context(), project, agent, r.PathValue("name"))
	if err != nil {
		return err
	}
	info, err := s.connectorInfo(r.Context(), c)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) setConnector(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	var req api.SetConnectorRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	c, err := s.connectors.Set(r.Context(), project, agent, strings.TrimSpace(r.PathValue("name")), req)
	if err != nil {
		return err
	}
	s.logf("connector %s set for %s (%s)", c.Name, scopeRef(project, agent), connectors.Redact(c.URL))
	info, err := s.connectorInfo(r.Context(), c)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) removeConnector(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	if err := s.connectors.Remove(r.Context(), project, agent, r.PathValue("name")); err != nil {
		return err
	}
	s.logf("connector %s removed from %s", r.PathValue("name"), scopeRef(project, agent))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// connectConnector starts a sign-in and answers with the page to open. The
// sign-in ends when the browser comes back, which publishes EventConnector.
func (s *Server) connectConnector(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	res, c, err := s.connectors.Connect(r.Context(), project, agent, r.PathValue("name"))
	if err != nil {
		return err
	}
	s.logf("connector %s of %s: signing in, waiting for the browser at %s", c.Name, scopeRef(project, agent), res.RedirectURI)
	if res.Connector, err = s.connectorInfo(r.Context(), c); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, res)
}

func (s *Server) disconnectConnector(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	c, err := s.connectors.Disconnect(r.Context(), project, agent, r.PathValue("name"))
	if err != nil {
		return err
	}
	s.logf("connector %s of %s disconnected", c.Name, scopeRef(project, agent))
	info, err := s.connectorInfo(r.Context(), c)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

// connectorInfo is a connector as the API shows it, with the agents it
// reaches: a project connector reaches every agent of the project that has no
// connector of its own by that name, and whose limit lets it through.
func (s *Server) connectorInfo(ctx context.Context, c state.Connector) (api.Connector, error) {
	var refs []string
	if c.Agent != "" {
		refs = []string{c.Project + "/" + c.Agent}
	} else {
		agents, err := s.store.Agents(ctx, c.Project)
		if err != nil {
			return api.Connector{}, err
		}
		for _, a := range agents {
			if a.IsLead() || !a.GetsConnector(c.Name) {
				continue
			}
			if _, err := s.store.Connector(ctx, a.Project, a.Name, c.Name); err == nil {
				continue
			} else if !errors.Is(err, state.ErrNotFound) {
				return api.Connector{}, err
			}
			refs = append(refs, a.Ref())
		}
	}
	return s.connectors.Info(ctx, c, refs), nil
}

// connectorChanged publishes a change, and when it changes what an agent is
// given — added, removed, turned on or off — rewrites the MCP servers of the
// running agents it reaches. That is slow (a few incus calls per agent), so
// it happens off the request.
func (s *Server) connectorChanged(c state.Connector, removed bool) {
	ctx := context.Background()
	info := api.Connector{Name: c.Name, Scope: c.Scope(), Project: c.Project, Agent: c.Agent, Removed: true, Agents: []string{}}
	if !removed {
		var err error
		if info, err = s.connectorInfo(ctx, c); err != nil {
			s.logf("connector %s: %v", c.Name, err)
			return
		}
	}
	s.events.publish(api.EventConnector, info)
	if !removed {
		// A sign-in finishing, or a connector turned on, may be what an
		// agent's request is waiting for.
		s.resolveConnectorRequests(ctx, c.Project)
	}
	given := !removed && c.Enabled
	s.mu.Lock()
	k := scopeRef(c.Project, c.Agent) + "\x00" + c.Name
	was, known := s.connectorsGiven[k]
	s.connectorsGiven[k] = given
	if removed {
		delete(s.connectorsGiven, k)
	}
	s.mu.Unlock()
	if known && was == given {
		return
	}
	// A connector the daemon hasn't seen since it started may or may not be
	// in the agents' configuration already: rewriting it is harmless.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.manager(nil).SyncConnectors(ctx, c.Project, c.Agent); err != nil {
			s.logf("connector %s: %v", c.Name, err)
		}
		// Running chats read MCP servers only as they start.
		s.chat.ToolsChanged(c.Project, c.Agent)
	}()
}

func scopeRef(project, agent string) string {
	if agent == "" {
		return project
	}
	return project + "/" + agent
}

// connectorRefreshInterval is how often the sweep looks for tokens about to
// expire.
const connectorRefreshInterval = 5 * time.Minute

// refreshConnectors renews tokens before they expire, so a tool call rarely
// waits on a refresh.
func (s *Server) refreshConnectors(ctx context.Context) {
	ticker := time.NewTicker(connectorRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.connectors.RefreshDue(ctx); err != nil {
				s.logf("refreshing connectors: %v", err)
			}
		}
	}
}

// selfConnectors is what an agent is given, on its own socket: names and
// status, for `agentbox connector list` inside it.
func (s *Server) selfConnectors(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		found, err := s.store.AgentConnectors(r.Context(), a.Project, a.Name)
		if err != nil {
			return err
		}
		out := []api.SelfConnector{}
		for _, c := range found {
			if !c.Enabled {
				continue
			}
			status, why := s.connectors.Status(r.Context(), c, a.Name)
			out = append(out, api.SelfConnector{Name: c.Name, URL: c.URL, Status: status, Error: why})
		}
		return writeJSON(w, http.StatusOK, out)
	}
}

// selfConnectorMCP relays an agent's MCP traffic to one of its connectors.
// The agent is known from the socket, and only its own connectors — its
// project's and its own — are reachable.
func (s *Server) selfConnectorMCP(instance string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			connectors.RelayError(w, http.StatusNotFound, err.Error())
			return
		}
		name := r.PathValue("name")
		found, err := s.store.AgentConnectors(r.Context(), a.Project, a.Name)
		if err != nil {
			connectors.RelayError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, c := range found {
			if c.Name == name {
				s.connectors.Proxy(w, r, c, a.Name)
				return
			}
		}
		connectors.RelayError(w, http.StatusNotFound, fmt.Sprintf("%s has no connector %q", a.Ref(), name))
	}
}

// leadConnectors is what a project's chat is given, on its own socket: its
// project's enabled connectors, for `agentbox connector list` there.
func (s *Server) leadConnectors(w http.ResponseWriter, r *http.Request) error {
	found, err := s.store.Connectors(r.Context(), r.PathValue("project"), "")
	if err != nil {
		return err
	}
	out := []api.SelfConnector{}
	for _, c := range found {
		if !c.Enabled {
			continue
		}
		status, why := s.connectors.Status(r.Context(), c, "")
		out = append(out, api.SelfConnector{Name: c.Name, URL: c.URL, Status: status, Error: why})
	}
	return writeJSON(w, http.StatusOK, out)
}

// leadConnectorMCP relays a project's chat's MCP traffic to one of its
// project's connectors: the same relay an agent's AI tools start, pointed at
// the lead's socket (agent.leadMCPServers). A project connector's secret is the
// project's.
func (s *Server) leadConnectorMCP(project string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		c, err := s.store.Connector(r.Context(), project, "", name)
		switch {
		case errors.Is(err, state.ErrNotFound):
			connectors.RelayError(w, http.StatusNotFound, fmt.Sprintf("%s has no connector %q", project, name))
			return
		case err != nil:
			connectors.RelayError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.connectors.Proxy(w, r, c, "")
	}
}
