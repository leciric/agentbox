package daemon

import (
	"context"
	"encoding/json"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/opencode"
	"agentbox/internal/state"
)

// openCodeModelsEvery is how often the daemon is willing to ask `opencode
// models` again. The list only changes when a provider is added or a model is
// released, and asking starts OpenCode's own runtime, so once an hour is
// plenty — a chat that runs OpenCode refreshes it for free anyway, from the
// menu its ACP session advertises (rememberChoices in internal/chat).
const openCodeModelsEvery = time.Hour

// refreshOpenCodeModels asks OpenCode what the stored login can run and
// remembers the answer, in the background and at most once an hour. It is how
// the menu exists at all before any OpenCode chat has started — which is when
// a project's lead needs it, since the lead chooses a model as it creates the
// agent that would otherwise have produced the list.
//
// Nothing here fails a request: an OpenCode that isn't on this machine's PATH,
// or a call that times out, leaves the menu as it was, and the app and the
// lead go on saying they have no list yet.
func (s *Server) refreshOpenCodeModels() {
	s.mu.Lock()
	if s.openCodeModels.running || time.Since(s.openCodeModels.last) < openCodeModelsEvery {
		s.mu.Unlock()
		return
	}
	s.openCodeModels.running, s.openCodeModels.last = true, time.Now()
	s.mu.Unlock()

	dataHome := s.manager(nil).Creds.OpenCodeDataHome()
	go func() {
		defer func() {
			s.mu.Lock()
			s.openCodeModels.running = false
			s.mu.Unlock()
		}()
		ids, err := opencode.Models(context.Background(), dataHome)
		if err != nil {
			s.logf("asking OpenCode which models it can run: %v", err)
			return
		}
		if len(ids) == 0 {
			return
		}
		raw, err := json.Marshal(openCodeChoices(ids))
		if err != nil {
			return
		}
		if err := s.store.SetSetting(context.Background(), state.SettingOpenCodeModelChoices, string(raw)); err != nil {
			s.logf("remembering OpenCode's model menu: %v", err)
		}
	}()
}

// openCodeChoices turns "provider/model" ids into menu entries. The id is the
// value, because that is what OpenCode is given back.
func openCodeChoices(ids []string) []api.ChatOptionChoice {
	choices := make([]api.ChatOptionChoice, 0, len(ids))
	for _, id := range ids {
		choices = append(choices, api.ChatOptionChoice{Value: id, Name: opencode.Name(id)})
	}
	return choices
}
