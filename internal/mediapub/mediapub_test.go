package mediapub

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
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

// newOrigin makes a bare "remote" and a repo with one commit on main that
// pushes to it, the way an agent's worktree does.
func newOrigin(t *testing.T) (origin, repo string) {
	t.Helper()
	tmp := t.TempDir()
	origin, repo = filepath.Join(tmp, "origin.git"), filepath.Join(tmp, "repo")
	run(t, "", "git", "init", "-q", "--bare", origin)
	run(t, "", "git", "init", "-q", "-b", "main", repo)
	run(t, repo, "git", "remote", "add", "origin", origin)
	run(t, repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	return origin, repo
}

// writePNG makes a real, small, valid PNG: this is real image data, not a
// fixture that only means something once ffmpeg has decoded it, so this test
// runs the same whether or not ffmpeg is installed (unlike a video, nothing
// here re-encodes it, since it's already under maxImageBytes).
func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 200, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func file(p string) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) { return os.Open(p) }
}

// TestPublish is the git-plumbing and Markdown assembly path: it commits an
// image and a "video" (bytes no encoder can shrink or preview) to the remote,
// publishes again from another agent's directory, then republishes the first
// agent's, and checks that the branch, the commit count and the directory
// each carry the right thing. It doesn't need ffmpeg — a video that isn't
// valid footage takes the same fallback path an environment without ffmpeg
// does (the file is kept as it is, with no GIF preview) — so this is the
// test that runs everywhere, including CI's coverage job.
func TestPublish(t *testing.T) {
	ctx := context.Background()
	origin, repo := newOrigin(t)
	tmp := t.TempDir()

	png := filepath.Join(tmp, "shot.png")
	writePNG(t, png)
	clip := filepath.Join(tmp, "clip.mp4")
	if err := os.WriteFile(clip, bytes.Repeat([]byte{0}, 1024), 0o600); err != nil {
		t.Fatal(err)
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
	if want := "README.md\nagentbox/feat-x/the-toggle.mp4\nagentbox/other/x.png\n"; tree != want {
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
		"[The toggle (mp4)](https://github.com/o/r/blob/agentbox-media/agentbox/feat-x/the-toggle.mp4?raw=true)",
		"> Turned on, then off.",
	} {
		if !strings.Contains(second.Markdown, want) {
			t.Errorf("second Markdown doesn't have %q:\n%s", want, second.Markdown)
		}
	}
	if strings.Contains(second.Markdown, ".gif") {
		t.Errorf("a GIF preview was made for footage that isn't real: %s", second.Markdown)
	}
	// Nothing in the repository itself moved: no local branch, a clean main.
	if refs := run(t, repo, "git", "for-each-ref", "--format=%(refname)", "refs/heads"); refs != "refs/heads/main\n" {
		t.Errorf("local refs after publishing:\n%s", refs)
	}
}

// TestPublishRecordingWithFFmpeg is the one part TestPublish can't cover
// without ffmpeg: a real recording is re-encoded smaller and gets a GIF
// preview, and the Markdown links both.
func TestPublishRecordingWithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs ffmpeg")
	}
	ctx := context.Background()
	_, repo := newOrigin(t)
	tmp := t.TempDir()

	clip := filepath.Join(tmp, "clip.mp4")
	run(t, "", "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=s=320x240:r=15", "-t", "2", "-pix_fmt", "yuv420p", clip)
	opts := Options{Repo: repo, Dir: "agentbox/feat-x", BaseURL: "https://github.com/o/r/blob/agentbox-media"}

	result, err := Publish(ctx, []Item{
		{ID: "c1", Kind: "recording", Name: "The toggle", Mime: "video/mp4", Open: file(clip)},
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[![The toggle](https://github.com/o/r/blob/agentbox-media/agentbox/feat-x/the-toggle.gif?raw=true)](https://github.com/o/r/blob/agentbox-media/agentbox/feat-x/the-toggle.mp4?raw=true)",
		"[The toggle (mp4)](",
	} {
		if !strings.Contains(result.Markdown, want) {
			t.Errorf("Markdown doesn't have %q:\n%s", want, result.Markdown)
		}
	}
	for _, f := range []string{"agentbox/feat-x/the-toggle.mp4", "agentbox/feat-x/the-toggle.gif"} {
		if !contains(result.Files, f) {
			t.Errorf("Files = %v, want %s", result.Files, f)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
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
