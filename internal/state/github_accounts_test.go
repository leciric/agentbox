package state

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Renaming a GitHub account carries it over to every project and agent that
// names it, and nowhere else: Claude Code accounts of the same name and other
// GitHub accounts stay as they were.
func TestRenameGitHubAccount(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, p := range []Project{
		{Name: "own", Root: "/src/own", ClaudeAccount: "work", GitHubAccount: "work"},
		{Name: "also", Root: "/src/also", GitHubAccount: "work"},
		{Name: "other", Root: "/src/other", GitHubAccount: "client"},
		{Name: "default", Root: "/src/default"},
	} {
		p.CreatedAt = time.Now()
		if err := st.AddProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []Agent{
		{Project: "own", Name: "agent-01", ClaudeAccount: "work", GitHubAccount: "work"},
		{Project: "own", Name: LeadName, ClaudeAccount: "work", Role: "lead"},
		{Project: "other", Name: "agent-01", GitHubAccount: "client"},
		{Project: "other", Name: "agent-02", GitHubAccount: "work"},
	} {
		a.AI, a.Status, a.CreatedAt = "claude", AgentReady, time.Now()
		a.Instance = a.Project + "-" + a.Name
		if err := st.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	// A move that fails undoes everything.
	if _, err := st.RenameGitHubAccount(ctx, "work", "personal", func() error { return fmt.Errorf("disk full") }); err == nil {
		t.Fatal("a failed move was not reported")
	}
	if p, _ := st.Project(ctx, "own"); p.GitHubAccount != "work" {
		t.Fatalf("a failed rename left own on %q", p.GitHubAccount)
	}

	moved := false
	done, err := st.RenameGitHubAccount(ctx, "work", "personal", func() error { moved = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Error("move was not called")
	}
	if want := []string{"also", "own"}; !reflect.DeepEqual(done.Projects, want) {
		t.Errorf("projects carried over = %v, want %v", done.Projects, want)
	}
	if want := []string{"other/agent-02", "own/agent-01"}; !reflect.DeepEqual(done.Agents, want) {
		t.Errorf("agents carried over = %v, want %v", done.Agents, want)
	}

	for _, c := range []struct{ name, github, claude string }{
		{"own", "personal", "work"},
		{"also", "personal", ""},
		{"other", "client", ""},
		{"default", "", ""},
	} {
		p, err := st.Project(ctx, c.name)
		if err != nil {
			t.Fatal(err)
		}
		if p.GitHubAccount != c.github || p.ClaudeAccount != c.claude {
			t.Errorf("%s: github %q, claude %q; want %q, %q", c.name, p.GitHubAccount, p.ClaudeAccount, c.github, c.claude)
		}
	}
	for _, c := range []struct{ project, name, github, claude string }{
		{"own", "agent-01", "personal", "work"},
		{"own", LeadName, "", "work"},
		{"other", "agent-01", "client", ""},
		{"other", "agent-02", "personal", ""},
	} {
		a, err := st.Agent(ctx, c.project, c.name)
		if err != nil {
			t.Fatal(err)
		}
		if a.GitHubAccount != c.github || a.ClaudeAccount != c.claude {
			t.Errorf("%s/%s: github %q, claude %q; want %q, %q", c.project, c.name, a.GitHubAccount, a.ClaudeAccount, c.github, c.claude)
		}
	}
}
