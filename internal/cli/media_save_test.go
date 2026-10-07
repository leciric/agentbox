package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/paths"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// addMediaRow keeps an item for an agent the way the daemon does: its file
// under the data directory's media folder, and its row in the state.
func addMediaRow(t *testing.T, m state.Media, content string) {
	t.Helper()
	p, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if m.File != "" {
		file := filepath.Join(p.Data, "media", m.Project, m.Agent, m.File)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		m.Size = int64(len(content))
	}
	st, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	m.Source, m.Meta = "agent", "{}"
	if err := st.AddMedia(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func TestMediaSaveCmd(t *testing.T) {
	isolate(t)
	startDaemon(t)
	if _, err := run(t, "", "add", testutil.FixtureRepo(t, "hello-stack")); err != nil {
		t.Fatal(err)
	}
	addAgentRow(t, "hello-stack", "agent-01")
	now := time.Now()
	for _, m := range []struct {
		item    state.Media
		content string
	}{
		{state.Media{ID: "old", Kind: "screenshot", Name: "empty-state", File: "old/empty-state.png", Mime: "image/png", CreatedAt: now.Add(-time.Hour)}, "old take"},
		{state.Media{ID: "new", Kind: "screenshot", Name: "empty-state", File: "new/empty-state.png", Mime: "image/png", CreatedAt: now}, "new take"},
		{state.Media{ID: "note", Kind: "note", Name: "summary", Text: "done", CreatedAt: now}, ""},
	} {
		m.item.Project, m.item.Agent = "hello-stack", "agent-01"
		addMediaRow(t, m.item, m.content)
	}

	// To a file: the newest item of that name.
	dest := filepath.Join(t.TempDir(), "shot.png")
	out, err := run(t, "", "media", "save", "hello-stack/agent-01", "--name", "empty-state", dest)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `Saved screenshot "empty-state" to `+dest)
	if b, err := os.ReadFile(dest); err != nil || string(b) != "new take" {
		t.Errorf("saved %q, %v; want the newest take", b, err)
	}

	// Into a directory: under the item's own file name.
	dir := t.TempDir()
	if _, err := run(t, "", "media", "save", "hello-stack/agent-01", "--name", "empty-state", dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "empty-state.png")); err != nil || string(b) != "new take" {
		t.Errorf("saved into the directory %q, %v", b, err)
	}

	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"no name", "pass --name", []string{"media", "save", "hello-stack/agent-01", dest}},
		{"no path", "usage:", []string{"media", "save", "hello-stack/agent-01", "--name", "empty-state"}},
		{"unknown name", `no media named "nope"`, []string{"media", "save", "hello-stack/agent-01", "--name", "nope", dest}},
		{"existing file", "agentbox media add " + dest + " --name nope", []string{"media", "save", "hello-stack/agent-01", "--name", "nope", dest}},
		{"a note", "is a note", []string{"media", "save", "hello-stack/agent-01", "--name", "summary", dest}},
	} {
		if _, err := run(t, "", c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error about %q", c.name, err, c.want)
		}
	}
}
