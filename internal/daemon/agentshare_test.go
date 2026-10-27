package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// An agent's run is shared as one page with its screenshot in it, and
// without the values of the secrets it was given; a past agent's too.
func TestAgentShare(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-08", Instance: "ab-hello-stack-agent-08", AI: "claude", Branch: "agentbox/agent-08",
		Worktree: "/gone/agent-08", Status: state.AgentReady, CreatedAt: time.Now(), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	const secret = "s3cr3t-value-of-the-deploy-key"
	if _, err := d.srv.secrets().Set(ctx, a.Project, "", "DEPLOY_KEY", secret); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	items := []api.ChatItem{
		{ID: "u1", Turn: "u1", Kind: "user", Text: "Fix the footer", Result: &api.ChatTurnResult{State: "done"}, CreatedAt: t0, UpdatedAt: t0},
		{ID: "t1", Turn: "u1", Kind: "tool", Tool: &api.ChatTool{Kind: "execute", Command: "./deploy --key " + secret, Status: "completed", Output: "deployed"}, CreatedAt: t0.Add(time.Minute), UpdatedAt: t0.Add(2 * time.Minute)},
		{ID: "a1", Turn: "u1", Kind: "assistant", Text: "The footer is fixed.", CreatedAt: t0.Add(3 * time.Minute)},
	}
	var rows []state.ChatItem
	for i, it := range items {
		data, _ := json.Marshal(it)
		rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(i), Data: data})
	}
	if err := d.srv.store.SaveChatItems(ctx, a.Project, a.Name, rows); err != nil {
		t.Fatal(err)
	}
	dir := d.srv.manager(nil).MediaDir(a.Project, a.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "after.png"), pic.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.AddMedia(ctx, state.Media{ID: "shot", Project: a.Project, Agent: a.Name, Kind: "screenshot", Name: "after", File: "after.png", Mime: "image/png", Source: "agent", Meta: "{}", CreatedAt: t0.Add(90 * time.Second)}); err != nil {
		t.Fatal(err)
	}

	check := func(past bool) {
		t.Helper()
		page, err := d.client.AgentShare(ctx, a.Ref())
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Fix the footer", "The footer is fixed.", "data:image/png;base64,", "Made with", "./deploy --key [redacted]"} {
			if !strings.Contains(page, want) {
				t.Errorf("past %v: the page lacks %q", past, want)
			}
		}
		if strings.Contains(page, secret) {
			t.Errorf("past %v: the page has the secret's value", past)
		}
	}
	check(false)
	if a, err := d.srv.store.Agent(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	} else if err := d.srv.store.ArchiveAgent(ctx, a, "", state.ArchivedPR{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	check(true)
	if _, err := d.client.AgentShare(ctx, a.Project+"/agent-nope"); err == nil {
		t.Error("an agent that doesn't exist has a page")
	}
}
