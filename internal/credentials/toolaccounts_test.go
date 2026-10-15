package credentials_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/credentials"
)

func save(t *testing.T, s credentials.Store, tool, provider, name, auth string) {
	t.Helper()
	if err := s.SaveToolAccount(tool, provider, name, []byte(auth)); err != nil {
		t.Fatalf("SaveToolAccount(%s, %s, %s): %v", tool, provider, name, err)
	}
}

// Codex and Cursor accounts work the way Claude Code's do: the first is the
// default, another can be marked, renamed and removed, and the default passes
// on when it goes.
func TestSeveralCodexAccounts(t *testing.T) {
	s := store(t)
	if s.HasCodexLogin("") {
		t.Fatal("an empty store has a Codex login")
	}
	save(t, s, credentials.ToolCodex, "", "work", `{"OPENAI_API_KEY":"sk-work"}`)
	save(t, s, credentials.ToolCodex, "", "home", `{"OPENAI_API_KEY":"sk-home"}`)
	if def, _ := s.DefaultToolAccount(credentials.ToolCodex, ""); def != "work" {
		t.Fatalf("the default is %q, want the first account saved", def)
	}
	b, name, err := s.ToolLogin(credentials.ToolCodex, "")
	if err != nil || name != "work" || string(b) != `{"OPENAI_API_KEY":"sk-work"}` {
		t.Fatalf("ToolLogin(codex, \"\") = %s, %q, %v", b, name, err)
	}
	if err := s.SetDefaultToolAccount(credentials.ToolCodex, "", "home"); err != nil {
		t.Fatal(err)
	}
	if !s.HasCodexLogin("work") || s.HasCodexLogin("nope") {
		t.Fatal("HasCodexLogin doesn't follow the account named")
	}
	if err := s.RenameToolAccount(credentials.ToolCodex, "", "home", "personal"); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.DefaultToolAccount(credentials.ToolCodex, ""); def != "personal" {
		t.Fatalf("renaming the default made %q the default", def)
	}
	if err := s.RenameToolAccount(credentials.ToolCodex, "", "work", "personal"); err == nil {
		t.Fatal("renaming onto a taken name was allowed")
	}
	if err := s.RemoveToolAccount(credentials.ToolCodex, "", "personal"); err != nil {
		t.Fatal(err)
	}
	accounts, err := s.ToolAccounts(credentials.ToolCodex)
	if err != nil || len(accounts) != 1 || accounts[0].Name != "work" || !accounts[0].Default || accounts[0].Kind != "apikey" {
		t.Fatalf("ToolAccounts = %+v, %v", accounts, err)
	}
	if err := s.RemoveToolAccount(credentials.ToolCodex, "", "personal"); err == nil {
		t.Fatal("removing an account twice succeeded")
	}
	if err := s.SaveToolAccount(credentials.ToolCodex, "", "x", []byte("not json")); err == nil {
		t.Fatal("a file that isn't JSON was stored")
	}
	if err := s.SaveToolAccount(credentials.ToolCodex, "openai", "x", []byte(`{"a":1}`)); err == nil {
		t.Fatal("a Codex account took a provider")
	}
	info, err := os.Stat(s.ToolAccountPath(credentials.ToolCodex, "", "work"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the login is stored with %v (%v), want 0600", info.Mode(), err)
	}
}

// A ChatGPT sign-in says whose it is in its id_token.
func TestCodexAccountSaysWhoseItIs(t *testing.T) {
	s := store(t)
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@example.com"}`))
	auth, _ := json.Marshal(map[string]any{"OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": "h." + payload + ".sig"}})
	save(t, s, credentials.ToolCodex, "", "chatgpt", string(auth))
	accounts, _ := s.ToolAccounts(credentials.ToolCodex)
	if len(accounts) != 1 || accounts[0].Kind != "chatgpt" || accounts[0].Email != "dev@example.com" {
		t.Fatalf("ToolAccounts = %+v", accounts)
	}
}

// Cursor's sign-in is the SDK's own credentials file. A key that expired is no
// sign-in, since the SDK ignores it, and neither is a file without a key.
func TestCursorAccounts(t *testing.T) {
	s := store(t)
	if s.HasCursorLogin("") {
		t.Fatal("a store with nothing in it has a Cursor sign-in")
	}
	save(t, s, credentials.ToolCursor, "", "first", `{"version":1,"apiKey":"key_1","email":"a@b.c"}`)
	login, ok := s.CursorLogin("")
	if !ok || login.APIKey != "key_1" || login.Email != "a@b.c" || !login.Expiry.IsZero() {
		t.Fatalf("CursorLogin = %+v, %v", login, ok)
	}
	save(t, s, credentials.ToolCursor, "", "minted", `{"version":1,"apiKey":"key_2","apiKeyExpiresAtMs":4102444800000}`)
	if login, ok := s.CursorLogin("minted"); !ok || login.Expiry.Year() != 2100 {
		t.Fatalf("a key minted at sign-in: %+v, %v", login, ok)
	}
	save(t, s, credentials.ToolCursor, "", "old", `{"version":1,"apiKey":"key_3","apiKeyExpiresAtMs":1000}`)
	if s.HasCursorLogin("old") {
		t.Fatal("an expired key counts as a sign-in")
	}
	if b, _, _ := s.ToolLogin(credentials.ToolCursor, "old"); b != nil {
		t.Fatal("an expired key is given to agents")
	}
	save(t, s, credentials.ToolCursor, "", "blank", `{"version":1,"apiKey":" "}`)
	if s.HasCursorLogin("blank") {
		t.Fatal("a file without a key counts as a sign-in")
	}
	accounts, _ := s.ToolAccounts(credentials.ToolCursor)
	for _, a := range accounts {
		if want := a.Name == "old" || a.Name == "blank"; a.Expired != want {
			t.Errorf("%s: Expired = %v", a.Name, a.Expired)
		}
	}
}

// OpenCode's accounts are per provider, and an agent's auth.json is put
// together from one account per provider: the one chosen, else that
// provider's default.
func TestOpenCodeAccountsPerProvider(t *testing.T) {
	s := store(t)
	if s.HasOpenCodeLogin(nil) {
		t.Fatal("an empty store has an OpenCode login")
	}
	if err := s.SaveToolAccount(credentials.ToolOpenCode, "", "x", []byte(`{"type":"api","key":"k"}`)); err == nil {
		t.Fatal("an OpenCode account was stored without a provider")
	}
	save(t, s, credentials.ToolOpenCode, "anthropic", "work", `{"type":"api","key":"a-work"}`)
	save(t, s, credentials.ToolOpenCode, "anthropic", "home", `{"type":"api","key":"a-home"}`)
	save(t, s, credentials.ToolOpenCode, "openai", "default", `{"type":"oauth","refresh":"r"}`)

	providers, _ := s.OpenCodeProviders()
	if len(providers) != 2 || providers[0] != "anthropic" || providers[1] != "openai" {
		t.Fatalf("OpenCodeProviders = %v", providers)
	}
	auth, used, err := s.OpenCodeAuth(map[string]string{"anthropic": "home"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(auth, &got); err != nil {
		t.Fatal(err)
	}
	if got["anthropic"]["key"] != "a-home" || got["openai"]["type"] != "oauth" || used["anthropic"] != "home" || used["openai"] != "default" {
		t.Fatalf("OpenCodeAuth = %s, %v", auth, used)
	}
	if _, _, err := s.OpenCodeAuth(map[string]string{"anthropic": "gone"}); err == nil {
		t.Fatal("an account that doesn't exist was quietly replaced")
	}
	if _, _, err := s.OpenCodeAuth(map[string]string{"google": "x"}); err == nil {
		t.Fatal("a provider with no accounts was accepted")
	}
	accounts, _ := s.ToolAccounts(credentials.ToolOpenCode)
	if len(accounts) != 3 || accounts[0].Provider != "anthropic" || accounts[2].Kind != "oauth" || accounts[0].Kind != "apikey" {
		t.Fatalf("ToolAccounts = %+v", accounts)
	}
	// The last account of a provider takes the provider with it.
	if err := s.RemoveToolAccount(credentials.ToolOpenCode, "openai", "default"); err != nil {
		t.Fatal(err)
	}
	if providers, _ := s.OpenCodeProviders(); len(providers) != 1 {
		t.Fatalf("OpenCodeProviders after removing openai's only account = %v", providers)
	}
}

// A login stored before named accounts becomes the account called "default":
// Codex's and Cursor's whole, OpenCode's one per provider.
func TestMigratesSingleToolLogins(t *testing.T) {
	s := store(t)
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(s.Dir, "codex", "auth.json"), `{"OPENAI_API_KEY":"sk"}`)
	write(filepath.Join(s.Dir, "cursor", "auth.json"), `{"apiKey":"key"}`)
	write(filepath.Join(s.Dir, "opencode-data", "opencode", "auth.json"), `{"anthropic":{"type":"api","key":"a"},"openai":{"type":"api","key":"o"}}`)

	if b, name, _ := s.ToolLogin(credentials.ToolCodex, ""); name != "default" || string(b) != `{"OPENAI_API_KEY":"sk"}` {
		t.Fatalf("Codex after migrating: %s, %q", b, name)
	}
	if !s.HasCursorLogin("default") {
		t.Fatal("Cursor's sign-in didn't become the default account")
	}
	_, used, err := s.OpenCodeAuth(nil)
	if err != nil || used["anthropic"] != "default" || used["openai"] != "default" {
		t.Fatalf("OpenCode after migrating: %v, %v", used, err)
	}
	for _, old := range []string{"codex/auth.json", "cursor/auth.json", "opencode-data/opencode/auth.json"} {
		if _, err := os.Stat(filepath.Join(s.Dir, old)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", old)
		}
	}
}

func TestAPIKeyLogins(t *testing.T) {
	if _, err := credentials.CodexAPIKeyAuth("two words"); err == nil {
		t.Fatal("a key with a space was taken")
	}
	b, err := credentials.CodexAPIKeyAuth(" sk-1 ")
	if err != nil || string(b) != `{"OPENAI_API_KEY":"sk-1","auth_mode":"apikey"}` {
		t.Fatalf("CodexAPIKeyAuth = %s, %v", b, err)
	}
	b, err = credentials.OpenCodeAPIKeyAuth("k")
	if err != nil || string(b) != `{"key":"k","type":"api"}` {
		t.Fatalf("OpenCodeAPIKeyAuth = %s, %v", b, err)
	}
	split, err := credentials.SplitOpenCodeAuth([]byte(`{"anthropic":{"type":"api"},"Bad Name":{}}`))
	if err != nil || len(split) != 1 || split["anthropic"] == nil {
		t.Fatalf("SplitOpenCodeAuth = %v, %v", split, err)
	}
}
