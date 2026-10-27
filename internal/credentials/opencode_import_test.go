package credentials_test

import (
	"encoding/json"
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
