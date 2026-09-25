package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// settings reports the installation's own settings, with the model menu the
// Claude Code adapter last advertised. That menu is remembered rather than
// listed by AgentBox: which models an account may use arrives over ACP and
// differs between accounts, so the only honest list is one an adapter really
// sent (see rememberChoices in internal/chat) — with AgentBox's own small
// pinned list folded on top (state.MergePinnedClaudeModels, D69).
func (s *Server) settings(w http.ResponseWriter, r *http.Request) error {
	out, err := s.currentSettings(r)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var req api.UpdateSettingsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	// The models are stored as typed, with no check against the menu above: a
	// model preference is passed to the adapter even when it isn't literally
	// one of this account's choices, because the adapter resolves aliases
	// itself ("opus[1m]" becomes plain "opus" where there's no 1M entry). A
	// pre-check here would reject exactly the values that do work. The window
	// is checked, though, against the model it goes with — whichever of them
	// this request moves — so that a Haiku default can't hold a 1M window.
	if err := s.updateRoleDefaults(r.Context(), []roleDefaults{
		{"new agents", state.SettingDefaultClaudeModel, state.DefaultClaudeModel, req.DefaultClaudeModel,
			state.SettingDefaultAgentContextWindow, req.DefaultAgentContextWindow},
		{"the lead", state.SettingDefaultLeadModel, "default", req.DefaultLeadModel,
			state.SettingDefaultLeadContextWindow, req.DefaultLeadContextWindow},
	}); err != nil {
		return err
	}
	if req.DefaultClaudeEffort != nil {
		// Checked, unlike the model: the adapter resolves no aliases for this
		// option and refuses anything outside the list it advertised, so a
		// value that has never been on this account's menu can only end in a
		// setting silently doing nothing. Nothing is rejected before a menu
		// has ever arrived, since there is then nothing true to check against.
		want := strings.TrimSpace(*req.DefaultClaudeEffort)
		menu, err := s.store.Setting(r.Context(), state.SettingClaudeEffortChoices)
		if err != nil {
			return err
		}
		offered := state.ChoiceValues(menu)
		if want != "" && len(offered) > 0 && !slices.Contains(offered, want) {
			return fmt.Errorf("this agent's Claude Code doesn't offer the effort %q: it offers %s", want, strings.Join(offered, ", "))
		}
		if err := s.store.SetSetting(r.Context(), state.SettingDefaultClaudeEffort, want); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		key   string
		value *string
		check func(string) error
	}{
		{state.SettingDefaultCPU, req.DefaultCPU, agent.ValidateCPU},
		{state.SettingDefaultCPUAllowance, req.DefaultCPUAllowance, agent.ValidateAllowance},
		{state.SettingDefaultMemory, req.DefaultMemory, agent.ValidateMemory},
	} {
		if field.value == nil {
			continue
		}
		// "" is stored, not treated as "nobody chose": it is how you say that
		// new agents get no limit at all, and the difference is what
		// SettingValue exists for. Agents that already exist keep what they
		// have, like the model and effort settings above.
		want := strings.TrimSpace(*field.value)
		if err := field.check(want); err != nil {
			return err
		}
		if err := s.store.SetSetting(r.Context(), field.key, want); err != nil {
			return err
		}
	}
	if req.ResumeAfterLimit != nil {
		// Stored as a flag rather than as "" / "1", because this one is on
		// until it is turned off: see state.FlagOn. Turning it off leaves any
		// chat already waiting alone in the store, and the wait itself checks
		// the setting again before it does anything (planResume in
		// internal/chat).
		if err := s.store.SetFlag(r.Context(), state.SettingResumeAfterLimit, *req.ResumeAfterLimit); err != nil {
			return err
		}
	}
	if req.ClaudeCompactWindow != nil {
		// Stored as a count, and checked against the bounds Claude Code holds
		// its own variable to: past them, it would clamp or ignore the value
		// and the setting would quietly mean something else (D83).
		if err := state.ValidClaudeCompactWindow(*req.ClaudeCompactWindow); err != nil {
			return err
		}
		if err := s.store.SetSetting(r.Context(), state.SettingClaudeCompactWindow, strconv.FormatInt(*req.ClaudeCompactWindow, 10)); err != nil {
			return err
		}
	}
	if req.MediaRetention != nil {
		want := strings.TrimSpace(*req.MediaRetention)
		if _, _, ok := state.MediaRetentionPeriod(want); !ok {
			return fmt.Errorf("invalid media retention %q: use %s, %s, %s, %s or %s", want,
				api.MediaRetentionImmediately, api.MediaRetentionDay, api.MediaRetentionWeek, api.MediaRetentionMonth, api.MediaRetentionForever)
		}
		if err := s.store.SetSetting(r.Context(), state.SettingMediaRetention, want); err != nil {
			return err
		}
		// A shorter period can make kept media due now, so the sweep doesn't
		// wait out its hour to act on it.
		go s.sweepExpiredMedia(s.background(), time.Now())
	}
	if req.UpdateCheck != nil {
		if err := s.setUpdateCheck(r.Context(), *req.UpdateCheck); err != nil {
			return err
		}
	}
	if req.UsageStats != nil {
		if err := s.setUsageStats(r.Context(), *req.UsageStats); err != nil {
			return err
		}
	}
	out, err := s.currentSettings(r)
	if err != nil {
		return err
	}
	s.countFeature(api.FeatureSettingsChange)
	return writeJSON(w, http.StatusOK, out)
}

// roleDefaults are one Settings section's default model and context window,
// and what a request asks of them. builtin is the model an empty setting
// means: AgentBox's own default for agents, Claude Code's for the lead.
type roleDefaults struct {
	role              string
	modelKey, builtin string
	model             *string
	windowKey         string
	window            *string
}

// updateRoleDefaults checks every role's model and window together, and only
// then stores any of them, so a refused window leaves the model it came with
// unchanged too.
func (s *Server) updateRoleDefaults(ctx context.Context, roles []roleDefaults) error {
	windows, err := s.store.ClaudeWindows(ctx)
	if err != nil {
		return err
	}
	installation, err := s.store.ClaudeCompactWindow(ctx)
	if err != nil {
		return err
	}
	var writes [][2]string
	for _, role := range roles {
		if role.model == nil && role.window == nil {
			continue
		}
		model, err := s.store.Setting(ctx, role.modelKey)
		if err != nil {
			return err
		}
		if role.model != nil {
			model = strings.TrimSpace(*role.model)
			writes = append(writes, [2]string{role.modelKey, model})
		}
		window, err := s.store.Setting(ctx, role.windowKey)
		if err != nil {
			return err
		}
		if role.window != nil {
			window = *role.window
		}
		if model == "" {
			model = role.builtin
		}
		stored, err := windows.DefaultContextWindow(windows.NormalizeClaudeModel(model), window, installation)
		if err != nil {
			if role.window == nil {
				return fmt.Errorf("%s start with a 1M context window, which %s doesn't have: choose 200k for them too", role.role, model)
			}
			return fmt.Errorf("the context window for %s: %w", role.role, err)
		}
		if role.window != nil {
			writes = append(writes, [2]string{role.windowKey, stored})
		}
	}
	for _, w := range writes {
		if err := s.store.SetSetting(ctx, w[0], w[1]); err != nil {
			return err
		}
	}
	return nil
}

// seedResourceDefaults writes the limits new agents start capped at, once, on
// an installation that has never had them. They can't be resolved lazily the
// way the Claude Code settings are: "" is a real choice there (no limit at
// all), so an unset key and a cleared one have to be different things, and the
// app's inputs have to show a number rather than the word "default".
//
// The memory ceiling is also given, once, to an installation seeded before
// there was one, whose default_memory the daemon itself wrote as "". That ""
// was never shown to anyone as a choice — the field was empty from the start,
// with nothing to clear — so it is taken as the old seed rather than a
// decision. Agents that already exist keep what they have: this only changes
// what the next one is made with.
func (s *Server) seedResourceDefaults(ctx context.Context) error {
	defaults := agent.DefaultLimits(agent.HostCores(), agent.HostMemory())
	seeded := false
	for _, field := range []struct{ key, value string }{
		{state.SettingDefaultCPU, defaults.CPU},
		{state.SettingDefaultCPUAllowance, defaults.Allowance},
		{state.SettingDefaultMemory, defaults.Memory},
	} {
		value, set, err := s.store.SettingValue(ctx, field.key)
		if err != nil {
			return err
		}
		if field.key == state.SettingDefaultMemory {
			_, memorySeeded, err := s.store.SettingValue(ctx, state.SettingDefaultMemorySeeded)
			if err != nil {
				return err
			}
			if !memorySeeded {
				if err := s.store.SetSetting(ctx, state.SettingDefaultMemorySeeded, "1"); err != nil {
					return err
				}
				// Set before this build, to "" by the old seed: the new one
				// replaces it. Set to a size: that was chosen, and stays.
				set = set && value != ""
			}
		}
		if set {
			continue
		}
		if err := s.store.SetSetting(ctx, field.key, field.value); err != nil {
			return err
		}
		seeded = true
	}
	if seeded {
		s.logf("new agents are capped at %s, of this host's %d cores and %s; change it in Settings, or per agent with agentbox limits",
			defaults.Describe(), agent.HostCores(), agent.HumanBytes(agent.HostMemory()))
	}
	return nil
}

func (s *Server) currentSettings(r *http.Request) (api.Settings, error) {
	model, err := s.store.Setting(r.Context(), state.SettingDefaultClaudeModel)
	if err != nil {
		return api.Settings{}, err
	}
	effort, err := s.store.Setting(r.Context(), state.SettingDefaultClaudeEffort)
	if err != nil {
		return api.Settings{}, err
	}
	var agentWindow, leadModel, leadWindow string
	for key, into := range map[string]*string{
		state.SettingDefaultAgentContextWindow: &agentWindow,
		state.SettingDefaultLeadModel:          &leadModel,
		state.SettingDefaultLeadContextWindow:  &leadWindow,
	} {
		if *into, err = s.store.Setting(r.Context(), key); err != nil {
			return api.Settings{}, err
		}
	}
	models, err := s.rememberedMenu(r, state.SettingClaudeModelChoices)
	if err != nil {
		return api.Settings{}, err
	}
	// AgentBox's own small pinned list — Fable, chief among them — goes on
	// top of whatever the adapter really advertised, so a menu shows it
	// before the account has ever run a chat on it (D45's amendment).
	menuKnown := len(models) > 0
	models = state.MergePinnedClaudeModels(models)
	efforts, err := s.rememberedMenu(r, state.SettingClaudeEffortChoices)
	if err != nil {
		return api.Settings{}, err
	}
	openCodeModels, err := s.rememberedMenu(r, state.SettingOpenCodeModelChoices)
	if err != nil {
		return api.Settings{}, err
	}
	openCodeReady, err := s.manager(nil).OpenCodeReady(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	// Asked for in the background, and only when there is nothing true to show
	// yet: the answer lands in the setting for the next read rather than
	// holding this one up behind another process.
	if len(openCodeModels) == 0 && openCodeReady {
		s.refreshOpenCodeModels()
	}
	limits, err := s.manager(nil).Defaults(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	resumeAfterLimit, err := s.store.FlagOn(r.Context(), state.SettingResumeAfterLimit)
	if err != nil {
		return api.Settings{}, err
	}
	updateCheck, err := s.store.FlagOn(r.Context(), state.SettingUpdateCheck)
	if err != nil {
		return api.Settings{}, err
	}
	usageStats, err := s.store.FlagOn(r.Context(), state.SettingUsageStats)
	if err != nil {
		return api.Settings{}, err
	}
	compactWindow, err := s.store.ClaudeCompactWindow(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	windows, err := s.store.ClaudeWindows(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	contextWindows := map[string][]int64{"default": windows.ContextWindows("default", compactWindow)}
	for _, c := range models {
		contextWindows[c.Value] = windows.ContextWindows(windows.NormalizeClaudeModel(c.Value), compactWindow)
	}
	// And the models the two roles default to, which Settings looks up here
	// even before any chat has remembered the adapter's menu: without them a
	// fresh installation's opus fell back to the first window alone, and
	// Settings offered it no 1M.
	for _, m := range []string{state.DefaultClaudeModel, model, leadModel} {
		if _, ok := contextWindows[m]; m != "" && !ok {
			contextWindows[m] = windows.ContextWindows(windows.NormalizeClaudeModel(m), compactWindow)
		}
	}
	mediaRetention, err := s.store.MediaRetention(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	return api.Settings{
		DefaultClaudeModel:        model,
		DefaultAgentContextWindow: agentWindow,
		DefaultLeadModel:          leadModel,
		DefaultLeadContextWindow:  leadWindow,
		ClaudeModelChoices:        models,
		ClaudeContextWindows:      contextWindows,
		ClaudeMenuKnown:           menuKnown,
		DefaultClaudeEffort:       effort,
		ClaudeEffortChoices:       efforts,

		OpenCodeModelChoices: openCodeModels,
		OpenCodeReady:        openCodeReady,

		DefaultCPU:          limits.CPU,
		DefaultCPUAllowance: limits.Allowance,
		DefaultMemory:       limits.Memory,
		HostCores:           agent.HostCores(),
		HostMemory:          agent.HostMemory(),
		SeedMemory:          agent.DefaultMemory(agent.HostMemory()),

		ResumeAfterLimit: resumeAfterLimit,
		UpdateCheck:      updateCheck,
		UsageStats:       usageStats,
		MediaRetention:   mediaRetention,

		ClaudeCompactWindow:        compactWindow,
		DefaultClaudeCompactWindow: state.DefaultClaudeCompactWindow,
	}, nil
}

// rememberedMenu reads one of the menus a tool's adapter really advertised. A menu stored by an older build that can't be read is no reason
// to fail the whole request: the app just shows no choices yet.
func (s *Server) rememberedMenu(r *http.Request, key string) ([]api.ChatOptionChoice, error) {
	value, err := s.store.Setting(r.Context(), key)
	if err != nil {
		return nil, err
	}
	choices := []api.ChatOptionChoice{}
	if value != "" {
		if err := json.Unmarshal([]byte(value), &choices); err != nil {
			choices = []api.ChatOptionChoice{}
		}
	}
	return choices, nil
}
