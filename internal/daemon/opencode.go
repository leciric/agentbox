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

// openCodeModelsTimeout bounds one refresh: installing OpenCode 2 with npm can
// hang, and without a deadline it would hold openCodeModels.running for the
// daemon's lifetime, so the menu would never be asked for again.
const openCodeModelsTimeout = 5 * time.Minute

// refreshOpenCodeModels asks OpenCode what the stored login can run and
// remembers the answer, in the background and at most once an hour. It is how
// the menu exists at all before any OpenCode chat has started — which is when
// a project's lead needs it, since the lead chooses a model as it creates the
// agent that would otherwise have produced the list.
//
// The OpenCode it asks is AgentBox's own: OpenCode 1 is downloaded the first
// time (opencode.Installer), and OpenCode 2 is installed with npm beside it
// when the installation chose it, with its credentials imported into a data
// home of their own. Nothing here fails a request: a download or a call that
// fails or times out leaves the menu as it was, and the app and the lead go on
// saying they have no list yet.
func (s *Server) refreshOpenCodeModels() {
	s.mu.Lock()
	if s.openCodeModels.running || time.Since(s.openCodeModels.last) < openCodeModelsEvery {
		s.mu.Unlock()
		return
	}
	s.openCodeModels.running, s.openCodeModels.last = true, time.Now()
	s.mu.Unlock()

	m := s.manager(nil)
	go func() {
		defer func() {
			s.mu.Lock()
			s.openCodeModels.running = false
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), openCodeModelsTimeout)
		defer cancel()
		raw, err := s.store.Setting(ctx, state.SettingOpenCodeVersion)
		if err != nil {
			s.logf("asking OpenCode which models it can run: %v", err)
			return
		}
		version := opencode.ReleaseOf(raw)
		var command, dataHome string
		standalone := version == opencode.V2
		if standalone {
			// OpenCode 2 is not the mise tool the base image pins: it is the
			// npm platform package, installed on the host the first time it is
			// asked, and its credentials go into a data home of their own.
			command, err = m.EnsureOpenCode2(ctx, func(string) {})
			if err != nil {
				s.logf("asking OpenCode which models it can run: %v", err)
				return
			}
			var export []byte
			dataHome, export, err = m.Creds.OpenCodeImportHome()
			if err != nil {
				s.logf("asking OpenCode which models it can run: %v", err)
				return
			}
			if err := opencode.Import(ctx, command, dataHome, export); err != nil {
				s.logf("asking OpenCode which models it can run: %v", err)
				return
			}
		} else {
			// OpenCode 1 is AgentBox's own download, and reads an auth.json
			// under the data home AgentBox writes for it.
			dataHome, err = m.Creds.OpenCodeModelsHome()
			if err != nil {
				s.logf("asking OpenCode which models it can run: %v", err)
				return
			}
			command, err = opencode.Installer{Dir: m.Paths.Tools()}.Ensure(ctx, func(string) {})
			if err != nil {
				s.logf("asking OpenCode which models it can run: %v", err)
				return
			}
		}
		ids, err := opencode.Models(ctx, command, dataHome, standalone)
		if err != nil {
			s.logf("asking OpenCode which models it can run: %v", err)
			return
		}
		if len(ids) == 0 {
			return
		}
		// The version may have moved while this was asking: the menu would
		// then name the release the installation no longer runs.
		if now, err := s.store.Setting(ctx, state.SettingOpenCodeVersion); err != nil || opencode.ReleaseOf(now) != version {
			return
		}
		menu, err := json.Marshal(openCodeChoices(ids))
		if err != nil {
			return
		}
		if err := s.store.SetSetting(ctx, state.SettingOpenCodeModelChoices, string(menu)); err != nil {
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
