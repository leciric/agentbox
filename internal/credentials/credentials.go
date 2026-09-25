// Package credentials stores the AI tool logins AgentBox gives to agents.
//
// These are AgentBox's own logins, never the host's ~/.claude or ~/.codex:
// mounting those into agents would let an agent rewrite host settings and
// reach the host's Claude daemon control key.
//
// Claude Code logins are named accounts, so one machine can hold several
// Anthropic accounts: a project picks which one its agents use, and an agent
// can be moved to another one. Codex and OpenCode still have a single login
// each.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentbox/internal/naming"
)

const (
	// MaxAccountLen keeps account names readable in lists and dialogs.
	MaxAccountLen = 24
	// DefaultAccount is the account `agentbox auth claude` writes to when you
	// don't name one, and where a single-account setup lands.
	DefaultAccount = "default"

	tokenSuffix  = ".token"
	loginSuffix  = ".login"
	defaultFile  = "default-account"
	legacyTokens = "claude-oauth-token" // single-account layout, before named accounts
)

type Store struct {
	Dir string
}

// ClaudeDir holds one file per Claude account, plus the default marker.
func (s Store) ClaudeDir() string     { return filepath.Join(s.Dir, "claude") }
func (s Store) CodexHome() string     { return filepath.Join(s.Dir, "codex") }
func (s Store) CodexAuthPath() string { return filepath.Join(s.CodexHome(), "auth.json") }

// OpenCodeDataHome is the XDG data directory AgentBox runs OpenCode with, and
// OpenCodeHome is what OpenCode makes inside it: it reads and writes its
// auth.json at $XDG_DATA_HOME/opencode/auth.json, so pointing that one
// variable at AgentBox's own directory keeps the host's ~/.local/share/opencode
// out of it entirely (D6), the way CODEX_HOME does for Codex.
func (s Store) OpenCodeDataHome() string { return filepath.Join(s.Dir, "opencode-data") }
func (s Store) OpenCodeHome() string     { return filepath.Join(s.OpenCodeDataHome(), "opencode") }
func (s Store) OpenCodeAuthPath() string { return filepath.Join(s.OpenCodeHome(), "auth.json") }

// ClaudeTokenPath is where an account's token is stored.
func (s Store) ClaudeTokenPath(account string) string {
	return filepath.Join(s.ClaudeDir(), account+tokenSuffix)
}

func (s Store) claudeDefaultPath() string { return filepath.Join(s.ClaudeDir(), defaultFile) }
func (s Store) legacyTokenPath() string   { return filepath.Join(s.Dir, legacyTokens) }

// ClaudeAccount is one stored Claude Code login.
type ClaudeAccount struct {
	Name    string
	Default bool
	// SavedAt is the day the token was stored, and SavedAtKnown is false for
	// a token stored before AgentBox recorded it (see claudecheck.go).
	SavedAt      time.Time
	SavedAtKnown bool
	// Valid is what the last check said about the token. Listing accounts
	// never calls Anthropic, so this is whatever was last written down:
	// CheckClaudeAccount is what asks.
	Valid TokenState
}

// ValidateAccount checks a name that becomes a file name in ClaudeDir.
func ValidateAccount(name string) error {
	return naming.Validate("account", name, MaxAccountLen)
}

// migrateClaude moves a token stored by the single-account layout into the
// account named "default", and gives any token without a sidecar one that says
// its date is unknown. It runs before every read and write, so an AgentBox
// that was set up before named accounts, or before tokens had a date, keeps
// working untouched.
func (s Store) migrateClaude() error {
	defer s.markClaudeIssuedUnknown()
	if _, err := os.Stat(s.legacyTokenPath()); err != nil {
		return nil
	}
	if err := os.MkdirAll(s.ClaudeDir(), 0o700); err != nil {
		return err
	}
	target := s.ClaudeTokenPath(DefaultAccount)
	if _, err := os.Stat(target); err == nil {
		// Both layouts have a token: the named one wins, and the old file goes.
		return os.Remove(s.legacyTokenPath())
	}
	if err := os.Rename(s.legacyTokenPath(), target); err != nil {
		return err
	}
	return s.writeDefault(DefaultAccount)
}

// SaveClaudeToken stores a token from `claude setup-token` under an account
// name; an empty name means DefaultAccount. Agents of that account receive the
// token as CLAUDE_CODE_OAUTH_TOKEN. The first account saved becomes the default.
func (s Store) SaveClaudeToken(account, token string) error {
	if account == "" {
		account = DefaultAccount
	}
	if err := ValidateAccount(account); err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token must be a single non-empty line")
	}
	if err := s.migrateClaude(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.ClaudeDir(), 0o700); err != nil {
		return err
	}
	if err := writeTokenFile(s.ClaudeTokenPath(account), token); err != nil {
		return err
	}
	// The date is what says how old a token that later dies was, and this
	// clears whatever the last check said: it's a different token now.
	if err := s.markClaudeIssued(account, time.Now()); err != nil {
		return err
	}
	if name, err := s.DefaultClaudeAccount(); err == nil && name == account {
		// Either it already was the default, or it's the only account there is.
		return s.writeDefault(account)
	} else if err != nil {
		return err
	}
	return nil
}

// ClaudeToken returns an account's token, or "" when there is none. An empty
// account means the default one.
func (s Store) ClaudeToken(account string) (string, error) {
	if err := s.migrateClaude(); err != nil {
		return "", err
	}
	if account == "" {
		name, err := s.DefaultClaudeAccount()
		if err != nil || name == "" {
			return "", err
		}
		account = name
	}
	if err := ValidateAccount(account); err != nil {
		return "", err
	}
	b, err := os.ReadFile(s.ClaudeTokenPath(account))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// ClaudeAccounts lists the stored accounts, in name order.
func (s Store) ClaudeAccounts() ([]ClaudeAccount, error) {
	if err := s.migrateClaude(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.ClaudeDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var accounts []ClaudeAccount
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), tokenSuffix)
		if !ok || e.IsDir() || ValidateAccount(name) != nil {
			continue
		}
		m := s.readClaudeMeta(name)
		a := ClaudeAccount{Name: name, Valid: m.State}
		if m.Issued != nil {
			a.SavedAt, a.SavedAtKnown = *m.Issued, true
		}
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
	def, err := s.DefaultClaudeAccount()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		accounts[i].Default = accounts[i].Name == def
	}
	return accounts, nil
}

// HasClaudeAccount reports whether an account holds a token.
func (s Store) HasClaudeAccount(name string) (bool, error) {
	if name == "" || ValidateAccount(name) != nil {
		return false, nil
	}
	if err := s.migrateClaude(); err != nil {
		return false, err
	}
	_, err := os.Stat(s.ClaudeTokenPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// DefaultClaudeAccount is the account agents use when neither the agent nor its
// project names one. It is the marked account, "default" when nothing is
// marked, or the only account there is; "" when no account is stored.
func (s Store) DefaultClaudeAccount() (string, error) {
	names, err := s.accountNames()
	if err != nil || len(names) == 0 {
		return "", err
	}
	marked, err := os.ReadFile(s.claudeDefaultPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	switch name := strings.TrimSpace(string(marked)); {
	case slicesContains(names, name):
		return name, nil
	case slicesContains(names, DefaultAccount):
		return DefaultAccount, nil
	default:
		return names[0], nil
	}
}

// SetDefaultClaudeAccount marks the account agents use by default.
func (s Store) SetDefaultClaudeAccount(name string) error {
	ok, err := s.HasClaudeAccount(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no Claude Code account named %q: see agentbox auth claude list", name)
	}
	return s.writeDefault(name)
}

// RemoveClaudeAccount deletes an account's token. Agents already created keep
// the token that was written into them.
func (s Store) RemoveClaudeAccount(name string) error {
	ok, err := s.HasClaudeAccount(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no Claude Code account named %q: see agentbox auth claude list", name)
	}
	if err := os.Remove(s.ClaudeTokenPath(name)); err != nil {
		return err
	}
	// Its date and last check go with it, so a later account of the same name
	// doesn't inherit them.
	if err := os.Remove(s.claudeMetaPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// The marker would point at nothing; let DefaultClaudeAccount pick again.
	if marked, err := os.ReadFile(s.claudeDefaultPath()); err == nil && strings.TrimSpace(string(marked)) == name {
		if err := os.Remove(s.claudeDefaultPath()); err != nil {
			return err
		}
	}
	return nil
}

// RenameClaudeAccount gives a stored account another name: its token, its
// date and last check, and the default marker when it is the default, so the
// machine's default stays the same account. A name already taken is refused.
// The token itself doesn't change, so agents that hold it keep working.
func (s Store) RenameClaudeAccount(old, name string) error {
	if err := ValidateAccount(name); err != nil {
		return err
	}
	ok, err := s.HasClaudeAccount(old)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no Claude Code account named %q: see agentbox auth claude list", old)
	}
	if old == name {
		return fmt.Errorf("the Claude Code account is already called %q", name)
	}
	if taken, err := s.HasClaudeAccount(name); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("there is already a Claude Code account named %q: remove it first, or pick another name", name)
	}
	// Read before anything moves: an implicit default ("default", or the only
	// account) would otherwise pass to whichever name sorts first.
	def, err := s.DefaultClaudeAccount()
	if err != nil {
		return err
	}
	// Behind the lock a check takes, so an answer in flight isn't written
	// under the old name after the sidecar has moved.
	lock := checkLock(s.claudeMetaPath(old))
	lock.Lock()
	defer lock.Unlock()
	if err := os.Rename(s.ClaudeTokenPath(old), s.ClaudeTokenPath(name)); err != nil {
		return err
	}
	if err := os.Rename(s.claudeMetaPath(old), s.claudeMetaPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if def == old {
		return s.writeDefault(name)
	}
	return nil
}

func (s Store) writeDefault(name string) error {
	return writeDefaultMarker(s.claudeDefaultPath(), name)
}

func (s Store) accountNames() ([]string, error) {
	return accountNames(s.ClaudeDir())
}

// writeTokenFile stores a token atomically, readable only by this user.
func writeTokenFile(path, token string) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeDefaultMarker records the account name a tool's agents use when
// nothing else names one.
func writeDefaultMarker(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(name+"\n"), 0o600)
}

// accountNames lists the accounts a tool's directory holds, in name order:
// one file per account, named "<account>.token", plus a "default-account" marker.
func accountNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), tokenSuffix); ok && !e.IsDir() && ValidateAccount(name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func slicesContains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func (s Store) HasCodexLogin() bool {
	_, err := os.Stat(s.CodexAuthPath())
	return err == nil
}

// HasOpenCodeLogin reports whether `agentbox auth opencode` has stored a
// provider login for agents. OpenCode keeps every provider's key in one
// auth.json, so this is "at least one provider", not a named account.
func (s Store) HasOpenCodeLogin() bool {
	b, err := os.ReadFile(s.OpenCodeAuthPath())
	if err != nil {
		return false
	}
	// An empty object is what a logout leaves behind: the file exists and says
	// there is no provider, which is not a login.
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(b, &providers); err != nil {
		return false
	}
	return len(providers) > 0
}

// GitHub logins are named accounts too, so a machine can hold several GitHub
// tokens: a project picks which one its agents use, and an agent can be moved
// to another one, mirroring Claude Code accounts. Agents get the chosen
// token as GH_TOKEN and GITHUB_TOKEN, so `gh` and the GitHub API work inside
// them without logging in.

const legacyGitHubToken = "github.token" // single-account layout, before named accounts

// GitHubDir holds one file per GitHub account, plus the default marker.
func (s Store) GitHubDir() string { return filepath.Join(s.Dir, "github") }

// GitHubTokenPath is where an account's token is stored.
func (s Store) GitHubTokenPath(account string) string {
	return filepath.Join(s.GitHubDir(), account+tokenSuffix)
}

func (s Store) githubDefaultPath() string     { return filepath.Join(s.GitHubDir(), defaultFile) }
func (s Store) legacyGitHubTokenPath() string { return filepath.Join(s.Dir, legacyGitHubToken) }

// GitHubAccount is one stored GitHub login.
type GitHubAccount struct {
	Name    string
	Default bool
	SavedAt time.Time
	// Login is the GitHub user its token belongs to, remembered when the
	// token was saved; "" when it was never recorded.
	Login string
}

// migrateGitHub moves a token stored by the single-account layout into the
// account named "default". It runs before every read and write, so an
// AgentBox that was set up before named accounts keeps working untouched.
func (s Store) migrateGitHub() error {
	if _, err := os.Stat(s.legacyGitHubTokenPath()); err != nil {
		return nil
	}
	if err := os.MkdirAll(s.GitHubDir(), 0o700); err != nil {
		return err
	}
	target := s.GitHubTokenPath(DefaultAccount)
	if _, err := os.Stat(target); err == nil {
		// Both layouts have a token: the named one wins, and the old file goes.
		return os.Remove(s.legacyGitHubTokenPath())
	}
	if err := os.Rename(s.legacyGitHubTokenPath(), target); err != nil {
		return err
	}
	return writeDefaultMarker(s.githubDefaultPath(), DefaultAccount)
}

// SaveGitHubToken stores a token under an account name; an empty name means
// DefaultAccount. The first account saved becomes the default.
func (s Store) SaveGitHubToken(account, token string) error {
	if account == "" {
		account = DefaultAccount
	}
	if err := ValidateAccount(account); err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token must be a single non-empty line")
	}
	if err := s.migrateGitHub(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.GitHubDir(), 0o700); err != nil {
		return err
	}
	if err := writeTokenFile(s.GitHubTokenPath(account), token); err != nil {
		return err
	}
	// A new token can belong to another GitHub user, so the login recorded for
	// this account is stale until the caller saves the one it checked.
	if err := os.Remove(s.GitHubLoginPath(account)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if name, err := s.DefaultGitHubAccount(); err == nil && name == account {
		// Either it already was the default, or it's the only account there is.
		return writeDefaultMarker(s.githubDefaultPath(), account)
	} else if err != nil {
		return err
	}
	return nil
}

// GitHubToken returns an account's token, or "" when there is none. An empty
// account means the default one.
func (s Store) GitHubToken(account string) (string, error) {
	if err := s.migrateGitHub(); err != nil {
		return "", err
	}
	if account == "" {
		name, err := s.DefaultGitHubAccount()
		if err != nil || name == "" {
			return "", err
		}
		account = name
	}
	if err := ValidateAccount(account); err != nil {
		return "", err
	}
	b, err := os.ReadFile(s.GitHubTokenPath(account))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// GitHubLoginPath is where the GitHub user an account's token belongs to is
// remembered.
func (s Store) GitHubLoginPath(account string) string {
	return filepath.Join(s.GitHubDir(), account+loginSuffix)
}

// SaveGitHubLogin remembers who an account is on GitHub. The login is checked
// against GitHub when the token is saved anyway, and keeping it means the app
// can say which GitHub user a project reads pull requests as without asking
// GitHub again on every poll.
func (s Store) SaveGitHubLogin(account, login string) error {
	if account == "" {
		account = DefaultAccount
	}
	if err := ValidateAccount(account); err != nil {
		return err
	}
	login = strings.TrimSpace(login)
	if login == "" {
		return nil
	}
	if err := os.MkdirAll(s.GitHubDir(), 0o700); err != nil {
		return err
	}
	return writeTokenFile(s.GitHubLoginPath(account), login)
}

// GitHubLogin is the GitHub user an account's token belongs to, or "" when it
// was never recorded: an account stored before AgentBox kept logins has none
// until its token is saved again, or the Setup page looks it up.
func (s Store) GitHubLogin(account string) (string, error) {
	if account == "" {
		name, err := s.DefaultGitHubAccount()
		if err != nil || name == "" {
			return "", err
		}
		account = name
	}
	if err := ValidateAccount(account); err != nil {
		return "", err
	}
	b, err := os.ReadFile(s.GitHubLoginPath(account))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// GitHubAccounts lists the stored accounts, in name order.
func (s Store) GitHubAccounts() ([]GitHubAccount, error) {
	if err := s.migrateGitHub(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.GitHubDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var accounts []GitHubAccount
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), tokenSuffix)
		if !ok || e.IsDir() || ValidateAccount(name) != nil {
			continue
		}
		a := GitHubAccount{Name: name}
		if info, err := e.Info(); err == nil {
			a.SavedAt = info.ModTime()
		}
		a.Login, _ = s.GitHubLogin(name)
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
	def, err := s.DefaultGitHubAccount()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		accounts[i].Default = accounts[i].Name == def
	}
	return accounts, nil
}

// HasGitHubAccount reports whether an account holds a token.
func (s Store) HasGitHubAccount(name string) (bool, error) {
	if name == "" || ValidateAccount(name) != nil {
		return false, nil
	}
	if err := s.migrateGitHub(); err != nil {
		return false, err
	}
	_, err := os.Stat(s.GitHubTokenPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// HasGitHubLogin reports whether at least one GitHub account is stored.
func (s Store) HasGitHubLogin() bool {
	accounts, err := s.GitHubAccounts()
	return err == nil && len(accounts) > 0
}

// DefaultGitHubAccount is the account agents use when neither the agent nor
// its project names one. It is the marked account, "default" when nothing is
// marked, or the only account there is; "" when no account is stored.
func (s Store) DefaultGitHubAccount() (string, error) {
	names, err := accountNames(s.GitHubDir())
	if err != nil || len(names) == 0 {
		return "", err
	}
	marked, err := os.ReadFile(s.githubDefaultPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	switch name := strings.TrimSpace(string(marked)); {
	case slicesContains(names, name):
		return name, nil
	case slicesContains(names, DefaultAccount):
		return DefaultAccount, nil
	default:
		return names[0], nil
	}
}

// SetDefaultGitHubAccount marks the account agents use by default.
func (s Store) SetDefaultGitHubAccount(name string) error {
	ok, err := s.HasGitHubAccount(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no GitHub account named %q: see agentbox auth github list", name)
	}
	return writeDefaultMarker(s.githubDefaultPath(), name)
}

// RemoveGitHubAccount deletes an account's token. Agents already created keep
// the token that was written into them.
func (s Store) RemoveGitHubAccount(name string) error {
	ok, err := s.HasGitHubAccount(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no GitHub account named %q: see agentbox auth github list", name)
	}
	if err := os.Remove(s.GitHubTokenPath(name)); err != nil {
		return err
	}
	if err := os.Remove(s.GitHubLoginPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// The marker would point at nothing; let DefaultGitHubAccount pick again.
	if marked, err := os.ReadFile(s.githubDefaultPath()); err == nil && strings.TrimSpace(string(marked)) == name {
		if err := os.Remove(s.githubDefaultPath()); err != nil {
			return err
		}
	}
	return nil
}

// RenameGitHubAccount gives a stored GitHub account another name: its token,
// which keeps its saved date, the GitHub user it belongs to, and the default
// marker when it is the default, so the machine's default stays the same
// account. A name already taken is refused. The token itself doesn't change,
// so agents that hold it keep working.
func (s Store) RenameGitHubAccount(old, name string) error {
	if err := ValidateAccount(name); err != nil {
		return err
	}
	ok, err := s.HasGitHubAccount(old)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no GitHub account named %q: see agentbox auth github list", old)
	}
	if old == name {
		return fmt.Errorf("the GitHub account is already called %q", name)
	}
	if taken, err := s.HasGitHubAccount(name); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("there is already a GitHub account named %q: remove it first, or pick another name", name)
	}
	// Read before anything moves: an implicit default ("default", or the only
	// account) would otherwise pass to whichever name sorts first.
	def, err := s.DefaultGitHubAccount()
	if err != nil {
		return err
	}
	if err := os.Rename(s.GitHubTokenPath(old), s.GitHubTokenPath(name)); err != nil {
		return err
	}
	// A login left behind by a removed account of the new name belongs to
	// another token, so it goes whether or not this one has a login to move.
	if err := os.Remove(s.GitHubLoginPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(s.GitHubLoginPath(old), s.GitHubLoginPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if def == old {
		return writeDefaultMarker(s.githubDefaultPath(), name)
	}
	return nil
}
