package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hookRepo is a git repository whose commits go through the hooks the way an
// agent's do: core.hooksPath at a directory holding commit-msg and chain,
// with chain linked under every name in chainedHooks.
func hookRepo(t *testing.T) (repo string, git func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	repo = filepath.Join(root, "repo")
	for _, dir := range []string{hooks, repo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(hooks, "commit-msg"), commitMsgHook)
	write(filepath.Join(hooks, "chain"), chainHook)
	for _, name := range chainedHooks {
		if err := os.Symlink("chain", filepath.Join(hooks, name)); err != nil {
			t.Fatal(err)
		}
	}
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("config", "core.hooksPath", hooks)
	return repo, git
}

func TestCommitMsgHookStripsAIToolCredit(t *testing.T) {
	_, git := hookRepo(t)
	for _, c := range []struct{ name, message, want string }{
		{"claude", "feat: a thing\n\nWhy it's done.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>\n",
			"feat: a thing\n\nWhy it's done.\n"},
		{"codex", "fix: b\n\nCo-authored-by: Codex <noreply@openai.com>\n", "fix: b\n"},
		{"cursor and a bot", "fix: c\n\nCo-authored-by: Cursor Agent <cursoragent@cursor.com>\nCo-authored-by: claude[bot] <209825114+claude[bot]@users.noreply.github.com>\n", "fix: c\n"},
		{"session link", "fix: d\n\nClaude-Session: https://claude.ai/code/session_01\n", "fix: d\n"},
		// A person co-authoring stays, beside the tool that's taken out, and
		// so does one who happens to be called Claude.
		{"humans stay", "feat: e\n\nCo-authored-by: Claude Monet <claude@example.com>\nCo-Authored-By: Claude <noreply@anthropic.com>\nCo-authored-by: Ada <ada@example.com>\nSigned-off-by: Dev <dev@example.com>\n",
			"feat: e\n\nCo-authored-by: Claude Monet <claude@example.com>\nCo-authored-by: Ada <ada@example.com>\nSigned-off-by: Dev <dev@example.com>\n"},
		{"nothing to take out", "chore: f\n\nGenerated with care, by hand.\n", "chore: f\n\nGenerated with care, by hand.\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			git("commit", "-q", "--allow-empty", "-m", c.message)
			if got := git("log", "-1", "--format=%B"); strings.TrimRight(got, "\n") != strings.TrimRight(c.want, "\n") {
				t.Errorf("committed message:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// TestHooksChainToTheRepositorysOwn: pointing core.hooksPath at AgentBox's
// hooks mustn't switch off the repository's: its commit-msg still runs, first,
// and can still refuse a commit, and every other hook runs through chain.
func TestHooksChainToTheRepositorysOwn(t *testing.T) {
	repo, git := hookRepo(t)
	own := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(repo, ".git", "pre-commit-ran")
	if err := os.WriteFile(filepath.Join(own, "pre-commit"), []byte("#!/bin/sh\ntouch "+ran+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The repository's commit-msg appends a trailer of its own: it stays, and
	// the AI one it sits beside goes.
	if err := os.WriteFile(filepath.Join(own, "commit-msg"), []byte("#!/bin/sh\ngrep -q refuse \"$1\" && exit 1\nprintf 'Change-Id: I1\\n' >>\"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "--allow-empty", "-m", "feat: g\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	if got, want := strings.TrimSpace(git("log", "-1", "--format=%B")), "feat: g\n\nChange-Id: I1"; got != want {
		t.Errorf("committed message:\n%s\nwant:\n%s", got, want)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("the repository's own pre-commit didn't run: %v", err)
	}

	cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "refuse this")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("the repository's commit-msg refused the commit, and it was made anyway:\n%s", out)
	}
}

func TestClaudeAttributionFollowsTheSetting(t *testing.T) {
	existing := []byte(`{"model":"opus","includeCoAuthoredBy":true}`)
	off, err := withClaudeAttribution(false)(existing)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(off, &got); err != nil {
		t.Fatal(err)
	}
	attribution, _ := got["attribution"].(map[string]any)
	if attribution["commit"] != "" || attribution["pr"] != "" || attribution["sessionUrl"] != false || got["includeCoAuthoredBy"] != false || got["model"] != "opus" {
		t.Errorf("with credit off, settings.json is %s", off)
	}
	if again, err := withClaudeAttribution(false)(off); err != nil || again != nil {
		t.Errorf("merging the same setting again = %s, %v; want nothing to write", again, err)
	}

	on, err := withClaudeAttribution(true)(off)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := json.Unmarshal(on, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["attribution"]; ok {
		t.Errorf("with credit on, attribution is still set: %s", on)
	}
	if _, ok := got["includeCoAuthoredBy"]; ok || got["model"] != "opus" {
		t.Errorf("with credit on, settings.json is %s", on)
	}
}

func TestGitConfigNamesTheHooksOnlyWhenGiven(t *testing.T) {
	if got := gitConfig(true, "/home/dev/"+gitHooksDir); !strings.Contains(got, "[core]\n\thooksPath = \"/home/dev/.config/agentbox/git-hooks\"\n") {
		t.Errorf("gitConfig with hooks:\n%s", got)
	}
	if got := gitConfig(true, ""); strings.Contains(got, "hooksPath") {
		t.Errorf("gitConfig without hooks:\n%s", got)
	}
}
