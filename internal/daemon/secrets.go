package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

// The secrets routes. Reading gives names, scopes and times, never a value:
// a value goes in once and from then on only into the agents that get it
// ([D52](decisions.md#d52)). Writing and removing also rewrite the file inside
// every running agent that holds the secret, so an agent that is up now has
// what the list says it has.

// listProjectSecrets is a project's own secrets: the ones every agent of it
// gets, including agents created later.
func (s *Server) listProjectSecrets(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	stored, err := s.secrets().List(r.Context(), p.Name, "")
	if err != nil {
		return err
	}
	out, err := s.secretsInfo(r.Context(), p.Name, stored)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// setProjectSecret stores a secret for the whole project and writes it into
// every running agent of it.
func (s *Server) setProjectSecret(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	return s.setSecret(w, r, p.Name, "")
}

func (s *Server) removeProjectSecret(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	return s.removeSecret(w, r, p.Name, "")
}

// listAgentSecrets is everything one agent holds: its project's secrets and its
// own, each saying which scope it comes from. The app's agent page shows the
// project's read-only above the agent's own, from this one answer.
func (s *Server) listAgentSecrets(w http.ResponseWriter, r *http.Request) error {
	a, err := s.secretsAgent(r)
	if err != nil {
		return err
	}
	scoped, err := s.secrets().List(r.Context(), a.Project, "")
	if err != nil {
		return err
	}
	own, err := s.secrets().List(r.Context(), a.Project, a.Name)
	if err != nil {
		return err
	}
	// An agent's own secret of a name its project also has is the one it gets
	// (secrets.ForAgent), so the project's copy is not listed as reaching it.
	var out []api.Secret
	for _, sec := range scoped {
		if hasSecret(own, sec.Name) {
			continue
		}
		info, err := s.secretInfo(r.Context(), a.Project, sec)
		if err != nil {
			return err
		}
		out = append(out, info)
	}
	for _, sec := range own {
		info, err := s.secretInfo(r.Context(), a.Project, sec)
		if err != nil {
			return err
		}
		out = append(out, info)
	}
	if out == nil {
		out = []api.Secret{}
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) setAgentSecret(w http.ResponseWriter, r *http.Request) error {
	a, err := s.secretsAgent(r)
	if err != nil {
		return err
	}
	return s.setSecret(w, r, a.Project, a.Name)
}

func (s *Server) removeAgentSecret(w http.ResponseWriter, r *http.Request) error {
	a, err := s.secretsAgent(r)
	if err != nil {
		return err
	}
	return s.removeSecret(w, r, a.Project, a.Name)
}

// secretsAgent finds the agent a secrets route is about. A project's lead is
// refused: its AI tool runs on the host, where a secret would land in the
// user's own environment rather than in a machine of its own.
func (s *Server) secretsAgent(r *http.Request) (state.Agent, error) {
	a, err := s.store.Agent(r.Context(), r.PathValue("project"), r.PathValue("agent"))
	if err != nil {
		return state.Agent{}, err
	}
	if a.IsLead() {
		return state.Agent{}, fmt.Errorf("%s is the project's chat, which runs on this machine rather than in an agent: give the secret to the project instead", a.Ref())
	}
	return a, nil
}

// setSecret stores one secret and delivers it. Storing and delivering are
// reported apart: a value that is stored but couldn't reach a running agent is
// not a failed request, it is a stored secret and a named agent that will pick
// it up when it starts.
func (s *Server) setSecret(w http.ResponseWriter, r *http.Request, project, agent string) error {
	name := strings.TrimSpace(r.PathValue("name"))
	var req api.SetSecretRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	stored, err := s.secrets().Set(r.Context(), project, agent, name, req.Value)
	if err != nil {
		return err
	}
	// Nothing is logged about the value, and the event carries only the name.
	s.logf("secret %s set for %s", stored.Name, secretScope(project, agent))
	if err := s.deliver(r.Context(), project, agent); err != nil {
		return err
	}
	info, err := s.secretInfo(r.Context(), project, stored)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) removeSecret(w http.ResponseWriter, r *http.Request, project, agent string) error {
	name := strings.TrimSpace(r.PathValue("name"))
	if err := s.secrets().Remove(r.Context(), project, agent, name); err != nil {
		return err
	}
	s.logf("secret %s removed from %s", name, secretScope(project, agent))
	// The file is written again without it, so an agent that is running loses
	// the variable when its next shell or its AI tool starts.
	if err := s.deliver(r.Context(), project, agent); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// deliver writes the secrets file again inside the agents a change reaches:
// one agent, or every running agent of the project.
func (s *Server) deliver(ctx context.Context, project, agent string) error {
	m := s.manager(nil)
	if agent == "" {
		return m.RewriteSecrets(ctx, project)
	}
	a, err := s.store.Agent(ctx, project, agent)
	if err != nil {
		return err
	}
	if a.Status != state.AgentReady {
		return nil // it is still being built; create writes the file itself
	}
	if inst, err := s.cfg.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
		return nil // it gets the file when it starts
	}
	if err := m.WriteSecrets(ctx, a); err != nil {
		return fmt.Errorf("the secret is stored, but couldn't be written into %s: it gets it when it starts: %w", a.Ref(), err)
	}
	return nil
}

// leadSecrets tells a project's chat which secrets its agents have, by name.
// Read-only, and values have no route at all: the chat can say "the key is in
// $STRIPE_SECRET_KEY" and cannot read one, which is the whole point of it
// being a separate, narrower route than the project's own.
func (s *Server) leadSecrets(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	stored, err := s.secrets().Project(r.Context(), project)
	if err != nil {
		return err
	}
	out, err := s.secretsInfo(r.Context(), project, stored)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) secretsInfo(ctx context.Context, project string, stored []secrets.Secret) ([]api.Secret, error) {
	out := make([]api.Secret, 0, len(stored))
	for _, sec := range stored {
		info, err := s.secretInfo(ctx, project, sec)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// secretInfo describes a secret and where it is delivered. A project secret
// names the agents of the project that hold it; agents made later get it too,
// which the field's documentation says rather than this list pretending to
// predict them.
func (s *Server) secretInfo(ctx context.Context, project string, sec secrets.Secret) (api.Secret, error) {
	info := api.Secret{
		Name:      sec.Name,
		Scope:     sec.Scope(),
		Project:   sec.Project,
		Agent:     sec.Agent,
		UpdatedAt: sec.UpdatedAt,
		Agents:    []string{},
	}
	if sec.Agent != "" {
		info.Agents = append(info.Agents, sec.Project+"/"+sec.Agent)
		return info, nil
	}
	agents, err := s.store.Agents(ctx, project)
	if err != nil {
		return api.Secret{}, err
	}
	for _, a := range agents {
		if a.IsLead() {
			continue // no machine of its own, so no secrets file
		}
		// An agent with its own secret of this name has that one instead.
		if _, err := s.store.Secret(ctx, project, a.Name, sec.Name); err == nil {
			continue
		} else if !errors.Is(err, state.ErrNotFound) {
			return api.Secret{}, err
		}
		info.Agents = append(info.Agents, a.Ref())
	}
	return info, nil
}

func hasSecret(list []secrets.Secret, name string) bool {
	for _, sec := range list {
		if sec.Name == name {
			return true
		}
	}
	return false
}

// secretScope names a scope in a log line: a project, or one agent.
func secretScope(project, agent string) string {
	if agent == "" {
		return "project " + project
	}
	return project + "/" + agent
}
