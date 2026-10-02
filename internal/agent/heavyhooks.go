package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Heavy phases inside an agent take their memory from the VM's burst pool
// (admission.go, internal/daemon/burst.go). In Claude Code agents that is
// automatic: hooks around its tool calls run `agentbox heavy-hook`, which
// takes a lease before a heavy Bash command or a browser tool and gives it
// back after (internal/cli/heavy.go). The hook matches commands itself, in
// process, so a tool call that isn't heavy costs one short exec and nothing
// in the conversation.

// HeavyHookCommand is the command the hooks run.
const HeavyHookCommand = AgentBinaryPath + " heavy-hook"

// HeavyEnvFile is where, under the agent user's home, the hook leaves the
// environment heavy commands run with under a lease (test runners' worker
// counts), and BASH_ENV points, so the Bash tool's shell reads it. Empty when
// the agent holds no lease.
const HeavyEnvFile = ".cache/agentbox/heavy.env"

// heavyHookMatcher is the tools the hooks run for: Bash, whose commands the
// hook sorts, and the browser's.
const heavyHookMatcher = "Bash|mcp__playwright__.*|mcp__desktop__.*"

// heavyHookTimeout is how long Claude Code lets the PreToolUse hook run, in
// seconds: beyond the daemon's longest wait for a lease (burstMaxWait, 10
// minutes), so the wait running out is the hook's to report, not a kill.
const heavyHookTimeout = 660

// withHeavyHooks merges the heavy-phase hooks into settings.json's "hooks",
// replacing any earlier version of them and keeping every other hook, and
// returns nil when they are already there.
func withHeavyHooks(existing []byte) ([]byte, error) {
	settings := map[string]json.RawMessage{}
	if trimmed := bytes.TrimSpace(existing); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &settings); err != nil {
			return nil, fmt.Errorf("its settings.json isn't a JSON object, so adding hooks to it would lose what's there: %w", err)
		}
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := settings["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("its settings.json has hooks that aren't an object of lists, so adding to them would lose what's there: %w", err)
		}
	}
	for event, timeout := range map[string]int{"PreToolUse": heavyHookTimeout, "PostToolUse": 30, "PostToolUseFailure": 30} {
		var kept []json.RawMessage
		for _, group := range hooks[event] {
			if !strings.Contains(string(group), HeavyHookCommand) {
				kept = append(kept, group)
			}
		}
		ours, err := json.Marshal(map[string]any{
			"matcher": heavyHookMatcher,
			"hooks":   []map[string]any{{"type": "command", "command": HeavyHookCommand, "timeout": timeout}},
		})
		if err != nil {
			return nil, err
		}
		hooks[event] = append(kept, ours)
	}
	raw, err := json.Marshal(hooks)
	if err != nil {
		return nil, err
	}
	if before, ok := settings["hooks"]; ok && jsonEqual(before, raw) {
		return nil, nil
	}
	return withClaudeKey(existing, "hooks", json.RawMessage(raw), true)
}

// jsonEqual compares two JSON values whatever their spacing.
func jsonEqual(a, b []byte) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}
