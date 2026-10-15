package credentials

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Codex, OpenCode and Cursor logins are named accounts too, the way Claude
// Code's and GitHub's are: a project picks one per tool, an agent gets a copy
// of the one its project names, and `agentbox auth <tool> --account <name>`
// adds one. Each account is the file the tool itself reads, kept whole:
//
//   - Codex: its auth.json (CODEX_HOME), a ChatGPT sign-in or an API key.
//   - Cursor: the SDK's auth.json (see CursorLogin), a key typed in or one
//     Cursor minted at a browser sign-in.
//   - OpenCode: one provider's entry from its auth.json. OpenCode keeps every
//     provider's key in one file, so its accounts are per provider ("work" for
//     anthropic, "personal" for openai), a project picks one per provider, and
//     an agent's auth.json is put together from them (OpenCodeAuth).
//
// They live under accounts/<tool>/ (OpenCode's under accounts/opencode/<provider>/),
// one <name>.json per account beside a "default-account" marker, as the
// Claude and GitHub directories hold <name>.token files.

// The tools whose logins are ToolAccounts.
const (
	ToolCodex    = "codex"
	ToolOpenCode = "opencode"
	ToolCursor   = "cursor"
)

// AccountTools are the tools with ToolAccounts, in the order they're listed.
var AccountTools = []string{ToolCodex, ToolOpenCode, ToolCursor}

// ToolName is how a tool is called in messages.
func ToolName(tool string) string {
	switch tool {
	case ToolCodex:
		return "Codex"
	case ToolOpenCode:
		return "OpenCode"
	case ToolCursor:
		return "Cursor"
	}
	return tool
}

const authSuffix = ".json"

// ToolAccount is one stored Codex, OpenCode or Cursor login.
type ToolAccount struct {
	Tool string
	// Provider is the OpenCode provider the account signs in to
	// ("anthropic", "openai"); "" for Codex and Cursor.
	Provider string
	Name     string
	// Default is the account of its tool (and provider) agents get when
	// neither the agent nor its project names one.
	Default bool
	SavedAt time.Time
	// Kind is how it signs in: "apikey", "chatgpt" (Codex), "oauth" or
	// "wellknown" (OpenCode's own words), "" when the file doesn't say.
	Kind string
	// Email is whose account it is, when the file says.
	Email string
	// Expiry is when a key Cursor minted stops working (zero for one that
	// doesn't), and Expired says it has: the SDK then ignores it, so it isn't
	// a login agents could use.
	Expiry  time.Time
	Expired bool
}

// ValidateProvider checks an OpenCode provider id, which becomes a directory
// name: OpenCode's own ids are lowercase words with hyphens ("github-copilot").
func ValidateProvider(provider string) error {
	if !providerRE.MatchString(provider) {
		return fmt.Errorf("invalid OpenCode provider %q: use lowercase letters, digits, dots and hyphens", provider)
	}
	return nil
}

var providerRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func checkTool(tool, provider string) error {
	switch tool {
	case ToolCodex, ToolCursor:
		if provider != "" {
			return fmt.Errorf("%s accounts have no provider: only OpenCode's do", ToolName(tool))
		}
		return nil
	case ToolOpenCode:
		if provider == "" {
			return errors.New("an OpenCode account is for one provider: name it (anthropic, openai…)")
		}
		return ValidateProvider(provider)
	}
	return fmt.Errorf("%q has no accounts: use codex, opencode or cursor", tool)
}

// toolDir holds a tool's accounts, one provider's for OpenCode.
func (s Store) toolDir(tool, provider string) string {
	d := filepath.Join(s.Dir, "accounts", tool)
	if tool == ToolOpenCode {
		d = filepath.Join(d, provider)
	}
	return d
}

// ToolAccountPath is where an account's login is stored.
func (s Store) ToolAccountPath(tool, provider, name string) string {
	return filepath.Join(s.toolDir(tool, provider), name+authSuffix)
}

func (s Store) toolDefaultPath(tool, provider string) string {
	return filepath.Join(s.toolDir(tool, provider), defaultFile)
}

// The single-login layout, before named accounts: Codex's CODEX_HOME, Cursor's
// directory, and the XDG data home OpenCode was run with.
func (s Store) legacyCodexAuth() string  { return filepath.Join(s.Dir, "codex", "auth.json") }
func (s Store) legacyCursorAuth() string { return filepath.Join(s.Dir, "cursor", "auth.json") }
func (s Store) legacyOpenCodeAuth() string {
	return filepath.Join(s.Dir, "opencode-data", "opencode", "auth.json")
}

// migrateTools moves a login stored by the single-login layout into the
// account named "default", as migrateClaude does: OpenCode's into one
// "default" account per provider. It runs before every read and write, so an
// AgentBox that was set up before named accounts keeps working untouched.
func (s Store) migrateTools() error {
	for tool, legacy := range map[string]string{ToolCodex: s.legacyCodexAuth(), ToolCursor: s.legacyCursorAuth()} {
		b, err := os.ReadFile(legacy)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.adoptLegacy(tool, "", b); err != nil {
			return err
		}
		if err := os.Remove(legacy); err != nil {
			return err
		}
	}
	b, err := os.ReadFile(s.legacyOpenCodeAuth())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var providers map[string]json.RawMessage
	if json.Unmarshal(b, &providers) == nil {
		for provider, entry := range providers {
			if ValidateProvider(provider) != nil {
				continue
			}
			if err := s.adoptLegacy(ToolOpenCode, provider, entry); err != nil {
				return err
			}
		}
	}
	return os.Remove(s.legacyOpenCodeAuth())
}

// adoptLegacy stores an old login as the "default" account, unless one by
// that name is already there: the named one wins, and the old file goes.
func (s Store) adoptLegacy(tool, provider string, auth []byte) error {
	target := s.ToolAccountPath(tool, provider, DefaultAccount)
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(target, auth); err != nil {
		return err
	}
	return writeDefaultMarker(s.toolDefaultPath(tool, provider), DefaultAccount)
}

// SaveToolAccount stores a login under an account name; an empty name means
// DefaultAccount. The first account of a tool (or OpenCode provider) becomes
// its default. auth is the tool's own file, or OpenCode's entry for provider,
// and must be a JSON object.
func (s Store) SaveToolAccount(tool, provider, name string, auth []byte) error {
	if name == "" {
		name = DefaultAccount
	}
	if err := checkTool(tool, provider); err != nil {
		return err
	}
	if err := ValidateAccount(name); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(auth, &obj); err != nil || len(obj) == 0 {
		return fmt.Errorf("the %s login isn't a JSON object with something in it", ToolName(tool))
	}
	if err := s.migrateTools(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.toolDir(tool, provider), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(s.ToolAccountPath(tool, provider, name), auth); err != nil {
		return err
	}
	if def, err := s.DefaultToolAccount(tool, provider); err != nil {
		return err
	} else if def == name {
		// Either it already was the default, or it's the only account there is.
		return writeDefaultMarker(s.toolDefaultPath(tool, provider), name)
	}
	return nil
}

// ToolAuth returns an account's login and the account it is, the default one
// when name is empty; nil and "" when there is no such account.
func (s Store) ToolAuth(tool, provider, name string) ([]byte, string, error) {
	if err := checkTool(tool, provider); err != nil {
		return nil, "", err
	}
	if err := s.migrateTools(); err != nil {
		return nil, "", err
	}
	if name == "" {
		def, err := s.DefaultToolAccount(tool, provider)
		if err != nil || def == "" {
			return nil, "", err
		}
		name = def
	}
	if err := ValidateAccount(name); err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(s.ToolAccountPath(tool, provider, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return b, name, nil
}

// HasToolAccount reports whether an account holds a login.
func (s Store) HasToolAccount(tool, provider, name string) (bool, error) {
	if name == "" || ValidateAccount(name) != nil || checkTool(tool, provider) != nil {
		return false, nil
	}
	if err := s.migrateTools(); err != nil {
		return false, err
	}
	_, err := os.Stat(s.ToolAccountPath(tool, provider, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// DefaultToolAccount is the account of a tool (or OpenCode provider) agents
// use when neither the agent nor its project names one: the marked account,
// "default" when nothing is marked, or the first there is; "" when none is
// stored.
func (s Store) DefaultToolAccount(tool, provider string) (string, error) {
	if err := checkTool(tool, provider); err != nil {
		return "", err
	}
	return pickDefault(s.toolDir(tool, provider), authSuffix)
}

// SetDefaultToolAccount marks the account agents use by default.
func (s Store) SetDefaultToolAccount(tool, provider, name string) error {
	ok, err := s.HasToolAccount(tool, provider, name)
	if err != nil {
		return err
	}
	if !ok {
		return s.noAccount(tool, provider, name)
	}
	return writeDefaultMarker(s.toolDefaultPath(tool, provider), name)
}

// RemoveToolAccount deletes an account's login. Agents already created keep
// the copy that was written into them.
func (s Store) RemoveToolAccount(tool, provider, name string) error {
	ok, err := s.HasToolAccount(tool, provider, name)
	if err != nil {
		return err
	}
	if !ok {
		return s.noAccount(tool, provider, name)
	}
	if err := os.Remove(s.ToolAccountPath(tool, provider, name)); err != nil {
		return err
	}
	// The marker would point at nothing; let DefaultToolAccount pick again.
	marker := s.toolDefaultPath(tool, provider)
	if marked, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(marked)) == name {
		if err := os.Remove(marker); err != nil {
			return err
		}
	}
	if tool == ToolOpenCode {
		// A provider with no account left isn't one: its directory goes, marker and all.
		if names, err := accountNamesWith(s.toolDir(tool, provider), authSuffix); err == nil && len(names) == 0 {
			_ = os.RemoveAll(s.toolDir(tool, provider))
		}
	}
	return nil
}

// RenameToolAccount gives a stored account another name, keeping it the
// default when it was. A name already taken is refused. The login itself
// doesn't change, so agents that hold a copy keep working.
func (s Store) RenameToolAccount(tool, provider, old, name string) error {
	if err := ValidateAccount(name); err != nil {
		return err
	}
	ok, err := s.HasToolAccount(tool, provider, old)
	if err != nil {
		return err
	}
	if !ok {
		return s.noAccount(tool, provider, old)
	}
	if old == name {
		return fmt.Errorf("the %s is already called %q", s.accountWord(tool, provider), name)
	}
	if taken, err := s.HasToolAccount(tool, provider, name); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("there is already a %s named %q: remove it first, or pick another name", s.accountWord(tool, provider), name)
	}
	// Read before anything moves: an implicit default would otherwise pass to
	// whichever name sorts first.
	def, err := s.DefaultToolAccount(tool, provider)
	if err != nil {
		return err
	}
	if err := os.Rename(s.ToolAccountPath(tool, provider, old), s.ToolAccountPath(tool, provider, name)); err != nil {
		return err
	}
	if def == old {
		return writeDefaultMarker(s.toolDefaultPath(tool, provider), name)
	}
	return nil
}

func (s Store) accountWord(tool, provider string) string {
	if provider != "" {
		return fmt.Sprintf("OpenCode account for %s", provider)
	}
	return ToolName(tool) + " account"
}

func (s Store) noAccount(tool, provider, name string) error {
	return fmt.Errorf("no %s named %q: see agentbox auth %s list", s.accountWord(tool, provider), name, tool)
}

// ToolAccounts lists a tool's stored accounts, by name (OpenCode's by
// provider, then name), with what their files say about them.
func (s Store) ToolAccounts(tool string) ([]ToolAccount, error) {
	if err := s.migrateTools(); err != nil {
		return nil, err
	}
	providers := []string{""}
	if tool == ToolOpenCode {
		var err error
		if providers, err = s.OpenCodeProviders(); err != nil {
			return nil, err
		}
	} else if err := checkTool(tool, ""); err != nil {
		return nil, err
	}
	var out []ToolAccount
	for _, provider := range providers {
		dir := s.toolDir(tool, provider)
		names, err := accountNamesWith(dir, authSuffix)
		if err != nil {
			return nil, err
		}
		def, err := pickDefault(dir, authSuffix)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			a := ToolAccount{Tool: tool, Provider: provider, Name: name, Default: name == def}
			path := s.ToolAccountPath(tool, provider, name)
			if info, err := os.Stat(path); err == nil {
				a.SavedAt = info.ModTime()
			}
			if b, err := os.ReadFile(path); err == nil {
				describe(&a, b)
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// describe reads what an account's file says about it, without checking
// anything with the tool's service.
func describe(a *ToolAccount, b []byte) {
	switch a.Tool {
	case ToolCodex:
		var f struct {
			APIKey *string `json:"OPENAI_API_KEY"`
			Tokens *struct {
				IDToken string `json:"id_token"`
			} `json:"tokens"`
		}
		if json.Unmarshal(b, &f) != nil {
			return
		}
		if f.Tokens != nil && f.Tokens.IDToken != "" {
			a.Kind, a.Email = "chatgpt", jwtEmail(f.Tokens.IDToken)
		} else if f.APIKey != nil && *f.APIKey != "" {
			a.Kind = "apikey"
		}
	case ToolOpenCode:
		var f struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(b, &f) == nil {
			a.Kind = map[string]string{"api": "apikey"}[f.Type]
			if a.Kind == "" {
				a.Kind = f.Type
			}
		}
	case ToolCursor:
		login, ok := parseCursor(b)
		a.Kind, a.Email, a.Expiry = "apikey", login.Email, login.Expiry
		a.Expired = !ok
	}
}

// jwtEmail is the email claim of a JWT's payload, unverified: it only says
// whose sign-in this is.
func jwtEmail(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(payload, &claims)
	return claims.Email
}

// OpenCodeProviders lists the providers OpenCode has accounts for, by name.
func (s Store) OpenCodeProviders() ([]string, error) {
	if err := s.migrateTools(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.Dir, "accounts", ToolOpenCode))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || ValidateProvider(e.Name()) != nil {
			continue
		}
		if names, err := accountNamesWith(filepath.Join(s.Dir, "accounts", ToolOpenCode, e.Name()), authSuffix); err == nil && len(names) > 0 {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// OpenCodeAuth puts together the auth.json an OpenCode agent gets: for every
// provider with an account, the one chosen names (provider → account), else
// that provider's default. used is the account each provider got. A chosen
// account that doesn't exist is an error rather than a quiet fallback, as an
// unknown Claude Code account is. auth is nil when there is no provider at all.
func (s Store) OpenCodeAuth(chosen map[string]string) (auth []byte, used map[string]string, err error) {
	providers, err := s.OpenCodeProviders()
	if err != nil {
		return nil, nil, err
	}
	for provider, name := range chosen {
		if name != "" && !slices.Contains(providers, provider) {
			return nil, nil, s.noAccount(ToolOpenCode, provider, name)
		}
	}
	merged := map[string]json.RawMessage{}
	used = map[string]string{}
	for _, provider := range providers {
		b, name, err := s.ToolAuth(ToolOpenCode, provider, chosen[provider])
		if err != nil {
			return nil, nil, err
		}
		if b == nil {
			return nil, nil, s.noAccount(ToolOpenCode, provider, chosen[provider])
		}
		merged[provider], used[provider] = json.RawMessage(b), name
	}
	if len(merged) == 0 {
		return nil, used, nil
	}
	auth, err = json.Marshal(merged)
	return auth, used, err
}

// ToolLogin is the file an agent on tool gets, and the account it is, for the
// account named ("" for the default): Codex's and Cursor's auth.json, nil
// when there is none or Cursor's key has expired. OpenCode's is OpenCodeAuth.
func (s Store) ToolLogin(tool, name string) ([]byte, string, error) {
	b, resolved, err := s.ToolAuth(tool, "", name)
	if err != nil || b == nil {
		return nil, "", err
	}
	if tool == ToolCursor {
		if _, ok := parseCursor(b); !ok {
			return nil, "", nil
		}
	}
	return b, resolved, nil
}

// HasCodexLogin reports whether a Codex account, the default for "", is stored.
func (s Store) HasCodexLogin(account string) bool {
	b, _, err := s.ToolLogin(ToolCodex, account)
	return err == nil && b != nil
}

// HasOpenCodeLogin reports whether OpenCode has a provider to sign in to with
// the accounts chosen (provider → account; the defaults for the rest).
func (s Store) HasOpenCodeLogin(chosen map[string]string) bool {
	b, _, err := s.OpenCodeAuth(chosen)
	return err == nil && b != nil
}

// CursorLogin is what AgentBox knows of a Cursor sign-in.
type CursorLogin struct {
	APIKey string
	Email  string    // when Cursor said whose key it is
	Expiry time.Time // zero for a key that doesn't expire (one typed in)
}

// CursorLogin reads a stored Cursor account, the default for "". ok is false
// when there is none, or the key it holds has expired: the SDK ignores an
// expired key, so it isn't a sign-in agents could use.
func (s Store) CursorLogin(account string) (login CursorLogin, ok bool) {
	b, _, err := s.ToolAuth(ToolCursor, "", account)
	if err != nil || b == nil {
		return CursorLogin{}, false
	}
	login, ok = parseCursor(b)
	if !ok {
		return CursorLogin{}, false
	}
	return login, true
}

// parseCursor reads the SDK's credentials file (StoredSdkCredentials in
// @cursor/sdk). The login comes back even when ok is false for an expired key.
func parseCursor(b []byte) (CursorLogin, bool) {
	var stored struct {
		APIKey            string `json:"apiKey"`
		Email             string `json:"email"`
		APIKeyExpiresAtMs int64  `json:"apiKeyExpiresAtMs"`
	}
	if json.Unmarshal(b, &stored) != nil || strings.TrimSpace(stored.APIKey) == "" {
		return CursorLogin{}, false
	}
	login := CursorLogin{APIKey: stored.APIKey, Email: stored.Email}
	if stored.APIKeyExpiresAtMs > 0 {
		login.Expiry = time.UnixMilli(stored.APIKeyExpiresAtMs)
		if !login.Expiry.After(time.Now()) {
			return login, false
		}
	}
	return login, true
}

// HasCursorLogin reports whether a Cursor account, the default for "", is a
// sign-in agents can use.
func (s Store) HasCursorLogin(account string) bool {
	_, ok := s.CursorLogin(account)
	return ok
}

// CodexAPIKeyAuth is the auth.json `codex login --with-api-key` writes, for
// an account the app signs in with a key.
func CodexAPIKeyAuth(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return nil, errors.New("the API key must be a single non-empty line")
	}
	return json.Marshal(map[string]any{"auth_mode": "apikey", "OPENAI_API_KEY": key})
}

// OpenCodeAPIKeyAuth is the entry `opencode auth login` writes for a provider
// signed in with an API key.
func OpenCodeAPIKeyAuth(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return nil, errors.New("the API key must be a single non-empty line")
	}
	return json.Marshal(map[string]string{"type": "api", "key": key})
}

// SplitOpenCodeAuth reads an OpenCode auth.json into its providers' entries,
// as `agentbox auth opencode` stores what OpenCode's own login wrote.
func SplitOpenCodeAuth(b []byte) (map[string][]byte, error) {
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(b, &providers); err != nil {
		return nil, fmt.Errorf("reading OpenCode's auth.json: %w", err)
	}
	out := map[string][]byte{}
	for _, p := range slices.Sorted(maps.Keys(providers)) {
		if ValidateProvider(p) == nil {
			out[p] = providers[p]
		}
	}
	return out, nil
}

// OpenCodeModelsHome is an XDG data home holding the default OpenCode
// accounts' auth.json, every provider's, for asking OpenCode which models
// they can run (`opencode models`). It is written afresh on every call.
func (s Store) OpenCodeModelsHome() (string, error) {
	dir := filepath.Join(s.Dir, "opencode-models")
	auth, _, err := s.OpenCodeAuth(nil)
	if err != nil {
		return "", err
	}
	if auth == nil {
		auth = []byte("{}")
	}
	if err := os.MkdirAll(filepath.Join(dir, "opencode"), 0o700); err != nil {
		return "", err
	}
	return dir, writeFileAtomic(filepath.Join(dir, "opencode", "auth.json"), auth)
}

// pickDefault is the default account of a directory of accounts: the marked
// one, "default" when nothing is marked, or the first there is.
func pickDefault(dir, suffix string) (string, error) {
	names, err := accountNamesWith(dir, suffix)
	if err != nil || len(names) == 0 {
		return "", err
	}
	marked, err := os.ReadFile(filepath.Join(dir, defaultFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	switch name := strings.TrimSpace(string(marked)); {
	case slices.Contains(names, name):
		return name, nil
	case slices.Contains(names, DefaultAccount):
		return DefaultAccount, nil
	default:
		return names[0], nil
	}
}

// writeFileAtomic stores a login whole, readable only by this user.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
