package mediapub

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubBlobBase(t *testing.T) {
	want := "https://github.com/leciric/agentbox/blob/agentbox-media"
	for _, remote := range []string{
		"https://github.com/leciric/agentbox.git",
		"https://github.com/leciric/agentbox",
		"https://x-access-token:abc@github.com/leciric/agentbox.git",
		"git@github.com:leciric/agentbox.git",
		"ssh://git@github.com/leciric/agentbox.git",
	} {
		if got, err := GitHubBlobBase(remote); err != nil || got != want {
			t.Errorf("GitHubBlobBase(%q) = %q, %v", remote, got, err)
		}
	}
	for _, remote := range []string{"https://gitlab.com/a/b.git", "/srv/git/b.git"} {
		if _, err := GitHubBlobBase(remote); err == nil {
			t.Errorf("GitHubBlobBase(%q) accepted a repository that isn't on GitHub", remote)
		}
	}
}

// Publishing twice from one agent: the branch is created with a README the
// first time, extended the second, and the agent's directory holds only what
// the second publish had — while another agent's directory is left alone.
func TestPublish(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs ffmpeg")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	origin, repo := filepath.Join(tmp, "origin.git"), filepath.Join(tmp, "repo")
	run(t, "", "git", "init", "-q", "--bare", origin)
	run(t, "", "git", "init", "-q", "-b", "main", repo)
	run(t, repo, "git", "remote", "add", "origin", origin)
	run(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")

	png := filepath.Join(tmp, "shot.png")
	clip := filepath.Join(tmp, "clip.mp4")
	run(t, "", "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x240", "-frames:v", "1", png)
	run(t, "", "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=s=320x240:r=15", "-t", "2", "-pix_fmt", "yuv420p", clip)
	file := func(p string) func(context.Context) (io.ReadCloser, error) {
		return func(context.Context) (io.ReadCloser, error) { return os.Open(p) }
	}
	opts := Options{Repo: repo, Dir: "agentbox/feat-x", BaseURL: "https://github.com/o/r/blob/agentbox-media"}

	first, err := Publish(ctx, []Item{
		{ID: "a1", Kind: "screenshot", Name: "Settings, before", Mime: "image/png", Open: file(png)},
		{ID: "a2", Kind: "report", Name: "junit", Mime: "application/xml", Open: file(png)},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	other := opts
	other.Dir = "agentbox/other"
	if _, err := Publish(ctx, []Item{{ID: "b1", Kind: "screenshot", Name: "x", Mime: "image/png", Open: file(png)}}, other); err != nil {
		t.Fatal(err)
	}
	second, err := Publish(ctx, []Item{
		{ID: "c1", Kind: "recording", Name: "The toggle", Mime: "video/mp4", Open: file(clip)},
		{ID: "c2", Kind: "note", Text: "Turned on, then off."},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}

	tree := run(t, origin, "git", "ls-tree", "-r", "--name-only", Branch)
	if want := "README.md\nagentbox/feat-x/the-toggle.gif\nagentbox/feat-x/the-toggle.mp4\nagentbox/other/x.png\n"; tree != want {
		t.Errorf("the branch holds\n%s\nwant\n%s", tree, want)
	}
	if n := strings.TrimSpace(run(t, origin, "git", "rev-list", "--count", Branch)); n != "3" {
		t.Errorf("the branch has %s commits, want 3: each publish builds on the last", n)
	}
	if !strings.Contains(first.Markdown, "![Settings, before](https://github.com/o/r/blob/agentbox-media/agentbox/feat-x/settings-before.png?raw=true)") {
		t.Errorf("first Markdown:\n%s", first.Markdown)
	}
	if strings.Contains(first.Markdown, "junit") {
		t.Errorf("a report was published:\n%s", first.Markdown)
	}
	for _, want := range []string{
		"[![The toggle](https://github.com/o/r/blob/agentbox-media/agentbox/feat-x/the-toggle.gif?raw=true)](https://github.com/o/r/blob/agentbox-media/agentbox/feat-x/the-toggle.mp4?raw=true)",
		"[The toggle (mp4)](",
		"> Turned on, then off.",
	} {
		if !strings.Contains(second.Markdown, want) {
			t.Errorf("second Markdown doesn't have %q:\n%s", want, second.Markdown)
		}
	}
	// Nothing in the repository itself moved: no local branch, a clean main.
	if refs := run(t, repo, "git", "for-each-ref", "--format=%(refname)", "refs/heads"); refs != "refs/heads/main\n" {
		t.Errorf("local refs after publishing:\n%s", refs)
	}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, stderr.String())
	}
	return out.String()
}
