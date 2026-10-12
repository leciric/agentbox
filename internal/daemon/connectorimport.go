package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/connectors"
	"agentbox/internal/state"
)

// Importing the MCP servers the user's own AI tools have as connectors
// (connectors/found.go). A scan reads the tools' configurations in the user's
// home, the same homes their skills are looked for in; an import scans again
// and adds what was picked, so a header's value goes from the tool's file
// into a secret without passing through the app.

// probeTimeout bounds asking a server whether it wants a sign-in.
const probeTimeout = 10 * time.Second

func (s *Server) scanConnectors(w http.ResponseWriter, r *http.Request) error {
	var req api.ScanConnectorsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	found, unreadable, err := s.connectorCandidates(r.Context(), req.Project)
	if err != nil {
		return err
	}
	out := api.ConnectorScan{Candidates: make([]api.ConnectorCandidate, 0, len(found)), Unreadable: []api.ConnectorUnreadable{}}
	for _, c := range found {
		out.Candidates = append(out.Candidates, c.ConnectorCandidate)
	}
	for _, u := range unreadable {
		out.Unreadable = append(out.Unreadable, api.ConnectorUnreadable{Tool: u.Tool, Path: u.Path, Error: u.Err.Error()})
	}
	return writeJSON(w, http.StatusOK, out)
}

// connectorCandidates is what the user's AI tools have, as it would be
// imported into project ("" for AgentBox-wide).
func (s *Server) connectorCandidates(ctx context.Context, project string) ([]connectors.Candidate, []connectors.Unreadable, error) {
	var have []state.Connector
	var err error
	if project == "" {
		have, err = s.store.Connectors(ctx, "", "")
	} else {
		if _, err = s.store.Project(ctx, project); err != nil {
			return nil, nil, err
		}
		have, err = s.store.ProjectConnectors(ctx, project)
	}
	if err != nil {
		return nil, nil, err
	}
	seen := make([]api.Connector, 0, len(have))
	for _, c := range have {
		seen = append(seen, api.Connector{Name: c.Name, URL: c.URL})
	}
	found, unreadable := connectors.FindServers(s.skillHomesFor())
	return connectors.Candidates(found, seen, s.connectors.OAuth.CheckURL), unreadable, nil
}

func (s *Server) importConnectors(w http.ResponseWriter, r *http.Request) error {
	var req api.ImportConnectorsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.Servers) == 0 {
		return errors.New("pick a server to import")
	}
	ctx := r.Context()
	found, _, err := s.connectorCandidates(ctx, req.Project)
	if err != nil {
		return err
	}
	byURL := map[string]connectors.Candidate{}
	for _, c := range found {
		if c.URL != "" {
			byURL[c.URL] = c
		}
	}
	out := api.ImportConnectorsResult{Added: []api.Connector{}, Failed: []api.ImportConnectorFailure{}}
	fail := func(srv api.ImportConnector, name string, err error) {
		out.Failed = append(out.Failed, api.ImportConnectorFailure{URL: srv.URL, Name: name, Error: err.Error()})
	}

	// A server with no header to send is asked whether it wants a sign-in,
	// all of them at once: one that doesn't answer shouldn't hold up the rest.
	auth := make([]string, len(req.Servers))
	var probes sync.WaitGroup
	for i, srv := range req.Servers {
		if c, ok := byURL[srv.URL]; ok && c.Header == "" && c.Problem == "" && c.Existing == "" {
			probes.Go(func() {
				ctx, cancel := context.WithTimeout(ctx, probeTimeout)
				defer cancel()
				auth[i] = s.connectors.OAuth.AuthOf(ctx, c.URL)
			})
		}
	}
	probes.Wait()

	secretsSet := false
	var added []string
	for i, srv := range req.Servers {
		c, ok := byURL[srv.URL]
		if !ok {
			fail(srv, srv.Name, errors.New("none of your AI tools has this server any more: scan again"))
			continue
		}
		name := cmp.Or(strings.TrimSpace(srv.Name), c.Name)
		set := api.SetConnectorRequest{URL: c.URL, Auth: auth[i]}
		switch {
		case c.Problem != "":
			fail(srv, name, fmt.Errorf("%s can't be a connector: %s", c.URL, problemText(c.Problem)))
			continue
		case c.Existing != "":
			fail(srv, name, fmt.Errorf("it is a connector already, %s", c.Existing))
			continue
		}
		if err := s.connectorNameFree(ctx, req.Project, name); err != nil {
			fail(srv, name, err)
			continue
		}
		if c.Header != "" {
			secret, stored, err := s.importSecret(ctx, req.Project, c, srv.Value)
			if err != nil {
				fail(srv, name, err)
				continue
			}
			secretsSet = secretsSet || (stored && req.Project != "")
			set = api.SetConnectorRequest{URL: c.URL, Auth: api.ConnectorSecret, Secret: secret, Header: c.Header, Scheme: c.Scheme}
		}
		conn, err := s.connectors.Set(ctx, req.Project, "", name, set)
		if err != nil {
			fail(srv, name, err)
			continue
		}
		info, err := s.connectorInfo(ctx, conn)
		if err != nil {
			return err
		}
		out.Added = append(out.Added, info)
		added = append(added, conn.Name)
	}
	if secretsSet {
		if err := s.deliver(ctx, req.Project, ""); err != nil {
			s.logf("connectors imported into %s: %v", scopeRef(req.Project, ""), err)
		}
	}
	if len(added) > 0 {
		s.logf("connectors imported into %s from the user's AI tools: %s", scopeRef(req.Project, ""), strings.Join(added, ", "))
	}
	return writeJSON(w, http.StatusOK, out)
}

// connectorNameFree refuses a name a connector of the scope has: an import
// adds, and never changes one.
func (s *Server) connectorNameFree(ctx context.Context, project, name string) error {
	var err error
	if project == "" {
		_, err = s.store.Connector(ctx, "", "", name)
	} else {
		_, err = s.store.ProjectConnector(ctx, project, name)
	}
	switch {
	case err == nil:
		return fmt.Errorf("a connector is called %s already: import it under another name", name)
	case errors.Is(err, state.ErrNotFound):
		return nil
	}
	return err
}

// importSecret stores the value a candidate's header sends, as a secret of
// the scope — the project's, or the one an AgentBox-wide connector keeps —
// and says which. A value from the tool's file goes under c.Secret, or a
// name of its own when the scope has another value there; one the tool reads
// from the user's shell is value, entered by the user, or failing that the
// scope's secret of that name. stored is whether a value was written.
func (s *Server) importSecret(ctx context.Context, project string, c connectors.Candidate, value string) (name string, stored bool, err error) {
	name = c.Secret
	if c.NeedsValue {
		if value == "" {
			if _, err := s.secrets().Resolve(ctx, project, "", name); err == nil {
				return name, false, nil
			}
			return "", false, fmt.Errorf("its %s header comes from $%s in your shell, which AgentBox can't read: enter its value", c.Header, c.Variable)
		}
	} else {
		value = c.Value
		for i := 2; ; i++ {
			have, err := s.secrets().Resolve(ctx, project, "", name)
			if err != nil || have == value {
				break
			}
			name = fmt.Sprintf("%s_%d", c.Secret, i)
		}
	}
	if project == "" {
		err = s.connectors.SetSecret(ctx, name, value)
	} else {
		_, err = s.secrets().Set(ctx, project, "", name, value)
	}
	if err != nil {
		return "", false, err
	}
	s.logf("secret %s set for %s, from an AI tool's MCP server", name, scopeRef(project, ""))
	return name, true, nil
}

// problemText is a ConnectorCandidate's Problem in words, for the API's and
// the command line's.
func problemText(problem string) string {
	switch problem {
	case api.ConnectorProblemStdio:
		return "it is a command your AI tool runs on your machine, and a connector is a remote server"
	case api.ConnectorProblemSSE:
		return "it speaks the older SSE transport, and connectors speak streamable HTTP"
	case api.ConnectorProblemVariable:
		return "its address has an environment variable in it"
	case api.ConnectorProblemURL:
		return "a connector's address is https, and not on this machine"
	}
	return problem
}
