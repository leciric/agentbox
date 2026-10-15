package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
)

// Codex and OpenCode accounts are added from an API key, listed with what
// they are, picked per project when it is added and in its settings, and
// renamed everywhere they are named; an account that isn't stored is refused.
func TestProjectToolAccounts(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()

	for _, req := range []struct {
		tool string
		api.ToolAccountRequest
	}{
		{"codex", api.ToolAccountRequest{APIKey: "sk-default"}},
		{"codex", api.ToolAccountRequest{Account: "work", APIKey: "sk-work"}},
		{"opencode", api.ToolAccountRequest{Provider: "anthropic", APIKey: "a-default"}},
		{"opencode", api.ToolAccountRequest{Provider: "anthropic", Account: "work", APIKey: "a-work"}},
		{"opencode", api.ToolAccountRequest{Provider: "openai", Account: "home", APIKey: "o-home"}},
	} {
		if err := d.client.SaveToolAccount(ctx, req.tool, req.ToolAccountRequest); err != nil {
			t.Fatalf("SaveToolAccount(%s, %+v): %v", req.tool, req.ToolAccountRequest, err)
		}
	}
	if err := d.client.SaveToolAccount(ctx, "opencode", api.ToolAccountRequest{APIKey: "x"}); err == nil {
		t.Error("an OpenCode key with no provider was stored")
	}
	if err := d.client.SaveToolAccount(ctx, "codex", api.ToolAccountRequest{Account: "Bad Name", APIKey: "x"}); err == nil {
		t.Error("an account with a name that can't be a file name was stored")
	}
	auth, err := d.client.Auth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Codex || len(auth.CodexAccounts) != 2 || auth.CodexAccounts[0].Kind != "apikey" || !auth.CodexAccounts[0].Default {
		t.Errorf("Codex accounts = %+v", auth.CodexAccounts)
	}
	if !auth.OpenCode || len(auth.OpenCodeAccounts) != 3 || auth.OpenCodeAccounts[2].Provider != "openai" {
		t.Errorf("OpenCode accounts = %+v", auth.OpenCodeAccounts)
	}
	if auth.CursorAccounts == nil || len(auth.CursorAccounts) != 0 {
		t.Errorf("Cursor accounts = %#v, want an empty list", auth.CursorAccounts)
	}

	// Picked when the project is added; an unknown one refuses the project.
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), CodexAccount: "missing"}); err == nil || !strings.Contains(err.Error(), "agentbox auth codex") {
		t.Errorf("a project on a Codex account that isn't stored: %v", err)
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"), OpenCodeAccounts: map[string]string{"openai": "work"}}); err == nil {
		t.Error("a project on an OpenCode account its provider doesn't have was added")
	}
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack"),
		CodexAccount: "work", OpenCodeAccounts: map[string]string{"anthropic": "work", "openai": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if p.CodexAccount != "work" || p.CursorAccount != "" || len(p.OpenCodeAccounts) != 1 || p.OpenCodeAccounts["anthropic"] != "work" {
		t.Errorf("added project = codex %q, opencode %v, cursor %q", p.CodexAccount, p.OpenCodeAccounts, p.CursorAccount)
	}

	// Changed in its settings.
	none, openai := "", map[string]string{"openai": "home"}
	if p, err = d.client.UpdateProject(ctx, p.Name, api.UpdateProjectRequest{CodexAccount: &none, OpenCodeAccounts: &openai}); err != nil {
		t.Fatal(err)
	}
	if p.CodexAccount != "" || p.OpenCodeAccounts["openai"] != "home" || p.OpenCodeAccounts["anthropic"] != "" {
		t.Errorf("updated project = codex %q, opencode %v", p.CodexAccount, p.OpenCodeAccounts)
	}
	missing := "missing"
	if _, err := d.client.UpdateProject(ctx, p.Name, api.UpdateProjectRequest{CursorAccount: &missing}); err == nil {
		t.Error("a Cursor account that isn't stored was picked")
	}

	// Renaming carries the project's choice over; the old name is gone.
	got, err := d.client.RenameToolAccount(ctx, "opencode", "openai", "home", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Projects, ",") != p.Name || got.Provider != "openai" {
		t.Errorf("renamed = %+v", got)
	}
	if p, err = d.client.Project(ctx, p.Name); err != nil || p.OpenCodeAccounts["openai"] != "personal" {
		t.Errorf("after the rename: %v, %v", p.OpenCodeAccounts, err)
	}
	if _, err := d.client.RenameToolAccount(ctx, "codex", "", "nope", "other"); err == nil {
		t.Error("renaming an account that doesn't exist succeeded")
	}

	// Default and remove.
	if err := d.client.SetDefaultToolAccount(ctx, "codex", "", "work"); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if def, _ := creds.DefaultToolAccount("codex", ""); def != "work" {
		t.Errorf("the Codex default is %q after making work it", def)
	}
	if err := d.client.RemoveToolAccount(ctx, "opencode", "anthropic", "work"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := creds.HasToolAccount("opencode", "anthropic", "work"); ok {
		t.Error("the removed account is still stored")
	}
}
