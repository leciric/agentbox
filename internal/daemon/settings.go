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

	"agentbox/internal/api"
	"agentbox/internal/memory"
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
	if req.EnforceAgentDefaults != nil {
		if err := s.store.SetFlag(r.Context(), state.SettingEnforceAgentDefaults, *req.EnforceAgentDefaults); err != nil {
			return err
		}
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
	if req.ContinueAfterRestart != nil {
		// A flag too, on until turned off. Read once, as the daemon starts.
		if err := s.store.SetFlag(r.Context(), state.SettingContinueAfterRestart, *req.ContinueAfterRestart); err != nil {
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
	if req.Language != nil {
		want := strings.TrimSpace(*req.Language)
		if want != "" {
			if err := state.ValidLanguage(want); err != nil {
				return err
			}
		}
		if err := s.store.SetSetting(r.Context(), state.SettingLanguage, want); err != nil {
			return err
		}
	}
	if req.DiskFloorMin != nil || req.DiskFloorPercent != nil {
		if err := s.setDiskFloor(r.Context(), req.DiskFloorMin, req.DiskFloorPercent); err != nil {
			return err
		}
	}
	if req.ImageCache != nil || req.ImageCacheMaxBytes != nil || req.ClearImageCache {
		if err := s.setImageCache(r.Context(), req.ImageCache, req.ImageCacheMaxBytes, req.ClearImageCache); err != nil {
			return err
		}
	}
	if req.PackageCache != nil || req.PackageCacheMaxBytes != nil || req.ClearPackageCache {
		if err := s.setPackageCache(r.Context(), req.PackageCache, req.PackageCacheMaxBytes, req.ClearPackageCache); err != nil {
			return err
		}
	}
	if req.IdleTimeSeconds != nil {
		if *req.IdleTimeSeconds < 60 {
			return fmt.Errorf("idle time is at least 60 seconds; %d isn't", *req.IdleTimeSeconds)
		}
		if err := s.store.SetSetting(r.Context(), state.SettingIdleTime, strconv.Itoa(*req.IdleTimeSeconds)); err != nil {
			return err
		}
	}
	if req.AutoStopIdle != nil {
		if err := s.store.SetFlag(r.Context(), state.SettingAutoStopIdle, *req.AutoStopIdle); err != nil {
			return err
		}
	}
	if req.DockerPruneOnStop != nil {
		if err := s.store.SetFlag(r.Context(), state.SettingDockerPruneOnStop, *req.DockerPruneOnStop); err != nil {
			return err
		}
	}
	if req.AgentQueue != nil {
		if err := s.store.SetFlag(r.Context(), state.SettingAgentQueue, *req.AgentQueue); err != nil {
			return err
		}
		// Off starts whatever is queued; on may have work to look at.
		s.kickQueue()
	}
	if req.TaskTarget != nil {
		target := strings.TrimSpace(*req.TaskTarget)
		if target != memory.TaskRouteAgent && target != memory.TaskRouteLead {
			return fmt.Errorf("tasks go to %q or %q; %q isn't either", memory.TaskRouteAgent, memory.TaskRouteLead, target)
		}
		if err := s.store.SetSetting(r.Context(), state.SettingTaskTarget, target); err != nil {
			return err
		}
	}
	if req.LeadRecheckMinutes != nil {
		if n := *req.LeadRecheckMinutes; n < 5 || n > 1440 {
			return fmt.Errorf("the recheck is every 5 to 1440 minutes; %d isn't", n)
		}
		if err := s.store.SetSetting(r.Context(), state.SettingLeadRecheckMinutes, strconv.Itoa(*req.LeadRecheckMinutes)); err != nil {
			return err
		}
	}
	if req.LeadRecheck != nil {
		if err := s.store.SetFlag(r.Context(), state.SettingLeadRecheck, *req.LeadRecheck); err != nil {
			return err
		}
	}
	if req.AgentQueue != nil || req.LeadRecheck != nil {
		// Every chat's brief says whether it has a queue and rechecks.
		if projects, err := s.store.Projects(r.Context()); err == nil {
			for _, p := range projects {
				if err := s.manager(nil).ReconfigureLead(r.Context(), p.Name); err != nil {
					s.logf("reconfiguring the %s chat: %v", p.Name, err)
				}
			}
		}
	}
	if req.AutoStopIdle != nil || req.IdleTimeSeconds != nil {
		// Turning this on, or shortening the idle time, can make an agent idle
		// now rather than at the next tick, so check right away instead of
		// leaving it stuck until the next sweep.
		go s.stopIdleAgents(s.background(), time.Now())
	}
	if req.UpdateCheck != nil {
		if err := s.setUpdateCheck(r.Context(), *req.UpdateCheck); err != nil {
			return err
		}
	}
	if req.UpdateChannel != nil {
		if err := s.setUpdateChannel(r.Context(), *req.UpdateChannel); err != nil {
			return err
		}
	}
	if req.UsageStats != nil {
		if err := s.setUsageStats(r.Context(), *req.UsageStats); err != nil {
			return err
		}
	}
	if req.ErrorReports != nil {
		if err := s.store.SetFlag(r.Context(), state.SettingErrorReports, *req.ErrorReports); err != nil {
			return err
		}
	}
	if req.PRWatch != nil {
		if err := s.setPRWatch(r.Context(), *req.PRWatch); err != nil {
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

func (s *Server) currentSettings(r *http.Request) (api.Settings, error) {
	model, err := s.store.Setting(r.Context(), state.SettingDefaultClaudeModel)
	if err != nil {
		return api.Settings{}, err
	}
	effort, err := s.store.Setting(r.Context(), state.SettingDefaultClaudeEffort)
	if err != nil {
		return api.Settings{}, err
	}
	enforce, err := s.store.Flag(r.Context(), state.SettingEnforceAgentDefaults)
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
	resumeAfterLimit, err := s.store.FlagOn(r.Context(), state.SettingResumeAfterLimit)
	if err != nil {
		return api.Settings{}, err
	}
	continueAfterRestart, err := s.store.FlagOn(r.Context(), state.SettingContinueAfterRestart)
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
	errorReports, err := s.store.Setting(r.Context(), state.SettingErrorReports)
	if err != nil {
		return api.Settings{}, err
	}
	prWatch, err := s.store.FlagOn(r.Context(), state.SettingPRWatch)
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
	language, err := s.store.Language(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	diskFloor := s.diskFloor(r.Context())
	autoStopIdle, idleTime, err := s.store.AutoStopIdle(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	dockerPrune, err := s.store.FlagOn(r.Context(), state.SettingDockerPruneOnStop)
	if err != nil {
		return api.Settings{}, err
	}
	agentQueue, err := s.store.Flag(r.Context(), state.SettingAgentQueue)
	if err != nil {
		return api.Settings{}, err
	}
	leadRecheck, recheckEvery, err := s.store.LeadRecheck(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	taskTarget, err := s.taskTarget(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	imageCache, imageCacheChosen, err := s.store.ImageCache(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	imageCacheMax, imageCacheDefault := s.cacheMax(r.Context(), s.cfg.Paths.ImageCache(), imageCacheChosen, state.DefaultImageCacheMax)
	packageCache, packageCacheChosen, err := s.store.PackageCache(r.Context())
	if err != nil {
		return api.Settings{}, err
	}
	packageCacheMax, packageCacheDefault := s.cacheMax(r.Context(), s.cfg.Paths.PackageCache(), packageCacheChosen, state.DefaultPackageCacheMax)
	return api.Settings{
		DefaultClaudeModel:        model,
		DefaultAgentContextWindow: agentWindow,
		EnforceAgentDefaults:      enforce,
		DefaultLeadModel:          leadModel,
		DefaultLeadContextWindow:  leadWindow,
		ClaudeModelChoices:        models,
		ClaudeContextWindows:      contextWindows,
		ClaudeMenuKnown:           menuKnown,
		DefaultClaudeEffort:       effort,
		ClaudeEffortChoices:       efforts,

		OpenCodeModelChoices: openCodeModels,
		OpenCodeReady:        openCodeReady,

		ResumeAfterLimit:     resumeAfterLimit,
		ContinueAfterRestart: continueAfterRestart,
		UpdateCheck:          updateCheck,
		UsageStats:           usageStats,
		ErrorReports:         errorReports == "1",
		ErrorReportsAsked:    errorReports != "",
		PRWatch:              prWatch,
		MediaRetention:       mediaRetention,
		Language:             language,

		ClaudeCompactWindow:        compactWindow,
		DefaultClaudeCompactWindow: state.DefaultClaudeCompactWindow,

		DiskFloorMin:     diskFloor.Min,
		DiskFloorPercent: diskFloor.Percent,

		AutoStopIdle:    autoStopIdle,
		IdleTimeSeconds: int(idleTime / time.Second),

		DockerPruneOnStop: dockerPrune,

		AgentQueue:         agentQueue,
		TaskTarget:         taskTarget,
		LeadRecheck:        leadRecheck,
		LeadRecheckMinutes: int(recheckEvery / time.Minute),

		ImageCache:                imageCache,
		ImageCacheMaxBytes:        imageCacheMax,
		DefaultImageCacheMaxBytes: imageCacheDefault,
		ImageCacheBytes:           s.imageCache.Size(),

		PackageCache:                packageCache,
		PackageCacheMaxBytes:        packageCacheMax,
		DefaultPackageCacheMaxBytes: packageCacheDefault,
		PackageCacheBytes:           s.packageCache.Size(),
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
