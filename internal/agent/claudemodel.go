package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/state"
)

// Claude Code has two model resolvers, and they don't agree (D46). Mid-session,
// session/set_config_option validates against the curated menu the interactive
// /model picker shows — four entries on this account — and refuses anything
// else. At launch it resolves ANTHROPIC_MODEL, then the "model" key of its own
// settings.json, then a resumed session's live model, then its default, and the
// first two resolve against the SDK's whole catalogue rather than that menu.
//
// So writing the chosen model into settings.json before the adapter starts is
// the only way to begin a session on a model the account's menu doesn't list —
// Fable among them — and it is not a silent override: the adapter advertises
// what it resolved back in configOptions, where the composer's menu shows it.

// claudeSettingsFile is Claude Code's settings, relative to HOME. Both paths
// that run it give it a HOME of its own — an agent's user inside its own
// machine, and a project's lead's private directory on the host (D6) — so this
// is per agent already, and never the user's own ~/.claude. That isolation is
// also why AgentBox doesn't set CLAUDE_CONFIG_DIR: HOME is what the login, the
// brief and the lead's permission policy already travel in, and pointing the
// settings somewhere else would only split the tool's state in two.
const claudeSettingsFile = ".claude/settings.json"

// modelSettingFor is the value to write into settings.json for a stored model
// preference, and whether to write one at all.
//
// "" is no preference. "default" is the menu's own sentinel for "the tool's
// own default" — it is not a model id, and writing it verbatim makes every
// turn fail with model_not_found (confirmed live, and an ordinary thing to
// pick from the composer). Both mean the same thing here, and both are best
// said by leaving the key out: Claude Code then falls through to its next
// source, which for a resumed session is the model that session was last on.
func modelSettingFor(model string) (string, bool) {
	if model == "" || model == "default" {
		return "", false
	}
	return model, true
}

// withClaudeModel merges a model into Claude Code's settings.json, leaving
// every other key exactly as it was.
//
// Merging rather than replacing is the whole point. provision.sh puts
// skipDangerousModePermissionPrompt in an agent's copy, and configureLead puts
// the lead's entire permission policy — the deny rules that keep it off the
// host's shell — in the lead's. Overwriting would hand every agent back its
// permission prompts and the lead a shell, and neither would be visible until
// something went wrong.
//
// It returns nil when the file already says this, so an unchanged model costs
// no write.
func withClaudeModel(existing []byte, model string) ([]byte, error) {
	value, want := modelSettingFor(model)
	return withClaudeKey(existing, "model", value, want)
}

// compactWindowKey is Claude Code's settings.json key for the context a
// session compacts at (D83). It is the same knob as the
// CLAUDE_CODE_AUTO_COMPACT_WINDOW variable, set where the model already is:
// the file every path that runs Claude Code reads, for the chat and the
// agent's terminal alike, and which is merged again before every chat starts —
// so an agent made before the setting changed picks it up without being
// remade.
const compactWindowKey = "autoCompactWindow"

// withClaudeCompactWindow merges the compact window into Claude Code's
// settings.json the way withClaudeModel merges the model. 0 is the model's
// whole window, which is said by leaving the key out.
func withClaudeCompactWindow(existing []byte, window int64) ([]byte, error) {
	return withClaudeKey(existing, compactWindowKey, window, window > 0)
}

// subagentLimits are the ceilings Claude Code holds subagents to, in every
// agent and lead (D84). Claude Code reads them from its environment, and
// copies settings.json's "env" into that environment as it starts, so they
// travel in the file the model and the compact window already do — merged
// again before every chat, which is what reaches agents that already exist.
//
// Three at once, where Claude Code allows twenty: each subagent starts a
// context of its own, and an agent that fans out a handful of them to answer
// one question pays for a handful of system prompts and the same files read a
// handful of times. And no subagent may start another: a spawn depth of one is
// the agent and its subagents, nothing below them.
var subagentLimits = map[string]string{
	"CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS": "3",
	"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH": "1",
}

// outputCaps hold Bash and MCP tool output well below Claude Code 2.1.280's
// own defaults (D83): Bash output alone was measured at ~29% of the worst
// agent's context (docs/implementation/token-spend.md), and the first pass at
// that review only asked agents, in the brief, to keep output short. Verified
// against the installed binary rather than assumed, BASH_MAX_OUTPUT_LENGTH
// defaults to 30000 characters (capped at 150000, no floor when set by env)
// and MAX_MCP_OUTPUT_TOKENS to 25000 tokens. 10000 characters is enough of a
// failing test's tail to be readable — Go and JS test runners put the failure
// and its stack trace last — and 15000 MCP tokens (~60000 characters) is
// comfortably above a real Playwright accessibility-tree snapshot measured at
// 49,893 bytes on a real page, so an agent still reads a normal tool result
// whole.
var outputCaps = map[string]string{
	"BASH_MAX_OUTPUT_LENGTH": "10000",
	"MAX_MCP_OUTPUT_TOKENS":  "15000",
}

// withClaudeEnv merges variables into settings.json's "env", leaving every
// other variable in it as it was, and returns nil when nothing changed.
func withClaudeEnv(existing []byte, vars map[string]string) ([]byte, error) {
	settings := map[string]json.RawMessage{}
	if trimmed := bytes.TrimSpace(existing); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &settings); err != nil {
			return nil, fmt.Errorf("its settings.json isn't a JSON object, so adding the env to it would lose what's there: %w", err)
		}
	}
	env := map[string]json.RawMessage{}
	if raw, ok := settings["env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("its settings.json has an env that isn't an object, so adding to it would lose what's there: %w", err)
		}
	}
	changed := false
	for name, value := range vars {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(env[name], raw) {
			env[name], changed = raw, true
		}
	}
	if !changed {
		return nil, nil
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return withClaudeKey(existing, "env", json.RawMessage(raw), true)
}

// withClaudeChat merges the settings a chat starts with — its model, the
// context it compacts at and the subagent limits — and returns nil when none
// changed.
func withClaudeChat(existing []byte, model string, window int64) ([]byte, error) {
	return withClaudeSettings(existing,
		func(b []byte) ([]byte, error) { return withClaudeModel(b, model) },
		func(b []byte) ([]byte, error) { return withClaudeCompactWindow(b, window) },
		func(b []byte) ([]byte, error) { return withClaudeEnv(b, subagentLimits) },
		func(b []byte) ([]byte, error) { return withClaudeEnv(b, outputCaps) },
	)
}

// withClaudeSettings applies merges one after another, each to what the last
// one produced, and returns nil when none of them changed anything. A merge
// answers nil for "nothing to change", like withClaudeKey.
func withClaudeSettings(existing []byte, merges ...func([]byte) ([]byte, error)) ([]byte, error) {
	out, changed := existing, false
	for _, merge := range merges {
		merged, err := merge(out)
		if err != nil {
			return nil, err
		}
		if merged != nil {
			out, changed = merged, true
		}
	}
	if !changed {
		return nil, nil
	}
	return out, nil
}

// withClaudeKey sets one key of Claude Code's settings.json, or removes it
// when want is false, and returns nil when the file already says that.
func withClaudeKey(existing []byte, key string, value any, want bool) ([]byte, error) {
	// json.RawMessage, not any: every other key is written back byte for byte,
	// so numbers keep their form and AgentBox never has to understand a setting
	// to preserve it.
	settings := map[string]json.RawMessage{}
	if trimmed := bytes.TrimSpace(existing); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &settings); err != nil {
			return nil, fmt.Errorf("its settings.json isn't a JSON object, so adding the %s to it would lose what's there: %w", key, err)
		}
	}
	before, had := settings[key]
	switch {
	case !want && !had:
		return nil, nil
	case !want:
		delete(settings, key)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if had && bytes.Equal(before, raw) {
			return nil, nil
		}
		settings[key] = raw
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// PrepareChatModel writes the model an agent's chat should start on, and the
// context it compacts at (D83), into its AI tool's own configuration, before
// the adapter is launched. It is the launch-time half of choosing a model;
// switching one mid-session still goes through the adapter's
// set_config_option, which this doesn't replace. compactWindow is
// state.ClaudeCompactWindow's value: 0 for the model's whole window.
//
// Choosing a model is Claude Code's alone — Codex and OpenCode start each
// chat on whatever their own configuration already says. The compact window
// and the subagent caps travel to all three, though, each in its own tool's
// shape: Claude Code's settings.json, Codex's config.toml
// (model_auto_compact_token_limit and [agents].max_concurrent_threads_per_session)
// and OpenCode's opencode.json (subagent_depth — it has no absolute compact
// window to set at all).
func (m *Manager) PrepareChatModel(ctx context.Context, a state.Agent, model string, compactWindow int64) error {
	switch a.AI {
	case "claude":
		merge := func(existing []byte) ([]byte, error) { return withClaudeChat(existing, model, compactWindow) }
		if a.IsLead() {
			return m.prepareLeadClaudeSettings(a, merge)
		}
		return m.prepareAgentClaudeSettings(ctx, a, merge)
	case "codex":
		return m.prepareAgentCodexSettings(ctx, a, compactWindow)
	case "opencode":
		return m.prepareAgentOpenCodeSettings(ctx, a)
	default:
		return nil
	}
}

// prepareAgentCodexSettings rewrites Codex's config.toml whole, the same way
// configure does at creation (agent.go): unlike Claude Code's settings.json
// there is no permission policy or other AgentBox-external state inside it,
// so there is nothing a merge would need to preserve.
func (m *Manager) prepareAgentCodexSettings(ctx context.Context, a state.Agent, compactWindow int64) error {
	home := "/home/" + m.User.Name
	config := codexConfigFor(a.Worktree, agentMCPServers(home), compactWindow)
	return m.Incus.WriteFile(ctx, a.Instance, home+"/.codex/config.toml", []byte(config), m.User.UID, m.User.GID, 0o600)
}

// prepareAgentOpenCodeSettings does the same for OpenCode's opencode.json.
func (m *Manager) prepareAgentOpenCodeSettings(ctx context.Context, a state.Agent) error {
	home := "/home/" + m.User.Name
	config, err := openCodeConfig(agentMCPServers(home), a.Autonomous)
	if err != nil {
		return err
	}
	return m.Incus.WriteFile(ctx, a.Instance, home+"/.config/opencode/opencode.json", config, m.User.UID, m.User.GID, 0o600)
}

// prepareAgentClaudeSettings merges into the settings of the agent's own user,
// inside the agent's machine. merge returns nil when there is nothing to write.
func (m *Manager) prepareAgentClaudeSettings(ctx context.Context, a state.Agent, merge func([]byte) ([]byte, error)) error {
	path := "/home/" + m.User.Name + "/" + claudeSettingsFile
	var existing bytes.Buffer
	// A machine with no settings file yet is not an error: the merge starts
	// from nothing. Anything else that went wrong shows up as unparseable
	// content, which the merge refuses rather than overwrites.
	_ = m.Incus.UserExec(ctx, a.Instance, m.User.Name, "cat "+shellQuote(path)+" 2>/dev/null", nil, &existing, io.Discard)
	merged, err := merge(existing.Bytes())
	if err != nil || merged == nil {
		return err
	}
	return m.Incus.WriteFile(ctx, a.Instance, path, merged, m.User.UID, m.User.GID, 0o600)
}

// prepareLeadClaudeSettings does the same for a project's lead, whose Claude
// Code runs on the host rather than in a machine — but in the private HOME
// configureLead wrote, not the user's own.
func (m *Manager) prepareLeadClaudeSettings(a state.Agent, merge func([]byte) ([]byte, error)) error {
	path := filepath.Join(m.Paths.LeadHome(a.Project), claudeSettingsFile)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	merged, err := merge(existing)
	if err != nil || merged == nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Written beside the file and renamed over it: a lead that loses its
	// permission policy half way through a write gets a shell on the host.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// carryClaudeModel copies the model, the compact window and the env (where
// the subagent limits are) out of Claude Code's existing settings into
// settings about to replace them.
//
// configureLead rewrites the lead's settings.json whole on every EnsureLead, so
// that a lead made by an older AgentBox picks up policy a newer one added. That
// is the right behaviour for the policy and the wrong one for the model, which
// AgentBox put there itself and which the user chose: without this, choosing a
// model for a project's chat would survive until its next message and then
// quietly vanish from the file. The compact window and the env are carried for
// the same reason — PrepareChatModel wrote them — and so is promptCacheTtl,
// which only the user writes and which the idle rollover reads.
func carryClaudeModel(existing []byte, settings map[string]any) {
	var was map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(existing), &was) != nil {
		return
	}
	for _, key := range []string{"model", compactWindowKey, "env", promptCacheTTLKey} {
		if value, ok := was[key]; ok {
			settings[key] = value
		}
	}
}

// Claude Code keeps the main conversation's prompt cache for five minutes or
// an hour. settings.json's promptCacheTtl sets which, and the
// CLAUDE_CODE_PROMPT_CACHE_TTL variable overrides it; with neither it chooses
// by itself — an hour on a subscription within its usage limits, five minutes
// on an API key or once past them. The lead's environment is the one
// LeadChatCommand builds, fresh, so the variable can only come from
// settings.json's "env", which Claude Code copies into its environment.
const (
	promptCacheTTLKey = "promptCacheTtl"
	promptCacheTTLEnv = "CLAUDE_CODE_PROMPT_CACHE_TTL"
)

// LeadPromptCacheTTL is the prompt-cache TTL a project's lead has been set to
// in its settings.json, or 0 when nothing there sets it and Claude Code picks
// one itself. A value Claude Code wouldn't accept is read as unset too.
func (m *Manager) LeadPromptCacheTTL(a state.Agent) time.Duration {
	existing, err := os.ReadFile(filepath.Join(m.Paths.LeadHome(a.Project), claudeSettingsFile))
	if err != nil {
		return 0
	}
	return promptCacheTTLOf(existing)
}

// promptCacheTTLOf reads the TTL out of a settings.json, the variable first.
func promptCacheTTLOf(settings []byte) time.Duration {
	// Raw values, so a key of some other type — anywhere, the env included —
	// can't make the two this reads unreadable.
	var doc map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(settings), &doc) != nil {
		return 0
	}
	var env map[string]json.RawMessage
	var fromEnv, fromKey string
	if json.Unmarshal(doc["env"], &env) == nil {
		_ = json.Unmarshal(env[promptCacheTTLEnv], &fromEnv)
	}
	_ = json.Unmarshal(doc[promptCacheTTLKey], &fromKey)
	if ttl := parsePromptCacheTTL(fromEnv); ttl > 0 {
		return ttl
	}
	return parsePromptCacheTTL(fromKey)
}

func parsePromptCacheTTL(v string) time.Duration {
	switch strings.TrimSpace(v) {
	case "5m":
		return 5 * time.Minute
	case "1h":
		return time.Hour
	}
	return 0
}
