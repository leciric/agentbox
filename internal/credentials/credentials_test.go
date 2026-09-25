package credentials_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/credentials"
)

func store(t *testing.T) credentials.Store {
	t.Helper()
	return credentials.Store{Dir: filepath.Join(t.TempDir(), "credentials")}
}

func TestNoAccounts(t *testing.T) {
	s := store(t)
	accounts, err := s.ClaudeAccounts()
	if err != nil || len(accounts) != 0 {
		t.Fatalf("ClaudeAccounts() = %v, %v", accounts, err)
	}
	if def, err := s.DefaultClaudeAccount(); def != "" || err != nil {
		t.Errorf("DefaultClaudeAccount() = %q, %v", def, err)
	}
	if token, err := s.ClaudeToken(""); token != "" || err != nil {
		t.Errorf("ClaudeToken() = %q, %v", token, err)
	}
}

func TestSeveralAccounts(t *testing.T) {
	s := store(t)
	if err := s.SaveClaudeToken("", "sk-ant-oat01-first"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveClaudeToken("work", "sk-ant-oat01-work"); err != nil {
		t.Fatal(err)
	}

	// The first account saved is the default until another one is picked.
	accounts, err := s.ClaudeAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].Name != "default" || !accounts[0].Default || accounts[1].Name != "work" || accounts[1].Default {
		t.Fatalf("ClaudeAccounts() = %+v", accounts)
	}
	if token, _ := s.ClaudeToken(""); token != "sk-ant-oat01-first" {
		t.Errorf("the default account's token = %q", token)
	}
	if token, _ := s.ClaudeToken("work"); token != "sk-ant-oat01-work" {
		t.Errorf("work's token = %q", token)
	}

	if err := s.SetDefaultClaudeAccount("work"); err != nil {
		t.Fatal(err)
	}
	if token, _ := s.ClaudeToken(""); token != "sk-ant-oat01-work" {
		t.Errorf("after making work the default, the default token = %q", token)
	}
	if err := s.SetDefaultClaudeAccount("nope"); err == nil || !strings.Contains(err.Error(), "no Claude Code account") {
		t.Errorf("SetDefaultClaudeAccount(nope) = %v", err)
	}

	// Removing the default leaves the remaining account as the default.
	if err := s.RemoveClaudeAccount("work"); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.DefaultClaudeAccount(); def != "default" {
		t.Errorf("default after removing work = %q", def)
	}
	if ok, _ := s.HasClaudeAccount("work"); ok {
		t.Error("work is still stored")
	}
	if err := s.RemoveClaudeAccount("work"); err == nil {
		t.Error("removing an unknown account succeeded")
	}
}

func TestTokensAreCheckedAndPrivate(t *testing.T) {
	s := store(t)
	for _, token := range []string{"", "   ", "two words", "with\nnewline"} {
		if err := s.SaveClaudeToken("personal", token); err == nil {
			t.Errorf("SaveClaudeToken(%q) was accepted", token)
		}
	}
	for _, account := range []string{"Work", "with space", "a/b", "..", "-x", strings.Repeat("a", 40)} {
		if err := s.SaveClaudeToken(account, "sk-ant-oat01-x"); err == nil {
			t.Errorf("account name %q was accepted", account)
		}
	}
	if err := s.SaveClaudeToken("personal", "  sk-ant-oat01-padded  "); err != nil {
		t.Fatal(err)
	}
	if token, _ := s.ClaudeToken("personal"); token != "sk-ant-oat01-padded" {
		t.Errorf("token = %q, want it trimmed", token)
	}
	info, err := os.Stat(s.ClaudeTokenPath("personal"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}
}

// The single-token layout used before named accounts becomes the "default" account.
func TestMigratesTheOldTokenFile(t *testing.T) {
	s := store(t)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(s.Dir, "claude-oauth-token")
	if err := os.WriteFile(old, []byte("sk-ant-oat01-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	token, err := s.ClaudeToken("")
	if err != nil || token != "sk-ant-oat01-old" {
		t.Fatalf("ClaudeToken() = %q, %v", token, err)
	}
	accounts, _ := s.ClaudeAccounts()
	if len(accounts) != 1 || accounts[0].Name != credentials.DefaultAccount || !accounts[0].Default {
		t.Errorf("ClaudeAccounts() = %+v", accounts)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old token file is still there: %v", err)
	}
}

func TestNoGitHubAccounts(t *testing.T) {
	s := store(t)
	accounts, err := s.GitHubAccounts()
	if err != nil || len(accounts) != 0 {
		t.Fatalf("GitHubAccounts() = %v, %v", accounts, err)
	}
	if def, err := s.DefaultGitHubAccount(); def != "" || err != nil {
		t.Errorf("DefaultGitHubAccount() = %q, %v", def, err)
	}
	if token, err := s.GitHubToken(""); token != "" || err != nil {
		t.Errorf("GitHubToken() = %q, %v", token, err)
	}
	if s.HasGitHubLogin() {
		t.Error("HasGitHubLogin() = true with nothing stored")
	}
}

func TestSeveralGitHubAccounts(t *testing.T) {
	s := store(t)
	if err := s.SaveGitHubToken("", "gho_first"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGitHubToken("work", "gho_work"); err != nil {
		t.Fatal(err)
	}

	// The first account saved is the default until another one is picked.
	accounts, err := s.GitHubAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].Name != "default" || !accounts[0].Default || accounts[1].Name != "work" || accounts[1].Default {
		t.Fatalf("GitHubAccounts() = %+v", accounts)
	}
	if !s.HasGitHubLogin() {
		t.Error("HasGitHubLogin() = false after saving")
	}
	if token, _ := s.GitHubToken(""); token != "gho_first" {
		t.Errorf("the default account's token = %q", token)
	}
	if token, _ := s.GitHubToken("work"); token != "gho_work" {
		t.Errorf("work's token = %q", token)
	}
	if accounts[1].SavedAt.IsZero() {
		t.Error("work's SavedAt is zero")
	}

	if err := s.SetDefaultGitHubAccount("work"); err != nil {
		t.Fatal(err)
	}
	if token, _ := s.GitHubToken(""); token != "gho_work" {
		t.Errorf("after making work the default, the default token = %q", token)
	}
	if err := s.SetDefaultGitHubAccount("nope"); err == nil || !strings.Contains(err.Error(), "no GitHub account") {
		t.Errorf("SetDefaultGitHubAccount(nope) = %v", err)
	}

	// Removing the default leaves the remaining account as the default.
	if err := s.RemoveGitHubAccount("work"); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.DefaultGitHubAccount(); def != "default" {
		t.Errorf("default after removing work = %q", def)
	}
	if ok, _ := s.HasGitHubAccount("work"); ok {
		t.Error("work is still stored")
	}
	if err := s.RemoveGitHubAccount("work"); err == nil {
		t.Error("removing an unknown account succeeded")
	}
}

func TestGitHubTokensAreCheckedAndPrivate(t *testing.T) {
	s := store(t)
	for _, token := range []string{"", "   ", "two words", "with\nnewline"} {
		if err := s.SaveGitHubToken("personal", token); err == nil {
			t.Errorf("SaveGitHubToken(%q) was accepted", token)
		}
	}
	for _, account := range []string{"Work", "with space", "a/b", "..", "-x", strings.Repeat("a", 40)} {
		if err := s.SaveGitHubToken(account, "gho_x"); err == nil {
			t.Errorf("account name %q was accepted", account)
		}
	}
	if err := s.SaveGitHubToken("personal", "  gho_padded  "); err != nil {
		t.Fatal(err)
	}
	if token, _ := s.GitHubToken("personal"); token != "gho_padded" {
		t.Errorf("token = %q, want it trimmed", token)
	}
	info, err := os.Stat(s.GitHubTokenPath("personal"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}
}

// The single-token layout used before named accounts becomes the "default" account.
func TestMigratesTheOldGitHubTokenFile(t *testing.T) {
	s := store(t)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(s.Dir, "github.token")
	if err := os.WriteFile(old, []byte("gho_old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	token, err := s.GitHubToken("")
	if err != nil || token != "gho_old" {
		t.Fatalf("GitHubToken() = %q, %v", token, err)
	}
	accounts, _ := s.GitHubAccounts()
	if len(accounts) != 1 || accounts[0].Name != credentials.DefaultAccount || !accounts[0].Default {
		t.Errorf("GitHubAccounts() = %+v", accounts)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old token file is still there: %v", err)
	}
}

// A GitHub account remembers who it is on GitHub, checked once when its token
// is saved, so the app can name the user a project reads pull requests as
// without a call per poll. A new token can belong to someone else, so saving
// one drops the login until the caller records the one it checked.
func TestGitHubAccountRemembersItsLogin(t *testing.T) {
	s := credentials.Store{Dir: t.TempDir()}
	if err := s.SaveGitHubToken("work", "gho_work"); err != nil {
		t.Fatal(err)
	}
	if login, err := s.GitHubLogin("work"); err != nil || login != "" {
		t.Errorf("GitHubLogin() = %q, %v; want empty until one is saved", login, err)
	}
	if err := s.SaveGitHubLogin("work", "leciric-work"); err != nil {
		t.Fatal(err)
	}
	if login, err := s.GitHubLogin("work"); err != nil || login != "leciric-work" {
		t.Errorf("GitHubLogin() = %q, %v; want leciric-work", login, err)
	}
	// The only account is the default one, so the empty name finds it too.
	if login, err := s.GitHubLogin(""); err != nil || login != "leciric-work" {
		t.Errorf("GitHubLogin(\"\") = %q, %v; want the default account's login", login, err)
	}
	accounts, err := s.GitHubAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].Login != "leciric-work" {
		t.Fatalf("GitHubAccounts() = %+v, %v", accounts, err)
	}
	if err := s.SaveGitHubToken("work", "gho_someone_else"); err != nil {
		t.Fatal(err)
	}
	if login, err := s.GitHubLogin("work"); err != nil || login != "" {
		t.Errorf("GitHubLogin() after a new token = %q, %v; want it forgotten", login, err)
	}
	if err := s.SaveGitHubLogin("work", "leciric-work"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveGitHubAccount("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.GitHubLoginPath("work")); !os.IsNotExist(err) {
		t.Errorf("the login outlived the account it belongs to: %v", err)
	}
}

// OpenCode keeps every provider's key in one auth.json, and AgentBox owns the
// XDG data directory it reads that from — never the host's. A file with no
// provider in it is not a login: that is what a logout leaves behind.
func TestOpenCodeLogin(t *testing.T) {
	dir := t.TempDir()
	s := credentials.Store{Dir: dir}

	if s.HasOpenCodeLogin() {
		t.Error("an empty credentials directory reads as a login")
	}
	if got := s.OpenCodeAuthPath(); got != filepath.Join(s.OpenCodeDataHome(), "opencode", "auth.json") {
		t.Errorf("auth.json is at %q, which isn't where opencode reads it from under XDG_DATA_HOME", got)
	}
	if !strings.HasPrefix(s.OpenCodeDataHome(), dir) {
		t.Errorf("OpenCode's data home %q is outside AgentBox's own directory", s.OpenCodeDataHome())
	}
	if err := os.MkdirAll(s.OpenCodeHome(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		content string
		want    bool
	}{
		{`{}`, false},
		{`not json`, false},
		{`{"anthropic": {"type": "api", "key": "sk-test"}}`, true},
	} {
		if err := os.WriteFile(s.OpenCodeAuthPath(), []byte(c.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := s.HasOpenCodeLogin(); got != c.want {
			t.Errorf("HasOpenCodeLogin() with %s = %v, want %v", c.content, got, c.want)
		}
	}
}
