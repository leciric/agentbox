package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/connectors"
	"agentbox/internal/state"
)

// A connector request is an agent asking the user to connect a connector it
// needs — request_connector — the way a credential request asks for a secret:
// a question of its own kind, that waits, is listed and shows in the agent's
// thread, but goes straight to the user, whom only the app can answer. The
// user adds and connects it from the request's card, or anywhere else — the
// Connectors tab, `agentbox connector connect` — and the request is answered
// once the connector reaches the agent and can be used; or they decline it,
// on the credential route. Nothing secret passes through it either way: the
// user signs in on the server's own page, and the tokens stay with the daemon.

// requestConnector is the in-agent route: the agent asks, and waits.
func (s *Server) requestConnector(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		var req api.ConnectorRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		q, err := s.requestConnectorFor(r.Context(), a, req)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, toAPIQuestion(q))
	}
}

// requestConnectorFor records an agent's request, puts it in front of the
// user and waits for it to be connected. A connector the agent can already use
// is answered at once, with nothing put in front of anybody.
func (s *Server) requestConnectorFor(ctx context.Context, a state.Agent, req api.ConnectorRequest) (state.Question, error) {
	if a.IsLead() {
		return state.Question{}, errNotAnAgent
	}
	name := strings.TrimSpace(req.Name)
	if err := connectors.ValidateName(name); err != nil {
		return state.Question{}, err
	}
	q := state.Question{
		ID: newID(), Project: a.Project, Agent: a.Name, Kind: state.QuestionConnector, Connector: name,
		Text:   strings.TrimSpace(req.Reason),
		Status: state.QuestionEscalated, CreatedAt: time.Now(),
	}
	if q.Text == "" {
		return state.Question{}, errors.New("no reason: say what you need the connector for, so the user can decide")
	}
	c, found, err := s.requestedConnector(ctx, a, name)
	if err != nil {
		return state.Question{}, err
	}
	if found {
		q.ConnectorURL = c.URL
		if s.connectorUsable(ctx, a, c) {
			q.Status, q.Answer, q.AnsweredBy, q.AnsweredAt = state.QuestionAnswered, connectorOutcome(name, true), "agentbox", time.Now()
			return q, nil
		}
	} else {
		q.ConnectorURL = strings.TrimSpace(req.URL)
		if q.ConnectorURL == "" {
			return state.Question{}, fmt.Errorf("%s has no connector %q: give url, its MCP server's address (like https://mcp.notion.com/mcp), "+
				"so the user can add it", a.Project, name)
		}
		if err := s.connectors.OAuth.CheckURL(q.ConnectorURL); err != nil {
			return state.Question{}, err
		}
	}
	// An agent waits on one call at a time, so a request it made before this
	// is gone, or about to be (see requestCredentialFor).
	s.cancelCredentialRequests(ctx, a.Project, a.Name, "The agent asked again, and that request replaces this one.")
	ch := s.waiting.add(q.ID)
	defer s.waiting.remove(q.ID)
	if err := s.store.AddQuestion(ctx, q); err != nil {
		return state.Question{}, err
	}
	s.captureEvent(ctx, a.Project, a.Name, "connector_requested", map[string]any{
		"connector": q.Connector, "url": q.ConnectorURL, "reason": q.Text,
	}, "")
	s.events.publish(api.EventQuestion, toAPIQuestion(q))
	s.record(ctx, questionEvent(q, a.Title, api.AgentAsked, q.CreatedAt))
	s.logf("%s asks the user to connect %s (%s): %s", a.Ref(), q.Connector, q.ConnectorURL, q.Text)
	s.tellLead(ctx, a.Project, connectorNotice(q), false)

	select {
	case answered := <-ch:
		if answered.Status == state.QuestionCancelled {
			return answered, errors.New(answered.Answer)
		}
		return answered, nil
	case <-ctx.Done():
		s.cancelCredentialRequest(context.WithoutCancel(ctx), q, "The agent stopped waiting for it.")
		return state.Question{}, ctx.Err()
	case <-time.After(askTimeout):
		s.cancelCredentialRequest(context.WithoutCancel(ctx), q, fmt.Sprintf("Nobody answered within %s; the agent carried on without it.", askTimeout))
		return state.Question{}, fmt.Errorf("nobody answered within %s: carry on without it, and say in your final message what it was needed for", askTimeout)
	}
}

// requestedConnector is the connector of that name an agent would be given:
// its own, or its project's, whether or not its limit lets that one through.
func (s *Server) requestedConnector(ctx context.Context, a state.Agent, name string) (state.Connector, bool, error) {
	for _, agent := range []string{a.Name, ""} {
		c, err := s.store.Connector(ctx, a.Project, agent, name)
		if err == nil {
			return c, true, nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return state.Connector{}, false, err
		}
	}
	return state.Connector{}, false, nil
}

// connectorUsable reports whether the agent can use c now: it is on, it
// reaches the agent, and it is connected.
func (s *Server) connectorUsable(ctx context.Context, a state.Agent, c state.Connector) bool {
	if !c.Enabled || (c.Agent == "" && !a.GetsConnector(c.Name)) {
		return false
	}
	status, _ := s.connectors.Status(ctx, c, a.Name)
	return status == api.ConnectorConnected
}

// connectorOutcome is what the agent is told once it has the connector.
func connectorOutcome(name string, already bool) string {
	what := "The user connected " + name + "."
	if already {
		what = name + " is already connected, and yours."
	}
	return what + " Use it right away from your shell, through AgentBox, which holds its sign-in: " +
		"`agentbox connector tools " + name + "` lists its tools and the JSON each takes, and " +
		"`agentbox connector call " + name + " <tool> '<json>'` calls one. Your AI tool gets its tools natively " +
		"(mcp__" + name + "__* in Claude Code) with its next session, since a session reads its MCP servers when it starts."
}

func connectorNotice(q state.Question) string {
	return fmt.Sprintf("%s asked the user to connect %s (%s): %s\n\nOnly the user can answer this, from the card in the app, "+
		"and it waits until they connect it or decline. You can't answer it.", q.Agent, q.Connector, q.ConnectorURL, q.Text)
}

// answerConnectorFor is the user's answer to a connector request, on the
// credential route (answerCredentialFor): a refusal, or the connector's name
// once they have added and connected it. The connector is turned on if it was
// off and given to the agent past its limit; one that still can't be used is
// refused, and the request keeps waiting.
func (s *Server) answerConnectorFor(ctx context.Context, q state.Question, req api.AnswerCredentialRequest) (state.Question, error) {
	name := strings.TrimSpace(req.Connector)
	// Answered on its own already, the moment the connector could be used
	// (resolveConnectorRequests): the app's own answer, right behind it,
	// finds it done.
	if q.Status == state.QuestionAnswered && name == q.Connector && !req.Refuse {
		return q, nil
	}
	if !q.Waiting() {
		return q, fmt.Errorf("question %s was already %s", q.ID, q.Status)
	}
	switch {
	case req.Refuse && name == "" && req.GitHubAccount == "" && req.Value == "":
		outcome := "The user declined"
		if why := strings.TrimSpace(req.Reason); why != "" {
			outcome += ": " + why
		}
		outcome += ". Carry on without it, and don't ask for it again; say in your final message what it was needed for."
		return s.answerQuestion(ctx, q.ID, outcome, "user")
	case req.Refuse || req.GitHubAccount != "" || req.Value != "" || name == "":
		return q, fmt.Errorf("this request is for the connector %s: answer with its name, once it is connected, or a refusal", q.Connector)
	case name != q.Connector:
		return q, fmt.Errorf("this request is for the connector %s, not %s", q.Connector, name)
	}
	a, err := s.store.Agent(ctx, q.Project, q.Agent)
	if err != nil {
		return q, err
	}
	c, found, err := s.requestedConnector(ctx, a, name)
	if err != nil {
		return q, err
	}
	if !found {
		return q, fmt.Errorf("%s has no connector %s yet: add it and connect it first", q.Project, name)
	}
	if !c.Enabled {
		set, on := setRequestOf(c), true
		set.Enabled = &on
		if c, err = s.connectors.Set(ctx, c.Project, c.Agent, c.Name, set); err != nil {
			return q, err
		}
	}
	if c.Agent == "" && !a.GetsConnector(name) {
		if err := s.manager(nil).GrantConnector(ctx, a, name); err != nil {
			return q, err
		}
		if a, err = s.store.Agent(ctx, q.Project, q.Agent); err != nil {
			return q, err
		}
		s.logf("%s is given the connector %s, as it asked", a.Ref(), name)
	}
	if !s.connectorUsable(ctx, a, c) {
		status, why := s.connectors.Status(ctx, c, a.Name)
		if why == "" {
			why = status
		}
		return q, fmt.Errorf("%s can't be used yet (%s): connect it first", name, why)
	}
	answered, err := s.answerQuestion(ctx, q.ID, connectorOutcome(name, false), "user")
	if err != nil {
		// Answered on its own in between, by the change above.
		if now, err2 := s.store.Question(ctx, q.ID); err2 == nil && now.Status == state.QuestionAnswered {
			return now, nil
		}
		return q, err
	}
	return answered, nil
}

// setRequestOf is a connector as the request that would set it as it is.
func setRequestOf(c state.Connector) api.SetConnectorRequest {
	on := c.Enabled
	return api.SetConnectorRequest{URL: c.URL, Auth: c.Auth, Secret: c.Secret, Header: c.Header, Scheme: c.Scheme, Enabled: &on}
}

// resolveConnectorRequests answers every request of a project's that its
// agent can now use the connector for: connected from its card, or from
// anywhere else.
func (s *Server) resolveConnectorRequests(ctx context.Context, project string) {
	waiting, err := s.store.WaitingConnectorRequests(ctx, project)
	if err != nil {
		s.logf("connector requests of %s: %v", project, err)
		return
	}
	for _, q := range waiting {
		a, err := s.store.Agent(ctx, q.Project, q.Agent)
		if err != nil {
			continue
		}
		c, found, err := s.requestedConnector(ctx, a, q.Connector)
		if err != nil || !found || !s.connectorUsable(ctx, a, c) {
			continue
		}
		if _, err := s.answerQuestion(ctx, q.ID, connectorOutcome(q.Connector, false), "user"); err != nil {
			s.logf("%s's request for %s: %v", q.Ref(), q.Connector, err)
			continue
		}
		s.logf("%s has %s now, as it asked", q.Ref(), q.Connector)
	}
}
