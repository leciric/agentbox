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

// connectorRoutes are the same five routes for the AgentBox-wide connectors,
// a project's and one agent's own, and a project's override of an
// AgentBox-wide one.
func (s *Server) connectorRoutes(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	for _, scope := range []struct {
		prefix string
		of     func(*http.Request) (project, agent string, err error)
	}{
		{"/v1/connectors", wideConnectorScope},
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
	h("PUT /v1/projects/{project}/connectors/{name}/override", s.setConnectorOverride)
}

func wideConnectorScope(*http.Request) (string, string, error) { return "", "", nil }

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

// listConnectors is a scope's connectors. A project's list is its own and the
// AgentBox-wide ones it doesn't replace, as it has them; an agent's is
// everything it is given: its project's that reach it, then its own.
func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	var found []state.Connector
	switch {
	case project == "":
		found, err = s.store.Connectors(r.Context(), "", "")
	case agent == "":
		found, err = s.store.ProjectConnectors(r.Context(), project)
	default:
		found, err = s.store.AgentConnectors(r.Context(), project, agent)
	}
	if err != nil {
		return err
	}
	out := make([]api.Connector, 0, len(found))
	for _, c := range found {
		info, err := s.connectorInfoIn(r.Context(), c, project)
		if err != nil {
			return err
		}
		out = append(out, info)
	}
	return writeJSON(w, http.StatusOK, out)
}

// getConnector is one connector of a scope. A project's may be an
// AgentBox-wide one it gets, as it has it.
func (s *Server) getConnector(w http.ResponseWriter, r *http.Request, of func(*http.Request) (string, string, error)) error {
	project, agent, err := of(r)
	if err != nil {
		return err
	}
	var c state.Connector
	if project != "" && agent == "" {
		c, err = s.store.ProjectConnector(r.Context(), project, r.PathValue("name"))
	} else {
		c, err = s.store.Connector(r.Context(), project, agent, r.PathValue("name"))
	}
	if err != nil {
		return err
	}
	info, err := s.connectorInfoIn(r.Context(), c, project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

// setConnectorOverride is a project's say on an AgentBox-wide connector: on,
// off, or none, to follow the AgentBox-wide switch. Its agents and its chat
// get the change like any other.
func (s *Server) setConnectorOverride(w http.ResponseWriter, r *http.Request) error {
	project, name := r.PathValue("project"), r.PathValue("name")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.ConnectorOverrideRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	on, err := parseOverride(req.Override)
	if err != nil {
		return err
	}
	if err := s.overrideConnector(r.Context(), project, name, on); err != nil {
		return err
	}
	c, err := s.store.ProjectConnector(r.Context(), project, name)
	if err != nil {
		return err
	}
	info, err := s.connectorInfoIn(r.Context(), c, project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

// overrideConnector stores a project's say on an AgentBox-wide connector,
// publishes it, and gives the project's running agents and chat the change.
func (s *Server) overrideConnector(ctx context.Context, project, name string, on *bool) error {
	if err := s.store.SetConnectorOverride(ctx, name, project, on); err != nil {
		return err
	}
	override := ""
	if on != nil {
		override = onOff(*on)
	}
	s.logf("connector %s in %s: %s", name, project, cmpOr(override, "as AgentBox-wide"))
	c, err := s.store.ProjectConnector(ctx, project, name)
	if err != nil {
		return err
	}
	if info, err := s.connectorInfoIn(ctx, c, project); err == nil {
		s.events.publish(api.EventConnector, info)
	}
	s.resolveConnectorRequests(ctx, project)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.manager(nil).SyncConnectors(ctx, project, ""); err != nil {
			s.logf("connector %s: %v", name, err)
		}
		s.chat.ToolsChanged(project, "")
	}()
	return nil
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
	name := strings.TrimSpace(r.PathValue("name"))
	if req.SecretValue != "" {
		if err := s.setWideConnectorSecret(r.Context(), project, name, req); err != nil {
			return err
		}
	}
	c, err := s.connectors.Set(r.Context(), project, agent, name, req)
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

// setWideConnectorSecret stores the value an AgentBox-wide connector sends,
// before the connector, so it is connected as soon as it is there. A
// project's or an agent's connector sends a secret of its own scope, set
// through the secrets routes, which give it to the agents too.
func (s *Server) setWideConnectorSecret(ctx context.Context, project, name string, req api.SetConnectorRequest) error {
	switch {
	case project != "":
		return fmt.Errorf("secretValue is for an AgentBox-wide connector: set %s with agentbox secrets set, and name it in secret", cmpOr(req.Secret, "the secret"))
	case req.Auth != "" && req.Auth != api.ConnectorSecret, req.Secret == "":
		return errors.New("secretValue is the value of the secret a connector sends: give secret, its name, too")
	}
	if err := connectors.ValidateName(name); err != nil {
		return err
	}
	if err := s.connectors.SetSecret(ctx, req.Secret, req.SecretValue); err != nil {
		return err
	}
	s.logf("connector %s: AgentBox-wide secret %s set", name, req.Secret)
	return nil
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
// connector of its own by that name, and whose limit lets it through; an
// AgentBox-wide one, those of every project it is on in that has none of its
// own by that name either.
func (s *Server) connectorInfo(ctx context.Context, c state.Connector) (api.Connector, error) {
	return s.connectorInfoIn(ctx, c, "")
}

// connectorInfoIn is connectorInfo as a project's list shows it: an
// AgentBox-wide connector there reaches that project's agents only, and says
// what the project overrides. c is as the project has it (ProjectConnectors).
func (s *Server) connectorInfoIn(ctx context.Context, c state.Connector, project string) (api.Connector, error) {
	var refs []string
	switch {
	case c.Agent != "":
		refs = []string{c.Project + "/" + c.Agent}
	case !c.Wide():
		var err error
		if refs, err = s.connectorReach(ctx, c.Project, c.Name); err != nil {
			return api.Connector{}, err
		}
	case project != "":
		if c.Enabled {
			var err error
			if refs, err = s.connectorReach(ctx, project, c.Name); err != nil {
				return api.Connector{}, err
			}
		}
	default:
		projects, err := s.store.Projects(ctx)
		if err != nil {
			return api.Connector{}, err
		}
		for _, p := range projects {
			if !c.EnabledFor(p.Name) {
				continue
			}
			if _, err := s.store.Connector(ctx, p.Name, "", c.Name); err == nil {
				continue // the project's own replaces it
			} else if !errors.Is(err, state.ErrNotFound) {
				return api.Connector{}, err
			}
			in, err := s.connectorReach(ctx, p.Name, c.Name)
			if err != nil {
				return api.Connector{}, err
			}
			refs = append(refs, in...)
		}
	}
	info := s.connectors.Info(ctx, c, refs)
	if c.Wide() && project != "" {
		if on, ok := c.Overrides[project]; ok {
			info.Override = onOff(on)
		}
		info.Overrides = nil
	}
	return info, nil
}

// connectorReach is the agents of a project a connector of the project's, or
// an AgentBox-wide one, reaches: those whose limit lets it through, with no
// connector of their own by that name.
func (s *Server) connectorReach(ctx context.Context, project, name string) ([]string, error) {
	agents, err := s.store.Agents(ctx, project)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, a := range agents {
		if a.IsLead() || !a.GetsConnector(name) {
			continue
		}
		if _, err := s.store.Connector(ctx, a.Project, a.Name, name); err == nil {
			continue
		} else if !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		refs = append(refs, a.Ref())
	}
	return refs, nil
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
	projects := []string{c.Project}
	if c.Wide() {
		all, err := s.store.Projects(ctx)
		if err != nil {
			s.logf("connector %s: %v", c.Name, err)
			return
		}
		projects = projects[:0]
		for _, p := range all {
			projects = append(projects, p.Name)
		}
	}
	if !removed {
		// A sign-in finishing, or a connector turned on, may be what an
		// agent's request is waiting for.
		for _, project := range projects {
			s.resolveConnectorRequests(ctx, project)
		}
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
		for _, project := range projects {
			if err := s.manager(nil).SyncConnectors(ctx, project, c.Agent); err != nil {
				s.logf("connector %s: %v", c.Name, err)
			}
			// Running chats read MCP servers only as they start.
			s.chat.ToolsChanged(project, c.Agent)
		}
	}()
}

func scopeRef(project, agent string) string {
	switch {
	case project == "":
		return "AgentBox"
	case agent == "":
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
// project's enabled connectors, AgentBox-wide ones included, for `agentbox
// connector list` there.
func (s *Server) leadConnectors(w http.ResponseWriter, r *http.Request) error {
	found, err := s.store.ProjectConnectors(r.Context(), r.PathValue("project"))
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
		c, err := s.store.ProjectConnector(r.Context(), project, name)
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
