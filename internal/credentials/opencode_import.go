package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// openCodeCredential is one entry of the JSON `opencode auth export` prints,
// which is also what `opencode auth import` reads. The value is the provider
// entry OpenCode stores: the same JSON an OpenCode 1 auth.json holds, plus
// "methodID" and "metadata" (the account's email, org and so on).
type openCodeCredential struct {
	ID            string          `json:"id"`
	IntegrationID string          `json:"integrationID"`
	Label         string          `json:"label"`
	Active        bool            `json:"active"`
	Value         json.RawMessage `json:"value"`
}

// OpenCodeImport puts together the JSON an OpenCode 2 agent's data directory
// gets with `opencode auth import`: one credential per provider, named after
// the account that was chosen for it, in the shape `auth export` prints. It is
// OpenCodeAuth's OpenCode 2 counterpart; auth is nil when there is no provider
// at all.
func (s Store) OpenCodeImport(chosen map[string]string) (auth []byte, used map[string]string, err error) {
	stored, err := s.OpenCodeProviders()
	if err != nil {
		return nil, nil, err
	}
	providers := openCodeProvidersWithGo(stored)
	for provider, name := range chosen {
		if name != "" && !slices.Contains(providers, provider) {
			return nil, nil, s.noAccount(ToolOpenCode, provider, name)
		}
	}
	used = map[string]string{}
	creds := make([]openCodeCredential, 0, len(providers))
	for _, provider := range providers {
		source := openCodeSourceProvider(provider, stored)
		name := chosen[provider]
		if name == "" && source != provider {
			name = chosen[source]
		}
		b, resolved, err := s.ToolAuth(ToolOpenCode, source, name)
		if err != nil {
			return nil, nil, err
		}
		if b == nil {
			return nil, nil, s.noAccount(ToolOpenCode, source, name)
		}
		used[provider] = resolved
		creds = append(creds, openCodeCredential{
			ID:            openCodeCredentialID(provider, resolved),
			IntegrationID: provider,
			Label:         resolved,
			Active:        true,
			Value:         openCodeV2Entry(source, b),
		})
	}
	if len(creds) == 0 {
		return nil, used, nil
	}
	auth, err = json.Marshal(creds)
	return auth, used, err
}

// SplitOpenCodeExport reads what `opencode auth export` printed into one entry
// per provider, as `agentbox auth opencode` stores what OpenCode 2's login
// wrote. The active credential of a provider is the one kept; an export with
// several accounts for one provider has no way to say which one AgentBox was
// asked for.
func SplitOpenCodeExport(b []byte) (map[string][]byte, error) {
	var creds []openCodeCredential
	if err := json.Unmarshal(b, &creds); err != nil {
		return nil, fmt.Errorf("reading OpenCode's exported credentials: %w", err)
	}
	out := map[string][]byte{}
	for _, c := range creds {
		if ValidateProvider(c.IntegrationID) != nil || len(c.Value) == 0 || !c.Active {
			// An account OpenCode 2 has switched away from is not one to sign
			// an agent in with.
			continue
		}
		out[c.IntegrationID] = c.Value
	}
	return out, nil
}

// openCodeCredentialID is the id an imported credential carries. OpenCode 2
// generates ids of its own when it logs a provider in; one of AgentBox's has
// to be stable across imports so that importing the same account twice
// replaces it rather than adding a second copy.
func openCodeCredentialID(provider, name string) string {
	return "cred_agentbox_" + openCodeIDPart(provider) + "_" + openCodeIDPart(name)
}

var openCodeIDPattern = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func openCodeIDPart(s string) string {
	s = openCodeIDPattern.ReplaceAllString(strings.TrimSpace(s), "_")
	if s == "" {
		return "default"
	}
	return s
}

// openCodeV2Entry is an account's stored entry as OpenCode 2's database takes
// it. OpenCode 2 requires the method a credential was created with
// ("device", "chatgpt-browser", …), which OpenCode 1 never stored; an account
// captured by OpenCode 1 gets the method its provider signs in with, and the
// account id OpenCode 1 kept at the top level goes where OpenCode 2 reads it.
// An entry captured by OpenCode 2 already has both and is left alone.
func openCodeV2Entry(provider string, b []byte) json.RawMessage {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(b, &entry); err != nil {
		return json.RawMessage(b)
	}
	var kind struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b, &kind)
	if kind.Type == "api" {
		// OpenCode 1 calls an API key's type "api"; OpenCode 2 calls it "key".
		entry["type"] = json.RawMessage(`"key"`)
	}
	if kind.Type == "oauth" {
		if _, ok := entry["methodID"]; !ok {
			method, _ := json.Marshal(openCodeMethod(provider))
			entry["methodID"] = method
		}
	}
	if id, ok := entry["accountId"]; ok {
		if _, has := entry["metadata"]; !has {
			metadata, _ := json.Marshal(map[string]json.RawMessage{"accountID": id})
			entry["metadata"] = metadata
		}
	}
	out, err := json.Marshal(entry)
	if err != nil {
		return json.RawMessage(b)
	}
	return out
}

// openCodeMethod is the login method OpenCode 2 records for a provider that
// OpenCode 1 signed in to. The two versions only ever signed in to these with
// OAuth; an unknown provider gets the device flow, which is what an OAuth
// credential without a method is most likely to have come from.
func openCodeMethod(provider string) string {
	if provider == "openai" {
		return "chatgpt-browser"
	}
	return "device"
}

// openCodeV1Entry is an account's stored entry as OpenCode 1's auth.json takes
// it. OpenCode 2 keeps the same fields and adds "methodID" and "metadata";
// OpenCode 1 knows neither. The account id OpenCode 2 carries in metadata
// ("accountID") is the "accountId" OpenCode 1 expects at the top level.
func openCodeV1Entry(b []byte) json.RawMessage {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(b, &entry); err != nil {
		return json.RawMessage(b)
	}
	var kind struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b, &kind)
	var metadata map[string]json.RawMessage
	if raw, ok := entry["metadata"]; ok {
		_ = json.Unmarshal(raw, &metadata)
	}
	delete(entry, "methodID")
	delete(entry, "metadata")
	if kind.Type == "key" {
		// OpenCode 2 calls an API key's type "key"; OpenCode 1 calls it "api".
		entry["type"] = json.RawMessage(`"api"`)
	}
	if _, ok := entry["accountId"]; !ok {
		if id, ok := metadata["accountID"]; ok {
			entry["accountId"] = id
		}
	}
	out, err := json.Marshal(entry)
	if err != nil {
		return json.RawMessage(b)
	}
	return out
}

// OpenCodeImportHome is an XDG data home for asking OpenCode 2 which models
// the stored login can run, and the export to import into it first. It is
// written afresh on every call: importing into a home that already holds a
// provider's credential leaves the old one in place.
func (s Store) OpenCodeImportHome() (dir string, export []byte, err error) {
	export, _, err = s.OpenCodeImport(nil)
	if err != nil {
		return "", nil, err
	}
	if export == nil {
		export = []byte("[]")
	}
	dir = filepath.Join(s.Dir, "opencode2-models")
	if err := os.RemoveAll(dir); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "opencode"), 0o700); err != nil {
		return "", nil, err
	}
	return dir, export, nil
}
