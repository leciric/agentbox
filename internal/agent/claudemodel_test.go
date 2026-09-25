package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// settingsOf parses what withClaudeModel wrote, so a test asserts on the
// settings rather than on their formatting.
func settingsOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the merged settings aren't JSON: %v\n%s", err, raw)
	}
	return out
}

// TestWithClaudeModelKeepsEverythingElse is the one that matters most:
// provision.sh writes skipDangerousModePermissionPrompt into every agent's
// settings.json and configureLead writes the lead's whole permission policy
// into its own, so a model that replaced the file rather than merging into it
// would hand agents back their permission prompts and the lead a shell on the
// host — neither of which shows up until something goes wrong.
func TestWithClaudeModelKeepsEverythingElse(t *testing.T) {
	existing := []byte(`{"skipDangerousModePermissionPrompt":true,"permissions":{"deny":["Bash","Write"]},"includeCoAuthoredBy":false}`)
	merged, err := withClaudeModel(existing, "claude-fable-5-1")
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, merged)
	if got["model"] != "claude-fable-5-1" {
		t.Errorf("model = %v", got["model"])
	}
	if got["skipDangerousModePermissionPrompt"] != true {
		t.Error("skipDangerousModePermissionPrompt was lost: every agent would get permission prompts back")
	}
	if got["includeCoAuthoredBy"] != false {
		t.Error("includeCoAuthoredBy was lost")
	}
	deny, _ := got["permissions"].(map[string]any)["deny"].([]any)
	if len(deny) != 2 || deny[0] != "Bash" {
		t.Errorf("the lead's deny rules were lost: %v", got["permissions"])
	}
}

func TestWithClaudeModelStartsFromNothing(t *testing.T) {
	for _, existing := range [][]byte{nil, {}, []byte("  \n")} {
		merged, err := withClaudeModel(existing, "opus[1m]")
		if err != nil {
			t.Fatalf("withClaudeModel(%q) = %v", existing, err)
		}
		if got := settingsOf(t, merged); got["model"] != "opus[1m]" {
			t.Errorf("withClaudeModel(%q) model = %v", existing, got["model"])
		}
	}
}

// TestWithClaudeModelLeavesNoKeyForNoChoice covers the two values that mean
// "no model of my own". "default" is the menu's own sentinel, not a model id:
// written verbatim it is resolved as a model name, isn't found, and every turn
// fails with model_not_found — confirmed against the real adapter, and an
// ordinary thing to pick from the composer. Leaving the key out instead lets
// Claude Code fall through to its next source, which for a resumed session is
// the model that session was last on.
func TestWithClaudeModelLeavesNoKeyForNoChoice(t *testing.T) {
	for _, model := range []string{"", "default"} {
		merged, err := withClaudeModel([]byte(`{"model":"claude-fable-5-1","skipDangerousModePermissionPrompt":true}`), model)
		if err != nil {
			t.Fatal(err)
		}
		got := settingsOf(t, merged)
		if _, ok := got["model"]; ok {
			t.Errorf("withClaudeModel(%q) left model = %v", model, got["model"])
		}
		if got["skipDangerousModePermissionPrompt"] != true {
			t.Errorf("withClaudeModel(%q) lost the other settings", model)
		}
	}
}

// TestWithClaudeModelSkipsAnUnchangedFile keeps a chat that starts on the model
// it already had from writing into an agent's machine every time.
func TestWithClaudeModelSkipsAnUnchangedFile(t *testing.T) {
	if merged, err := withClaudeModel([]byte(`{"model":"opus","x":1}`), "opus"); merged != nil || err != nil {
		t.Errorf("withClaudeModel(same model) = %s, %v", merged, err)
	}
	if merged, err := withClaudeModel([]byte(`{"skipDangerousModePermissionPrompt":true}`), ""); merged != nil || err != nil {
		t.Errorf("withClaudeModel(no model, no key) = %s, %v", merged, err)
	}
}

// TestWithClaudeModelRefusesToParseOverUnreadableSettings: a file AgentBox
// can't parse is one it can't merge into, and writing a fresh one over it is
// exactly the clobber this is all here to avoid.
func TestWithClaudeModelRefusesUnreadableSettings(t *testing.T) {
	for _, existing := range []string{`{"model":`, `["not","an","object"]`, `"a string"`} {
		if _, err := withClaudeModel([]byte(existing), "opus"); err == nil {
			t.Errorf("withClaudeModel(%q) = nil error, want a refusal rather than an overwrite", existing)
		}
	}
}

// TestPrepareLeadChatModelMergesInPlace runs the host path for real, on the
// settings configureLead actually writes.
func TestPrepareLeadChatModelMergesInPlace(t *testing.T) {
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	a := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead, AI: "claude"}
	path := filepath.Join(m.Paths.LeadHome(a.Project), claudeSettingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(leadSettings())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, policy, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.PrepareChatModel(t.Context(), a, "claude-fable-5-1", 200_000); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, raw)
	if got["model"] != "claude-fable-5-1" {
		t.Errorf("model = %v", got["model"])
	}
	if got[compactWindowKey] != float64(200_000) {
		t.Errorf("%s = %v, want 200000", compactWindowKey, got[compactWindowKey])
	}
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil || perms["defaultMode"] != "default" {
		t.Fatalf("the lead lost its permission policy, which is what makes it ask first: %s", raw)
	}

	// And going back to no choice takes the keys out again, still in place:
	// no model is the tool's own default, and a window of 0 is the model's
	// whole window.
	if err := m.PrepareChatModel(t.Context(), a, "default", 0); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got = settingsOf(t, raw)
	if _, ok := got["model"]; ok {
		t.Errorf("model = %v, want no key at all", got["model"])
	}
	if _, ok := got[compactWindowKey]; ok {
		t.Errorf("%s = %v, want no key at all", compactWindowKey, got[compactWindowKey])
	}
	if perms, _ := got["permissions"].(map[string]any); perms == nil {
		t.Error("the lead lost its permission policy on the way back")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("settings mode = %v, %v", info.Mode().Perm(), err)
	}
}

// fakeIncus stands in for the incus binary with a shell script, the same
// stand-in agent_test.go uses — duplicated here because a package's own test
// files (agent) and its external ones (agent_test) don't share identifiers.
func fakeIncus(t *testing.T, script string) incus.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "incus")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return incus.Client{Bin: path}
}

// TestPrepareChatModelOnlyClaudeCodeHasAModel: a chosen model is Claude
// Code's settings.json alone — Codex and OpenCode start each chat on
// whatever model their own configuration already names, and this only ever
// dispatches to their own config file, never to Claude Code's channel.
func TestPrepareChatModelOnlyClaudeCodeHasAModel(t *testing.T) {
	for _, ai := range []string{"codex", "opencode"} {
		m := &Manager{
			Paths: paths.Paths{Data: t.TempDir()},
			Incus: fakeIncus(t, "exit 0"),
			User:  image.User{Name: "dev", UID: 1000, GID: 1000},
		}
		a := state.Agent{Project: "hello-stack", Name: "agent-01", AI: ai, Instance: "ab-hello-stack-agent-01", Worktree: "/worktree"}
		if err := m.PrepareChatModel(t.Context(), a, "claude-fable-5-1", 200_000); err != nil {
			t.Errorf("PrepareChatModel(%s) = %v", ai, err)
		}
		if _, err := os.Stat(filepath.Join(m.Paths.LeadHome(a.Project), claudeSettingsFile)); !os.IsNotExist(err) {
			t.Errorf("a %s agent got Claude Code settings: %v", ai, err)
		}
	}
}

// TestPrepareChatModelWritesCodexAndOpenCodeConfig checks what actually
// reaches Codex's and OpenCode's own configuration through PrepareChatModel,
// not just that nothing goes wrong: the compact window and subagent caps
// have to be in the file a chat is about to start on, not only the one
// configure wrote at creation, or an agent made before the window changed
// would never pick it up (D83).
func TestPrepareChatModelWritesCodexAndOpenCodeConfig(t *testing.T) {
	dir := t.TempDir()
	// Captures what WriteFile sent on stdin, keyed by the path it targeted —
	// see incus.Client.WriteFile: exec NAME -T -- sh -c SCRIPT sh PATH UID:GID MODE.
	script := `
if [ "$1" = "exec" ]; then
  n=$#
  eval path=\${$((n-2))}
  out="` + dir + `/$(echo "$path" | tr '/' '_')"
  cat > "$out"
fi
exit 0
`
	m := &Manager{
		Paths: paths.Paths{Data: t.TempDir()},
		Incus: fakeIncus(t, script),
		User:  image.User{Name: "dev", UID: 1000, GID: 1000},
	}

	a := state.Agent{Project: "hello-stack", Name: "agent-01", AI: "codex", Instance: "ab-hello-stack-agent-01", Worktree: "/worktree"}
	if err := m.PrepareChatModel(t.Context(), a, "unused", 150_000); err != nil {
		t.Fatal(err)
	}
	codex, err := os.ReadFile(filepath.Join(dir, "_home_dev_.codex_config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "model_auto_compact_token_limit = 150000") {
		t.Errorf("codex config.toml = %s, want the compact window", codex)
	}
	if !strings.Contains(string(codex), "max_concurrent_threads_per_session = 3") {
		t.Errorf("codex config.toml = %s, want the subagent cap", codex)
	}
	if strings.Index(string(codex), "model_auto_compact_token_limit") > strings.Index(string(codex), "[") {
		t.Errorf("codex config.toml has a bare key after a [table]: %s", codex)
	}

	a.AI = "opencode"
	if err := m.PrepareChatModel(t.Context(), a, "unused", 150_000); err != nil {
		t.Fatal(err)
	}
	opencode, err := os.ReadFile(filepath.Join(dir, "_home_dev_.config_opencode_opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(opencode), `"subagent_depth":1`) {
		t.Errorf("opencode.json = %s, want the subagent depth cap", opencode)
	}
}

// TestPrepareLeadChatModelReportsUnreadableSettings: the failure has to reach
// the caller, which turns it into a notice in the chat. Starting on the wrong
// model in silence is the failure this whole path is guarding against.
func TestPrepareLeadChatModelReportsUnreadableSettings(t *testing.T) {
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	a := state.Agent{Project: "hello-stack", Name: state.LeadName, Role: state.RoleLead, AI: "claude"}
	path := filepath.Join(m.Paths.LeadHome(a.Project), claudeSettingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.PrepareChatModel(t.Context(), a, "opus", 200_000)
	if err == nil || !strings.Contains(err.Error(), "would lose what's there") {
		t.Fatalf("PrepareChatModel over unreadable settings = %v, want the merge's own refusal", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "not json at all" {
		t.Errorf("the unreadable settings were overwritten: %q", raw)
	}
}

// TestConfigureLeadKeepsTheChosenModel: configureLead rewrites the lead's
// settings whole on every EnsureLead — which happens on every message — so
// without carrying the model across, choosing one for a project's chat would
// survive until its next message and then quietly vanish from the file.
func TestConfigureLeadKeepsTheChosenModel(t *testing.T) {
	policy := leadSettings()
	carryClaudeModel([]byte(`{"model":"claude-fable-5-1","autoCompactWindow":200000,"promptCacheTtl":"5m","permissions":{"deny":[]}}`), policy)
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, raw)
	if got["model"] != "claude-fable-5-1" {
		t.Errorf("the chosen model didn't survive a rewrite: %v", got["model"])
	}
	if got[compactWindowKey] != float64(200_000) {
		t.Errorf("the compact window didn't survive a rewrite: %v", got[compactWindowKey])
	}
	if got[promptCacheTTLKey] != "5m" {
		t.Errorf("the prompt cache TTL didn't survive a rewrite: %v", got[promptCacheTTLKey])
	}
	// The policy is still the new one, not whatever was on disk.
	if mode, _ := got["permissions"].(map[string]any)["defaultMode"].(string); mode != "default" {
		t.Error("the rewrite lost the policy it exists to apply")
	}

	// A lead with no model chosen gains no key, and unreadable settings are
	// simply not carried from, rather than failing the repair.
	plain := leadSettings()
	carryClaudeModel([]byte(`{"permissions":{}}`), plain)
	if _, ok := plain["model"]; ok {
		t.Errorf("a lead with no model chosen gained one: %v", plain["model"])
	}
	carryClaudeModel([]byte("not json"), plain)
	if _, ok := plain["model"]; ok {
		t.Error("unreadable settings produced a model")
	}
}

// TestWithClaudeChatMergesBoth: the model and the compact window are merged
// independently, a write happens only when one of them changed, and every key
// AgentBox doesn't own is kept as it was.
func TestWithClaudeChatMergesBoth(t *testing.T) {
	existing := []byte(`{"model":"sonnet","skipDangerousModePermissionPrompt":true}`)
	merged, err := withClaudeChat(existing, "sonnet", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, merged)
	if got["model"] != "sonnet" || got[compactWindowKey] != float64(200_000) || got["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("withClaudeChat = %s", merged)
	}
	if again, err := withClaudeChat(merged, "sonnet", 200_000); again != nil || err != nil {
		t.Errorf("withClaudeChat(unchanged) = %s, %v; want no write", again, err)
	}
	// A window of 0 is the model's whole window: the key goes, the model stays.
	whole, err := withClaudeChat(merged, "sonnet", 0)
	if err != nil {
		t.Fatal(err)
	}
	got = settingsOf(t, whole)
	if _, ok := got[compactWindowKey]; ok || got["model"] != "sonnet" {
		t.Errorf("withClaudeChat(window 0) = %s", whole)
	}
}

// TestWithClaudeEnvMergesTheSubagentLimits: the limits go into settings.json's
// env beside whatever else is there, a second merge writes nothing, and an env
// that isn't an object is refused rather than overwritten.
func TestWithClaudeEnvMergesTheSubagentLimits(t *testing.T) {
	existing := []byte(`{"model":"sonnet","env":{"FOO":"bar","CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS":"20"}}`)
	merged, err := withClaudeEnv(existing, subagentLimits)
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, merged)
	env, _ := got["env"].(map[string]any)
	if env["FOO"] != "bar" || env["CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS"] != "3" || env["CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"] != "1" || got["model"] != "sonnet" {
		t.Errorf("withClaudeEnv = %s", merged)
	}
	if again, err := withClaudeEnv(merged, subagentLimits); again != nil || err != nil {
		t.Errorf("withClaudeEnv(unchanged) = %s, %v; want no write", again, err)
	}
	if _, err := withClaudeEnv([]byte(`{"env":"nope"}`), subagentLimits); err == nil {
		t.Error("an env that isn't an object was overwritten rather than refused")
	}
	// And a chat's own merge carries them, from nothing at all.
	chat, err := withClaudeChat(nil, "sonnet", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	if env, _ := settingsOf(t, chat)["env"].(map[string]any); env["CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"] != "1" {
		t.Errorf("withClaudeChat = %s, want the subagent limits in it", chat)
	}
	// The lead's settings are rewritten whole, and the env comes across.
	policy := leadSettings()
	carryClaudeModel(merged, policy)
	if raw, _ := json.Marshal(policy); !strings.Contains(string(raw), `"CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS":"3"`) {
		t.Errorf("the env didn't survive the lead's rewrite: %s", raw)
	}
}

// TestWithClaudeEnvMergesTheOutputCaps: BASH_MAX_OUTPUT_LENGTH and
// MAX_MCP_OUTPUT_TOKENS go into settings.json's env the same way the subagent
// limits do (D83), so Bash and MCP tool output can't refill the context the
// desktop subagent and the compact window were built to hold down.
func TestWithClaudeEnvMergesTheOutputCaps(t *testing.T) {
	existing := []byte(`{"model":"sonnet","env":{"FOO":"bar","BASH_MAX_OUTPUT_LENGTH":"30000"}}`)
	merged, err := withClaudeEnv(existing, outputCaps)
	if err != nil {
		t.Fatal(err)
	}
	got := settingsOf(t, merged)
	env, _ := got["env"].(map[string]any)
	if env["FOO"] != "bar" || env["BASH_MAX_OUTPUT_LENGTH"] != "10000" || env["MAX_MCP_OUTPUT_TOKENS"] != "15000" || got["model"] != "sonnet" {
		t.Errorf("withClaudeEnv = %s", merged)
	}
	if again, err := withClaudeEnv(merged, outputCaps); again != nil || err != nil {
		t.Errorf("withClaudeEnv(unchanged) = %s, %v; want no write", again, err)
	}
	// And a chat's own merge carries them, from nothing at all, beside the
	// subagent limits.
	chat, err := withClaudeChat(nil, "sonnet", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	chatEnv, _ := settingsOf(t, chat)["env"].(map[string]any)
	if chatEnv["BASH_MAX_OUTPUT_LENGTH"] != "10000" || chatEnv["MAX_MCP_OUTPUT_TOKENS"] != "15000" || chatEnv["CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"] != "1" {
		t.Errorf("withClaudeChat = %s, want the output caps beside the subagent limits", chat)
	}
	// The lead's settings are rewritten whole, and the env comes across.
	policy := leadSettings()
	carryClaudeModel(merged, policy)
	if raw, _ := json.Marshal(policy); !strings.Contains(string(raw), `"MAX_MCP_OUTPUT_TOKENS":"15000"`) {
		t.Errorf("the env didn't survive the lead's rewrite: %s", raw)
	}
}
