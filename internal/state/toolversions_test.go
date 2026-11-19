package state_test

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/state"
)

func TestToolVersions(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if on, err := s.FlagOn(ctx, state.SettingClaudeCodeAutoUpdate); err != nil || !on {
		t.Fatalf("auto-update starts %v, %v; want on", on, err)
	}
	at := time.UnixMilli(time.Now().UnixMilli())
	if err := s.SetToolVersion(ctx, state.ToolVersion{Name: "claude", Latest: "2.2.0", CheckedAt: at, Error: "no network"}); err != nil {
		t.Fatal(err)
	}
	if o, err := s.ToolOverrides(ctx); err != nil || len(o) != 0 {
		t.Fatalf("a failed check overrides %v, %v; want nothing", o, err)
	}
	if err := s.SetToolVersion(ctx, state.ToolVersion{Name: "claude", Version: "2.2.0", Latest: "2.2.0", CheckedAt: at}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ToolVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := all["claude"]; got.Version != "2.2.0" || got.Error != "" || !got.CheckedAt.Equal(at) {
		t.Fatalf("claude = %+v", got)
	}
	if o, _ := s.ToolOverrides(ctx); o["claude"] != "2.2.0" {
		t.Fatalf("overrides = %v", o)
	}
	if err := s.ForgetToolVersions(ctx, "claude"); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.ToolVersions(ctx); len(all) != 0 {
		t.Fatalf("after forgetting: %v", all)
	}
}

// Keeping Claude Code up to date is on until somebody turns it off, and an
// explicit off stays off.
func TestClaudeCodeAutoUpdateDefaultsOn(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if on, err := s.FlagOn(ctx, state.SettingClaudeCodeAutoUpdate); err != nil || !on {
		t.Fatalf("never chosen: %v, %v; want on", on, err)
	}
	if err := s.SetFlag(ctx, state.SettingClaudeCodeAutoUpdate, false); err != nil {
		t.Fatal(err)
	}
	if on, err := s.FlagOn(ctx, state.SettingClaudeCodeAutoUpdate); err != nil || on {
		t.Fatalf("turned off: %v, %v; want off", on, err)
	}
	if err := s.SetFlag(ctx, state.SettingClaudeCodeAutoUpdate, true); err != nil {
		t.Fatal(err)
	}
	if on, err := s.FlagOn(ctx, state.SettingClaudeCodeAutoUpdate); err != nil || !on {
		t.Fatalf("turned back on: %v, %v; want on", on, err)
	}
}
