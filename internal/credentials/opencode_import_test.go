package credentials_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/credentials"
)

// OpenCode 2 imports credentials as one JSON array, per provider, in the shape
// `opencode auth export` prints. The stored entries are the same ones OpenCode
// 1's auth.json holds, so one account serves both versions.
func TestOpenCodeImport(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, "anthropic", "work", `{"type":"key","key":"a-work"}`)
	save(t, s, credentials.ToolOpenCode, "openai", "default", `{"type":"oauth","refresh":"r","methodID":"chatgpt-browser","metadata":{"accountID":"acc_1"}}`)

	out, used, err := s.OpenCodeImport(map[string]string{"anthropic": "work"})
	if err != nil {
		t.Fatal(err)
	}
	if used["anthropic"] != "work" || used["openai"] != "default" {
		t.Fatalf("used = %v", used)
	}
	var creds []struct {
		ID            string         `json:"id"`
		IntegrationID string         `json:"integrationID"`
		Label         string         `json:"label"`
		Active        bool           `json:"active"`
		Value         map[string]any `json:"value"`
	}
	if err := json.Unmarshal(out, &creds); err != nil {
		t.Fatalf("OpenCodeImport = %s: %v", out, err)
	}
	if len(creds) != 2 {
		t.Fatalf("OpenCodeImport has %d credentials, want 2: %s", len(creds), out)
	}
	if creds[0].IntegrationID != "anthropic" || creds[0].Label != "work" || !creds[0].Active || creds[0].Value["key"] != "a-work" {
		t.Fatalf("the anthropic credential reads %+v", creds[0])
	}
	if creds[0].ID == "" || creds[1].ID == creds[0].ID {
		t.Fatalf("credential ids aren't distinct and stable: %q, %q", creds[0].ID, creds[1].ID)
	}

	// The same import again names the same credentials, so importing twice
	// replaces them instead of adding copies.
	again, _, err := s.OpenCodeImport(map[string]string{"anthropic": "work"})
	if err != nil {
		t.Fatal(err)
	}
	var twice []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(again, &twice); err != nil {
		t.Fatal(err)
	}
	if twice[0].ID != creds[0].ID {
		t.Fatalf("the same account was imported as %q then %q", creds[0].ID, twice[0].ID)
	}
}

// TestOpenCodeAuthDeliversTheGoProvider: an "opencode" account is also written
// as opencode-go, with the same key, so the Go endpoint's models are available
// to an agent whose auth.json would otherwise only name "opencode".
func TestOpenCodeAuthDeliversTheGoProvider(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, "opencode", "default", `{"type":"api","key":"sk-go"}`)
	auth, used, err := s.OpenCodeAuth(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(auth, &got); err != nil {
		t.Fatal(err)
	}
	if got["opencode"]["key"] != "sk-go" || got["opencode-go"]["key"] != "sk-go" {
		t.Fatalf("OpenCodeAuth = %s, want the key under both opencode and opencode-go", auth)
	}
	if used["opencode-go"] != "default" {
		t.Fatalf("used = %v, want opencode-go on the opencode account", used)
	}
	export, imported, err := s.OpenCodeImport(nil)
	if err != nil {
		t.Fatal(err)
	}
	var creds []struct {
		IntegrationID string `json:"integrationID"`
		Label         string `json:"label"`
	}
	if err := json.Unmarshal(export, &creds); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range creds {
		if c.IntegrationID == credentials.OpenCodeGoProvider {
			found = c.Label == "default"
		}
	}
	if !found || imported["opencode-go"] != "default" {
		t.Fatalf("OpenCodeImport = %s, %v, want an opencode-go credential", export, imported)
	}
}

// TestOpenCodeResolveLegacyProvider: a row written before the Go switch says
// opencode=h4ad, while the store now holds that account under opencode-go.
// The provider is moved; an account that is gone is dropped so the default
// resolves.
func TestOpenCodeResolveLegacyProvider(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, credentials.OpenCodeGoProvider, "h4ad", `{"type":"api","key":"sk"}`)
	got, changed, err := s.ResolveOpenCodeAccounts(map[string]string{"opencode": "h4ad"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || got["opencode-go"] != "h4ad" || len(got) != 1 {
		t.Fatalf("ResolveOpenCodeAccounts = %v, %v; want opencode-go=h4ad", got, changed)
	}
	got, changed, err = s.ResolveOpenCodeAccounts(map[string]string{"opencode": "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || len(got) != 0 {
		t.Fatalf("a legacy provider whose account is gone = %v, %v; want it dropped", got, changed)
	}
	auth, used, err := s.OpenCodeAuth(map[string]string{"opencode": "h4ad"})
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]map[string]string
	if err := json.Unmarshal(auth, &entries); err != nil {
		t.Fatal(err)
	}
	if entries[credentials.OpenCodeGoProvider]["key"] != "sk" || used[credentials.OpenCodeGoProvider] != "h4ad" {
		t.Fatalf("OpenCodeAuth from a legacy row = %s, %v", auth, used)
	}
}

// An account captured under OpenCode 2 carries its method and metadata; an
// OpenCode 1 auth.json takes neither, and takes the account id from metadata.
func TestOpenCodeAuthProjectsVersionOneEntries(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, "openai", "default",
		`{"type":"oauth","refresh":"r","expires":1,"methodID":"chatgpt-browser","metadata":{"accountID":"acc_1","email":"a@example.com"}}`)
	auth, _, err := s.OpenCodeAuth(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(auth, &got); err != nil {
		t.Fatal(err)
	}
	entry := got["openai"]
	if _, ok := entry["methodID"]; ok {
		t.Fatalf("auth.json kept OpenCode 2's methodID: %s", auth)
	}
	if _, ok := entry["metadata"]; ok {
		t.Fatalf("auth.json kept OpenCode 2's metadata: %s", auth)
	}
	if entry["accountId"] != "acc_1" {
		t.Fatalf("auth.json lost the account id: %s", auth)
	}
}

func TestSplitOpenCodeExport(t *testing.T) {
	export := `[
		{"id":"cred_1","integrationID":"anthropic","label":"old","active":false,"value":{"type":"key","key":"old"}},
		{"id":"cred_2","integrationID":"anthropic","label":"new","active":true,"value":{"type":"key","key":"new"}},
		{"id":"cred_3","integrationID":"openai","label":"work","active":true,"value":{"type":"oauth","refresh":"r"}},
		{"id":"cred_4","integrationID":"not/a/provider","label":"x","active":true,"value":{"type":"key","key":"x"}}
	]`
	got, err := credentials.SplitOpenCodeExport([]byte(export))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("SplitOpenCodeExport = %v", got)
	}
	var anthropic map[string]string
	if err := json.Unmarshal(got["anthropic"], &anthropic); err != nil {
		t.Fatal(err)
	}
	if anthropic["key"] != "new" {
		t.Fatalf("the active credential wasn't the one kept: %s", got["anthropic"])
	}
	if _, ok := got["not/a/provider"]; ok {
		t.Fatal("an invalid provider was stored")
	}
}

// The model menu is asked with a data home of AgentBox's own, imported into
// fresh every time: a home that already held a provider's credential would
// keep the old one.
func TestOpenCodeImportHome(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, "anthropic", "work", `{"type":"key","key":"a-work"}`)
	dir, export, err := s.OpenCodeImportHome()
	if err != nil {
		t.Fatal(err)
	}
	if len(export) == 0 {
		t.Fatal("OpenCodeImportHome returned no credentials to import")
	}
	var creds []struct {
		IntegrationID string `json:"integrationID"`
	}
	if err := json.Unmarshal(export, &creds); err != nil || len(creds) != 1 || creds[0].IntegrationID != "anthropic" {
		t.Fatalf("export = %s, %v", export, err)
	}
	stale := filepath.Join(dir, "opencode", "stale")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.OpenCodeImportHome()
	if err != nil {
		t.Fatal(err)
	}
	if again != dir {
		t.Fatalf("the data home moved: %s then %s", dir, again)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the data home kept a file from the last ask: %v", err)
	}
}

// OpenCode 2 calls an API key's type "key" and requires the method an OAuth
// credential was created with, neither of which OpenCode 1 stored under those
// names. Both are filled in on the way in, and the account id moves into the
// metadata OpenCode 2 reads it from.
func TestOpenCodeImportSpeaksVersionTwo(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, "anthropic", "default", `{"type":"api","key":"a-key"}`)
	save(t, s, credentials.ToolOpenCode, "openai", "default",
		`{"type":"oauth","access":"a","refresh":"r","expires":1,"accountId":"acc_1"}`)

	out, _, err := s.OpenCodeImport(nil)
	if err != nil {
		t.Fatal(err)
	}
	var creds []struct {
		IntegrationID string `json:"integrationID"`
		Value         struct {
			Type     string            `json:"type"`
			MethodID string            `json:"methodID"`
			Metadata map[string]string `json:"metadata"`
		} `json:"value"`
	}
	if err := json.Unmarshal(out, &creds); err != nil {
		t.Fatal(err)
	}
	byProvider := map[string]int{}
	for i, c := range creds {
		byProvider[c.IntegrationID] = i
	}
	key := creds[byProvider["anthropic"]].Value
	if key.Type != "key" || key.MethodID != "" {
		t.Fatalf("an API key became %+v", key)
	}
	oauth := creds[byProvider["openai"]].Value
	if oauth.Type != "oauth" || oauth.MethodID != "chatgpt-browser" || oauth.Metadata["accountID"] != "acc_1" {
		t.Fatalf("an OpenAI sign-in became %+v", oauth)
	}
}

// And an account OpenCode 2 captured reads back in the shape OpenCode 1 takes:
// "key" is what OpenCode 1 calls "api".
func TestOpenCodeAuthProjectsVersionTwoKeys(t *testing.T) {
	s := store(t)
	save(t, s, credentials.ToolOpenCode, "anthropic", "default", `{"type":"key","key":"a-key"}`)
	auth, _, err := s.OpenCodeAuth(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(auth, &got); err != nil {
		t.Fatal(err)
	}
	if got["anthropic"]["type"] != "api" || got["anthropic"]["key"] != "a-key" {
		t.Fatalf("auth.json = %s", auth)
	}
}
