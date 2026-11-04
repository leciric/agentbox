package daemon

import (
	"context"
	"encoding/json"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/github"
	"agentbox/internal/state"
)

// copilotModelsEvery is how often the daemon asks GitHub Copilot CLI for its
// menu while no chat has named one.
const copilotModelsEvery = time.Hour

// refreshCopilotModels asks GitHub Copilot CLI on this machine which models
// the default login can run (agent.CopilotModels) and remembers the answer,
// as refreshGeminiModels does for Gemini CLI: in the background, at most once
// an hour unless force says the login just changed. A Copilot chat's session
// replaces it with its own menu (chat.rememberChoices).
func (s *Server) refreshCopilotModels(force bool) {
	m := s.manager(nil)
	if !m.CopilotReady() {
		return
	}
	s.mu.Lock()
	if s.copilotModels.running || !force && time.Since(s.copilotModels.last) < copilotModelsEvery {
		s.mu.Unlock()
		return
	}
	s.copilotModels.running, s.copilotModels.last = true, time.Now()
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.copilotModels.running = false
			s.mu.Unlock()
		}()
		ctx := s.background()
		ask := func(ctx context.Context) ([]acp.ConfigChoice, error) { return m.CopilotModels(ctx, "") }
		if s.copilotModelsFor != nil {
			ask = s.copilotModelsFor
		}
		models, err := ask(ctx)
		if err != nil {
			s.logf("asking GitHub Copilot CLI which models it can run: %v", err)
			return
		}
		if len(models) == 0 {
			return
		}
		choices := make([]api.ChatOptionChoice, len(models))
		for i, c := range models {
			choices[i] = api.ChatOptionChoice{Value: c.Value, Name: c.Name, Description: c.Description}
		}
		raw, err := json.Marshal(choices)
		if err != nil {
			return
		}
		if err := s.store.SetSetting(ctx, state.SettingCopilotModelChoices, string(raw)); err != nil {
			s.logf("remembering GitHub Copilot CLI's model menu: %v", err)
		}
	}()
}

// copilotTokenUser is the GitHub user a Copilot account's token belongs to,
// shown beside the account, "" when GitHub doesn't say: a fine-grained token
// with only the Copilot Requests permission can still read who it is, and a
// token GitHub refuses is kept anyway, for Copilot to give its own reason.
func (s *Server) copilotTokenUser(ctx context.Context, token string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ask := func(ctx context.Context, token string) (string, error) {
		return s.gitHub(token).Login(github.WithCaller(ctx, "accounts"))
	}
	if s.copilotUserFor != nil {
		ask = s.copilotUserFor
	}
	login, err := ask(ctx, token)
	if err != nil {
		s.logf("asking GitHub whose GitHub Copilot CLI token it is: %v", err)
		return ""
	}
	return login
}
