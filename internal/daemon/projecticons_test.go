package daemon

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

var testPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)

// A project's icon is looked for once and kept; choosing one wins over it,
// and resetting looks again. Every change moves the project's IconVersion.
func TestProjectIconRoutes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	root := d.fixtureRepo(t, "hello-stack")
	if err := os.WriteFile(filepath.Join(root, "logo.png"), testPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	version := func() int64 {
		t.Helper()
		p, err := d.client.Project(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		return p.IconVersion
	}
	if v := version(); v != 0 {
		t.Errorf("IconVersion before any look = %d, want 0", v)
	}

	// The repository's own logo, found once.
	ic, err := d.client.ProjectIcon(ctx, "hello-stack")
	if err != nil || ic.Source != "detected" || ic.DetectedFrom != "logo.png" || !ic.HasDetected ||
		ic.Image != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(testPNG) {
		t.Fatalf("ProjectIcon() = %+v, %v", ic, err)
	}
	v1 := version()
	if v1 == 0 {
		t.Error("IconVersion stayed 0 after an icon was found")
	}

	// A chosen emoji, then refused choices that change nothing.
	if ic, err = d.client.SetProjectIcon(ctx, "hello-stack", api.SetProjectIconRequest{Emoji: "🐶"}); err != nil || ic.Source != "chosen" || ic.Emoji != "🐶" || !ic.HasDetected {
		t.Errorf("SetProjectIcon(emoji) = %+v, %v", ic, err)
	}
	v2 := version()
	if v2 <= v1 {
		t.Errorf("IconVersion after choosing = %d, want more than %d", v2, v1)
	}
	for name, req := range map[string]api.SetProjectIconRequest{
		"neither":        {},
		"both":           {Emoji: "🐶", Image: "data:image/png;base64,AA=="},
		"two emoji":      {Emoji: "🐶 🐱"},
		"not an image":   {Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<html>"))},
		"not a data URL": {Image: "https://example.com/x.png"},
	} {
		if _, err := d.client.SetProjectIcon(ctx, "hello-stack", req); err == nil {
			t.Errorf("SetProjectIcon(%s) succeeded", name)
		}
	}
	if v := version(); v != v2 {
		t.Errorf("a refused choice moved IconVersion to %d from %d", v, v2)
	}
	if ic, _ = d.client.ProjectIcon(ctx, "hello-stack"); ic.Source != "chosen" || ic.Emoji != "🐶" {
		t.Errorf("ProjectIcon() after choosing = %+v", ic)
	}

	// An image of its own.
	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG)
	if ic, err = d.client.SetProjectIcon(ctx, "hello-stack", api.SetProjectIconRequest{Image: img}); err != nil || ic.Source != "chosen" || ic.Image != img || ic.Emoji != "" {
		t.Errorf("SetProjectIcon(image) = %+v, %v", ic, err)
	}

	// Resetting looks again, and finds the favicon added meanwhile.
	if err := os.WriteFile(filepath.Join(root, "favicon.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ic, err = d.client.ResetProjectIcon(ctx, "hello-stack"); err != nil || ic.Source != "detected" || ic.DetectedFrom != "favicon.svg" ||
		!strings.HasPrefix(ic.Image, "data:image/svg+xml;base64,") {
		t.Errorf("ResetProjectIcon() = %+v, %v", ic, err)
	}
	if _, err := d.client.ProjectIcon(ctx, "missing"); err == nil {
		t.Error("ProjectIcon(missing) succeeded")
	}
}

// A look that found nothing is kept too, so reads don't walk the repository
// every time, and nothing stands in for the icon: not the GitHub owner's
// avatar, even for a repository whose origin is on GitHub.
func TestProjectIconRemembersFindingNothing(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	root := d.fixtureRepo(t, "hello-stack")
	if out, err := exec.Command("git", "-C", root, "remote", "add", "origin", "https://github.com/leciric/hello-stack.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v %s", err, out)
	}
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if ic, err := d.client.ProjectIcon(ctx, "hello-stack"); err != nil || ic.Source != "none" || ic.HasDetected || ic.Image != "" {
			t.Fatalf("ProjectIcon() = %+v, %v", ic, err)
		}
	}
	stored, _, err := d.srv.store.ProjectIcon(ctx, "hello-stack")
	if err != nil || stored.DetectedAt.IsZero() {
		t.Errorf("the empty look wasn't kept: %+v, %v", stored, err)
	}
	// The project's removal takes its icon with it.
	if err := d.client.RemoveProject(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.srv.store.ProjectIcon(ctx, "hello-stack"); ok {
		t.Error("the icon outlived its project")
	}
}

func TestProjectActivityRoute(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	if got, err := d.client.ProjectActivity(ctx); err != nil || len(got) != 0 {
		t.Fatalf("ProjectActivity() with no chat = %+v, %v", got, err)
	}
	if err := d.srv.store.SaveChatItems(ctx, "hello-stack", "lead", []state.ChatItem{
		{ID: "u", Position: 1, Data: []byte(`{"id":"u","kind":"user","text":"hi","createdAt":"2026-10-15T10:00:00.123456789Z"}`)},
		{ID: "w", Position: 2, Data: []byte(`{"id":"w","kind":"user","woken":true,"createdAt":"2026-10-15T11:00:00Z"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := d.client.ProjectActivity(ctx)
	if err != nil || len(got) != 1 || got[0].Project != "hello-stack" || got[0].LastActivity == nil || got[0].LastMessage == nil {
		t.Fatalf("ProjectActivity() = %+v, %v", got, err)
	}
	if a, m := got[0].LastActivity.UTC().Format("15:04:05.000000000"), got[0].LastMessage.UTC().Format("15:04:05.000000000"); a != "11:00:00.000000000" || m != "10:00:00.123456789" {
		t.Errorf("ProjectActivity() = activity %s, message %s", a, m)
	}
}

