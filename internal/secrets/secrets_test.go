package secrets_test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

func store(t *testing.T) (secrets.Store, *state.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key := filepath.Join(dir, "config", "secrets.key")
	return secrets.Store{State: st, KeyPath: key}, st, key
}

// TestRoundTrip is the whole promise of the store: a value goes in, comes back
// out unchanged on its way into the agent, and what sits in SQLite in between
// is not the value.
func TestRoundTrip(t *testing.T) {
	s, st, keyPath := store(t)
	ctx := context.Background()
	const secret = "sk-live-0123456789"

	stored, err := s.Set(ctx, "pawly", "", "STRIPE_SECRET_KEY", secret)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "STRIPE_SECRET_KEY" || stored.Scope() != state.ScopeProject || stored.UpdatedAt.IsZero() {
		t.Errorf("Set() = %+v", stored)
	}

	values, err := s.ForAgent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Name != "STRIPE_SECRET_KEY" || values[0].Value != secret {
		t.Fatalf("ForAgent() = %+v, want the value back whole", values)
	}

	// What's in the database is ciphertext, and it doesn't hold the value.
	row, err := st.Secret(ctx, "pawly", "", "STRIPE_SECRET_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Value), secret) {
		t.Error("the stored value contains the plaintext")
	}
	if len(row.Value) <= len(secret) {
		t.Errorf("the stored value is %d bytes, too short to be a nonce plus a sealed %d", len(row.Value), len(secret))
	}

	// The key was created on first use, readable by nobody else.
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("%s is mode %o, want 600", keyPath, mode)
	}

	// Sealing twice gives different ciphertext (a fresh nonce each time), so
	// equal values in a stolen database don't show up as equal blobs.
	if _, err := s.Set(ctx, "pawly", "agent-02", "STRIPE_SECRET_KEY", secret); err != nil {
		t.Fatal(err)
	}
	if other, _ := st.Secret(ctx, "pawly", "agent-02", "STRIPE_SECRET_KEY"); string(other.Value) == string(row.Value) {
		t.Error("the same value sealed twice gave the same ciphertext")
	}
}

// TestAnotherKeyCantOpenIt is what the encryption is actually for: the
// database on its own is not enough.
func TestAnotherKeyCantOpenIt(t *testing.T) {
	s, st, _ := store(t)
	ctx := context.Background()
	if _, err := s.Set(ctx, "pawly", "", "TOKEN", "value"); err != nil {
		t.Fatal(err)
	}

	elsewhere := filepath.Join(t.TempDir(), "secrets.key")
	copied := secrets.Store{State: st, KeyPath: elsewhere}
	_, err := copied.ForAgent(ctx, "pawly", "agent-01")
	if err == nil {
		t.Fatal("a store with a different key opened the secret")
	}
	if !strings.Contains(err.Error(), elsewhere) {
		t.Errorf("the error should name the key that failed: %v", err)
	}
	// And it says what to do, rather than reporting corruption.
	if !strings.Contains(err.Error(), "set it again") {
		t.Errorf("the error should say how to recover: %v", err)
	}
}

// TestKeyIsKeptOnceCreated checks that a second use doesn't replace the key,
// which would make everything stored before it unreadable.
func TestKeyIsKeptOnceCreated(t *testing.T) {
	s, _, keyPath := store(t)
	ctx := context.Background()
	if _, err := s.Set(ctx, "pawly", "", "ONE", "first"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "pawly", "", "TWO", "second"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(keyPath)
	if string(before) != string(after) {
		t.Error("the second write replaced the key")
	}
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(after))); err != nil || len(raw) != secrets.KeySize {
		t.Errorf("the key file isn't %d base64 bytes: %v", secrets.KeySize, err)
	}
	values, err := s.ForAgent(ctx, "pawly", "agent-01")
	if err != nil || len(values) != 2 {
		t.Fatalf("ForAgent() = %+v, %v; want both secrets still readable", values, err)
	}
}

// TestAgentSecretWinsOverTheProject checks the rule that makes per-agent
// secrets useful: one agent can be given a different key under a name its
// project also uses (a test account, say), and it gets its own.
func TestAgentSecretWinsOverTheProject(t *testing.T) {
	s, _, _ := store(t)
	ctx := context.Background()
	for _, set := range []struct{ agent, value string }{{"", "project-key"}, {"agent-01", "agent-key"}} {
		if _, err := s.Set(ctx, "pawly", set.agent, "STRIPE_SECRET_KEY", set.value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Set(ctx, "pawly", "", "SHARED", "shared-value"); err != nil {
		t.Fatal(err)
	}

	values, err := s.ForAgent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range values {
		got[v.Name] = v.Value
	}
	if got["STRIPE_SECRET_KEY"] != "agent-key" {
		t.Errorf("the agent's own value lost to its project's: %q", got["STRIPE_SECRET_KEY"])
	}
	if got["SHARED"] != "shared-value" {
		t.Errorf("the agent didn't get its project's other secret: %+v", got)
	}
	// Another agent of the same project still gets the project's value.
	other, err := s.ForAgent(ctx, "pawly", "agent-02")
	if err != nil || len(other) != 2 || other[1].Value != "project-key" {
		t.Errorf("ForAgent(agent-02) = %+v, %v; want the project's values", other, err)
	}
	// Names for the brief say each name once, sorted, and decrypt nothing.
	names, err := s.NamesForAgent(ctx, "pawly", "agent-01")
	if err != nil || len(names) != 2 || names[0] != "SHARED" || names[1] != "STRIPE_SECRET_KEY" {
		t.Errorf("NamesForAgent() = %v, %v", names, err)
	}
}

// TestEnvFileQuoting is the file that lands inside the agent. A value that
// holds quotes, newlines, a $ or a backslash has to arrive byte for byte, so
// this checks the rendering against what a shell really reads back.
func TestEnvFileQuoting(t *testing.T) {
	values := []secrets.Value{
		{Name: "PLAIN", Value: "sk-simple"},
		{Name: "SPACES", Value: "two words"},
		{Name: "SINGLE_QUOTES", Value: `it's a 'quoted' key`},
		{Name: "DOUBLE_QUOTES", Value: `say "hello"`},
		{Name: "DOLLAR", Value: `$HOME and $(whoami) and ${x}`},
		{Name: "BACKSLASH", Value: `C:\keys\id`},
		{Name: "NEWLINES", Value: "-----BEGIN KEY-----\nline two\n-----END KEY-----"},
		{Name: "BACKTICK", Value: "`id`"},
	}
	file := secrets.EnvFile(values)
	if !strings.HasPrefix(file, "# Written by AgentBox") {
		t.Errorf("the file should say who wrote it:\n%s", file)
	}
	if !strings.Contains(file, "Never print, commit or echo") {
		t.Errorf("the file should carry the rule, where the agent will see it:\n%s", file)
	}
	if !strings.Contains(file, `export SINGLE_QUOTES='it'\''s a '\''quoted'\'' key'`) {
		t.Errorf("single quotes aren't spliced the POSIX way:\n%s", file)
	}

	// Sourced by a shell, every value is exactly what went in. This is the
	// check that matters: the quoting is only as good as what /bin/sh reads.
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		script := ". " + path + `; printf '%s' "$` + v.Name + `"`
		out, err := run(t, script)
		if err != nil {
			t.Fatalf("sourcing the file for %s: %v", v.Name, err)
		}
		if out != v.Value {
			t.Errorf("$%s read back as %q, want %q", v.Name, out, v.Value)
		}
	}

	// An empty set still renders a usable file: sourcing it is a no-op, which
	// is what an agent with no secrets gets.
	empty := secrets.EnvFile(nil)
	if strings.Contains(empty, "export ") {
		t.Errorf("an empty set exported something:\n%s", empty)
	}
	if err := os.WriteFile(path, []byte(empty), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, ". "+path+"; printf ok"); err != nil {
		t.Errorf("sourcing an empty secrets file failed: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"OPENAI_API_KEY", "A", "_UNDERSCORE", "KEY2", "A_B_C_1"} {
		if err := secrets.ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want it accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "lower_case", "1LEADING_DIGIT", "HAS-HYPHEN", "HAS SPACE", "HAS.DOT", "PATH=X", strings.Repeat("A", secrets.MaxNameLen+1)} {
		if err := secrets.ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) was accepted", bad)
		}
	}
	// The variables AgentBox writes itself are refused, with the command that
	// really changes them: a secret named after one would be sourced last and
	// quietly replace a login.
	for name, mentions := range map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "claude-account",
		"GH_TOKEN":                "github-account",
		"GITHUB_TOKEN":            "github-account",
		"COMPOSE_PROJECT_NAME":    "Compose",
	} {
		err := secrets.ValidateName(name)
		if err == nil {
			t.Errorf("ValidateName(%q) was accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), mentions) {
			t.Errorf("ValidateName(%q) = %v; it should point at %s", name, err, mentions)
		}
	}
}

func TestSetRefusesWhatCantBeAVariable(t *testing.T) {
	s, _, _ := store(t)
	ctx := context.Background()
	if _, err := s.Set(ctx, "pawly", "", "lower", "value"); err == nil {
		t.Error("a name that isn't an environment variable name was accepted")
	}
	if _, err := s.Set(ctx, "pawly", "", "EMPTY", ""); err == nil {
		t.Error("an empty value was accepted")
	}
	if _, err := s.Set(ctx, "pawly", "", "NUL", "before\x00after"); err == nil {
		t.Error("a value with a NUL byte was accepted")
	}
	if _, err := s.Set(ctx, "pawly", "", "HUGE", strings.Repeat("x", secrets.MaxValueLen+1)); err == nil {
		t.Error("a value larger than the limit was accepted")
	}
	// A refused value is never echoed back in the error.
	_, err := s.Set(ctx, "pawly", "", "NUL", "sk-secret-value\x00")
	if err == nil || strings.Contains(err.Error(), "sk-secret-value") {
		t.Errorf("the error carries the value: %v", err)
	}
	if list, _ := s.List(ctx, "pawly", ""); len(list) != 0 {
		t.Errorf("a refused secret was stored anyway: %+v", list)
	}
}

// TestListAndRemove covers what the API reads and writes: names with times and
// no values, and a removal that really removes.
func TestListAndRemove(t *testing.T) {
	s, _, _ := store(t)
	ctx := context.Background()
	for _, name := range []string{"B_KEY", "A_KEY"} {
		if _, err := s.Set(ctx, "pawly", "", name, "value-of-"+name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Set(ctx, "pawly", "agent-01", "OWN_KEY", "own"); err != nil {
		t.Fatal(err)
	}

	list, err := s.List(ctx, "pawly", "")
	if err != nil || len(list) != 2 || list[0].Name != "A_KEY" {
		t.Fatalf("List() = %+v, %v; want both by name", list, err)
	}
	all, err := s.Project(ctx, "pawly")
	if err != nil || len(all) != 3 {
		t.Fatalf("Project() = %+v, %v; want both scopes", all, err)
	}
	for _, sec := range all {
		if sec.Scope() != state.ScopeProject && sec.Scope() != state.ScopeAgent {
			t.Errorf("secret %s has scope %q", sec.Name, sec.Scope())
		}
	}

	if err := s.Remove(ctx, "pawly", "", "A_KEY"); err != nil {
		t.Fatal(err)
	}
	values, err := s.ForAgent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.Name == "A_KEY" {
			t.Error("a removed secret is still delivered")
		}
	}
	if len(values) != 2 {
		t.Errorf("ForAgent() = %+v; want B_KEY and OWN_KEY", values)
	}
}

// TestNoStoreFailsLoudly checks that a Store built without state says so
// instead of delivering an empty file that looks like "you have no secrets".
func TestNoStoreFailsLoudly(t *testing.T) {
	var unset secrets.Store
	if _, err := unset.ForAgent(context.Background(), "pawly", "agent-01"); err == nil {
		t.Error("a store with no state answered as if there were no secrets")
	}
}

// run executes a script with /bin/sh and returns its stdout: the only honest
// check of the quoting is a shell reading the file back.
func run(t *testing.T, script string) (string, error) {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	return string(out), err
}
