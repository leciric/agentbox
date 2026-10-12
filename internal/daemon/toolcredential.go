package daemon

import (
	"context"
	"net/http"
	"sync"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/toolcreds"
)

// agentCredential serves what an agent's AI tool really runs on, read from its
// running process (agent.Manager.ToolCredential), for its info card.
func (s *Server) agentCredential(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.toolCredential(r.Context(), a))
}

// projectCredentials is the same for every agent of a project with an AI
// tool, for its AI accounts: read in parallel, since each one asks its machine.
func (s *Server) projectCredentials(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	agents, err := s.store.Agents(r.Context(), project)
	if err != nil {
		return err
	}
	var mine []state.Agent
	for _, a := range agents {
		if !a.IsLead() && len(toolcreds.Variables[a.AI]) > 0 {
			mine = append(mine, a)
		}
	}
	out := make([]api.ToolCredential, len(mine))
	var wg sync.WaitGroup
	for i, a := range mine {
		wg.Go(func() { out[i] = s.toolCredential(r.Context(), a) })
	}
	wg.Wait()
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) toolCredential(ctx context.Context, a state.Agent) api.ToolCredential {
	cred, err := s.manager(nil).ToolCredential(ctx, a)
	out := api.ToolCredential{
		Agent: a.Ref(), Tool: a.AI, Running: cred.Running,
		Variable: cred.Variable, Account: cred.Account, Others: cred.Others,
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}
