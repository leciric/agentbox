package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/update"
)

// Anonymous usage stats: how many times each feature was used, per UTC day,
// counted in state.db and sent with the update check once the day is over.
// A count is only ever an api.Feature key and a number, so nothing about what
// a feature was used on can reach one. Nothing is counted while nothing may
// be sent: a development build, AGENTBOX_NO_UPDATE_CHECK, DO_NOT_TRACK, the
// update check turned off, or the stats themselves turned off.

// usageDay is the UTC day a use now falls on.
func usageDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// usageStatsOn is whether a feature may be counted and sent now.
func (s *Server) usageStatsOn(ctx context.Context) bool {
	if on, err := s.updateCheckOn(ctx); err != nil || !on {
		return false
	}
	on, err := s.store.FlagOn(ctx, state.SettingUsageStats)
	return err == nil && on
}

// countFeature counts one use of feature, when the stats are on. A count
// that fails is dropped: it is never worth failing what was counted.
func (s *Server) countFeature(feature string) {
	ctx := s.background()
	if !s.usageStatsOn(ctx) {
		return
	}
	if err := s.store.CountFeature(ctx, usageDay(time.Now()), feature); err != nil {
		s.logf("counting %s: %v", feature, err)
	}
}

// agentFeature is feature with the agent's AI tool on the end, for the
// features counted per tool. A tool AgentBox doesn't know yet is counted as
// Claude Code, its default, rather than inventing a key.
func agentFeature(ai string, claude, codex, openCode string) string {
	switch ai {
	case "codex":
		return codex
	case "opencode":
		return openCode
	}
	return claude
}

// sendUsage sends the counts of the days that are over, and forgets them
// once the server has them. Days past update.MaxUsageDays are forgotten
// unsent: the server wouldn't take them. Like the check, every failure is
// silent, and what wasn't sent is tried again with the next check.
func (s *Server) sendUsage(ctx context.Context, install string) {
	if !s.usageStatsOn(ctx) {
		return
	}
	now := time.Now()
	if err := s.store.ForgetFeatureUsage(ctx, usageDay(now.AddDate(0, 0, -update.MaxUsageDays))); err != nil {
		return
	}
	days, counts, err := s.store.FeatureUsage(ctx, usageDay(now))
	if err != nil || len(days) == 0 {
		return
	}
	if len(days) > update.MaxUsageDays {
		days = days[:update.MaxUsageDays]
	}
	report := make([]update.UsageDay, len(days))
	for i, day := range days {
		report[i] = update.UsageDay{Day: day, Features: counts[day]}
	}
	if err := update.SendUsage(ctx, s.cfg.UpdateURL, update.NewRequest(install, Version), report); err != nil {
		return
	}
	s.store.ForgetFeatureUsage(ctx, days[len(days)-1])
}

// setUsageStats is the setting changing. Off forgets the counts not sent yet,
// so turning it back on later doesn't send what was used while it was off.
func (s *Server) setUsageStats(ctx context.Context, on bool) error {
	if err := s.store.SetFlag(ctx, state.SettingUsageStats, on); err != nil {
		return err
	}
	if !on {
		return s.store.ForgetFeatureUsage(ctx, "")
	}
	return nil
}

// countAppFeature is POST /v1/usage-stats/{feature}: the app counting a use
// only it can see. It takes a key from api.AppFeatures and nothing else.
func (s *Server) countAppFeature(w http.ResponseWriter, r *http.Request) error {
	feature := r.PathValue("feature")
	if !slices.Contains(api.AppFeatures, feature) {
		return fmt.Errorf("unknown feature %q", feature)
	}
	s.countFeature(feature)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// countChatOption counts a chat setting changing, by the kind of setting the
// tool says it is: its id is the tool's own, and differs between tools.
func (s *Server) countChatOption(session api.ChatSession, id string) {
	i := slices.IndexFunc(session.Options, func(o api.ChatOption) bool { return o.ID == id })
	if i < 0 {
		return
	}
	switch session.Options[i].Category {
	case "model":
		s.countFeature(api.FeatureChatModel)
	case "thought_level":
		s.countFeature(api.FeatureChatEffort)
	case "context_window":
		s.countFeature(api.FeatureChatWindow)
	case "mode":
		s.countFeature(api.FeatureChatMode)
	}
}
