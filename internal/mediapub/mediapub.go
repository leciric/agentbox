// Package mediapub puts an agent's screenshots and recordings where a pull
// request can show them. GitHub has no API for attaching a file to a pull
// request, so they are committed to an orphan branch of the project's own
// repository (Branch, "agentbox-media"), one directory per agent branch, and
// the pull request's description points at them with blob URLs ending in
// ?raw=true: those go through GitHub's own login, so they show on a private
// repository too, to anyone who can read it.
//
// Everything is done with git plumbing against a throwaway index, so no
// working tree and no local branch is touched: the commit is pushed by its
// hash straight to the remote's branch. The .git directory is shared by the
// host and every agent of the project, so nothing here may move a ref.
package mediapub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Branch is the orphan branch the media is committed to.
const Branch = "agentbox-media"

// Limits that keep what a pull request loads, and what the branch grows by,
// small: a GIF preview is a few seconds of a recording at a low frame rate,
// and anything bigger is only linked, as an mp4.
const (
	maxGIFBytes   = 8 << 20  // GitHub stops rendering an image in Markdown at 10 MB
	maxVideoBytes = 25 << 20 // re-encoded again, smaller, above this
	maxImageBytes = 3 << 20  // a PNG above this becomes a JPEG
)

// gifTries are the GIF previews tried in turn until one fits maxGIFBytes.
var gifTries = []struct{ seconds, fps, width int }{
	{20, 8, 640},
	{12, 6, 480},
	{8, 5, 360},
}

// Item is one piece of media to publish.
type Item struct {
	ID   string
	Kind string // screenshot, recording, note, …
	Name string
	Mime string
	Text string // a note's text
	// Open reads the item's file. Nil for a note.
	Open func(context.Context) (io.ReadCloser, error)
}

// Options says what to publish, and where.
type Options struct {
	// Repo is a directory in the repository to commit to.
	Repo string
	// Remote is the remote to push to, "origin" by default.
	Remote string
	// Dir is the directory on Branch the media goes in: the agent's branch.
	Dir string
	// BaseURL is where Branch's files are read from, without a trailing
	// slash: https://github.com/<owner>/<repo>/blob/agentbox-media. Empty
	// means it's worked out from Remote's URL (GitHubBlobBase).
	BaseURL string
	// NoPush commits without pushing, for a look first (and for tests).
	NoPush bool
	// Warn is told what couldn't be done as asked: no ffmpeg, a GIF that
	// wouldn't fit. May be nil.
	Warn func(string)
}

// Result is what was published.
type Result struct {
	Commit   string
	Files    []string // paths on Branch
	Markdown string
}

// Publish commits items to Branch under opts.Dir, replacing whatever that
// directory held, pushes it, and returns the Markdown that shows them.
func Publish(ctx context.Context, items []Item, opts Options) (Result, error) {
	if opts.Remote == "" {
		opts.Remote = "origin"
	}
	if opts.Warn == nil {
		opts.Warn = func(string) {}
	}
	dir := strings.Trim(path.Clean("/"+filepath.ToSlash(opts.Dir)), "/")
	if dir == "" {
		return Result{}, errors.New("no directory to publish into: name the agent's branch")
	}
	base := opts.BaseURL
	if base == "" {
		remoteURL, err := git(ctx, opts.Repo, nil, "remote", "get-url", opts.Remote)
		if err != nil {
			return Result{}, err
		}
		if base, err = GitHubBlobBase(remoteURL); err != nil {
			return Result{}, err
		}
	}

	work, err := os.MkdirTemp("", "agentbox-media-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	files, md, err := prepare(ctx, items, work, dir, base, opts.Warn)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, errors.New("nothing to publish: this agent has no screenshots or recordings")
	}

	// Another agent may push to the branch between the fetch and the push:
	// the push is then refused as not a fast-forward, and the commit is made
	// again on top of theirs.
	var commit string
	for attempt := 0; ; attempt++ {
		commit, err = commitFiles(ctx, opts.Repo, opts.Remote, dir, work, files, !opts.NoPush)
		if err != nil || opts.NoPush {
			break
		}
		_, err = git(ctx, opts.Repo, nil, "push", "--quiet", opts.Remote, commit+":refs/heads/"+Branch)
		if err == nil || attempt == 2 {
			break
		}
	}
	if err != nil {
		return Result{}, err
	}
	out := Result{Commit: commit, Markdown: md}
	for _, f := range files {
		out.Files = append(out.Files, dir+"/"+f)
	}
	return out, nil
}

// prepare writes each item's publishable files into work and returns their
// names and the Markdown that shows them.
func prepare(ctx context.Context, items []Item, work, dir, base string, warn func(string)) ([]string, string, error) {
	_, ffmpegErr := exec.LookPath("ffmpeg")
	hasFFmpeg := ffmpegErr == nil
	if !hasFFmpeg {
		warn("ffmpeg isn't installed: files are published as they are, and recordings get no GIF preview")
	}
	link := func(name string) string {
		return base + "/" + escapePath(dir+"/"+name) + "?raw=true"
	}
	used := map[string]bool{}
	unique := func(stem, ext string) string {
		name := stem + ext
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		used[name] = true
		return name
	}

	var files []string
	var md strings.Builder
	md.WriteString("### Media\n")
	for _, it := range items {
		title := strings.TrimSpace(it.Name)
		if title == "" {
			title = it.Kind
		}
		switch {
		case it.Kind == "note":
			if text := strings.TrimSpace(it.Text); text != "" {
				md.WriteString("\n> " + strings.ReplaceAll(text, "\n", "\n> ") + "\n")
			}
			continue
		case it.Open == nil:
			continue
		case strings.HasPrefix(it.Mime, "image/"), strings.HasPrefix(it.Mime, "video/"):
		default:
			continue // reports, logs and other files stay in AgentBox
		}
		src := filepath.Join(work, "src-"+it.ID)
		if err := fetch(ctx, it, src); err != nil {
			return nil, "", fmt.Errorf("%s: %w", title, err)
		}
		stem := slug(title, it.ID)
		if strings.HasPrefix(it.Mime, "image/") {
			ext := imageExt(it.Mime)
			if info, _ := os.Stat(src); hasFFmpeg && info != nil && info.Size() > maxImageBytes && ext != ".gif" {
				jpg := src + ".jpg"
				if ffmpeg(ctx, "-i", src, "-vf", "scale='min(1920,iw)':-2", "-q:v", "4", jpg) == nil {
					src, ext = jpg, ".jpg"
				}
			}
			name := unique(stem, ext)
			if err := os.Rename(src, filepath.Join(work, name)); err != nil {
				return nil, "", err
			}
			files = append(files, name)
			fmt.Fprintf(&md, "\n**%s**\n\n![%s](%s)\n", mdText(title), mdText(title), link(name))
			continue
		}
		// A recording: a smaller mp4 to link, and a GIF to show inline.
		video := unique(stem, ".mp4")
		if hasFFmpeg {
			if err := shrinkVideo(ctx, src, filepath.Join(work, video)); err != nil {
				warn(fmt.Sprintf("%s: couldn't re-encode it (%v), publishing it as it is", title, err))
				if err := os.Rename(src, filepath.Join(work, video)); err != nil {
					return nil, "", err
				}
			}
		} else if err := os.Rename(src, filepath.Join(work, video)); err != nil {
			return nil, "", err
		}
		files = append(files, video)
		fmt.Fprintf(&md, "\n**%s**\n\n", mdText(title))
		if hasFFmpeg {
			gif := unique(stem, ".gif")
			if err := makeGIF(ctx, filepath.Join(work, video), filepath.Join(work, gif)); err != nil {
				warn(fmt.Sprintf("%s: no GIF preview: %v", title, err))
				used[gif] = false
			} else {
				files = append(files, gif)
				fmt.Fprintf(&md, "[![%s](%s)](%s)\n\n", mdText(title), link(gif), link(video))
			}
		}
		fmt.Fprintf(&md, "[%s (mp4)](%s)\n", mdText(title), link(video))
	}
	return files, md.String(), nil
}

func fetch(ctx context.Context, it Item, dst string) error {
	r, err := it.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// shrinkVideo re-encodes a recording small enough to keep in a repository:
// no sound (a screen recording has none worth keeping), at most 1280 wide and
// 15 frames a second, and smaller again if that's still over maxVideoBytes.
// It keeps the original when that is already the smaller of the two.
func shrinkVideo(ctx context.Context, src, dst string) error {
	for _, try := range []struct{ width, crf int }{{1280, 30}, {960, 36}} {
		err := ffmpeg(ctx, "-i", src, "-an",
			"-vf", fmt.Sprintf("scale='min(%d,iw)':-2,fps=15", try.width),
			"-c:v", "libx264", "-preset", "slow", "-crf", fmt.Sprint(try.crf),
			"-pix_fmt", "yuv420p", "-movflags", "+faststart", dst)
		if err != nil {
			return err
		}
		if size(dst) <= maxVideoBytes {
			break
		}
	}
	if orig := size(src); orig > 0 && orig < size(dst) {
		return os.Rename(src, dst)
	}
	return nil
}

// makeGIF makes the inline preview: the recording's first seconds, at a low
// frame rate, with a palette of its own, tried smaller until it fits.
func makeGIF(ctx context.Context, video, dst string) error {
	for _, try := range gifTries {
		err := ffmpeg(ctx, "-t", fmt.Sprint(try.seconds), "-i", video,
			"-vf", fmt.Sprintf("fps=%d,scale='min(%d,iw)':-2:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer", try.fps, try.width),
			"-loop", "0", dst)
		if err != nil {
			return err
		}
		if size(dst) <= maxGIFBytes {
			return nil
		}
	}
	_ = os.Remove(dst)
	return fmt.Errorf("even the smallest preview is over %d MB", maxGIFBytes>>20)
}

func ffmpeg(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("ffmpeg: %s", lastLine(msg))
		}
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return nil
}

// commitFiles makes a commit of Branch as the remote has it, with dir
// replaced by files, and returns its hash. Nothing but the object database
// changes: the index is a temporary one and no ref is written.
func commitFiles(ctx context.Context, repo, remote, dir, work string, files []string, fetchFirst bool) (string, error) {
	var parent string
	if fetchFirst {
		// A branch that doesn't exist yet is the one case where this fails
		// and publishing goes on: the first publish creates it.
		if _, err := git(ctx, repo, nil, "fetch", "--quiet", "--no-tags", remote, "refs/heads/"+Branch); err == nil {
			parent, _ = git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "FETCH_HEAD^{commit}")
		}
	}
	index := filepath.Join(work, "index")
	env := []string{"GIT_INDEX_FILE=" + index}
	if parent != "" {
		if _, err := git(ctx, repo, env, "read-tree", parent); err != nil {
			return "", err
		}
		if _, err := git(ctx, repo, env, "rm", "--cached", "-r", "-q", "--ignore-unmatch", "--", dir); err != nil {
			return "", err
		}
	} else {
		readme := filepath.Join(work, "README.md")
		if err := os.WriteFile(readme, []byte(readmeText), 0o644); err != nil {
			return "", err
		}
		if err := addFile(ctx, repo, env, readme, "README.md"); err != nil {
			return "", err
		}
	}
	for _, f := range files {
		if err := addFile(ctx, repo, env, filepath.Join(work, f), dir+"/"+f); err != nil {
			return "", err
		}
	}
	tree, err := git(ctx, repo, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-m", "Media for " + dir}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return git(ctx, repo, identity(ctx, repo), args...)
}

const readmeText = `# agentbox-media

Screenshots and recordings that AgentBox agents published for their pull
requests (` + "`agentbox media publish`" + `), one directory per agent branch. Nothing here
is ever merged: this branch only holds files for pull request descriptions to
show.
`

func addFile(ctx context.Context, repo string, env []string, file, name string) error {
	hash, err := git(ctx, repo, nil, "hash-object", "-w", "--", file)
	if err != nil {
		return err
	}
	_, err = git(ctx, repo, env, "update-index", "--add", "--cacheinfo", "100644,"+hash+","+name)
	return err
}

// identity is the commit's author when git has none configured, so publishing
// works on a machine nobody set user.name on.
func identity(ctx context.Context, repo string) []string {
	if name, _ := git(ctx, repo, nil, "config", "user.name"); name != "" {
		if email, _ := git(ctx, repo, nil, "config", "user.email"); email != "" {
			return nil
		}
	}
	return []string{"GIT_AUTHOR_NAME=AgentBox", "GIT_AUTHOR_EMAIL=agentbox@localhost",
		"GIT_COMMITTER_NAME=AgentBox", "GIT_COMMITTER_EMAIL=agentbox@localhost"}
}

func git(ctx context.Context, repo string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

var githubRemote = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?github\.com/|ssh://git@github\.com/|git@github\.com:)([^/]+)/([^/]+?)(?:\.git)?/?$`)

// GitHubBlobBase is where Branch's files are read from on GitHub, for a
// remote's URL: https://github.com/<owner>/<repo>/blob/agentbox-media.
func GitHubBlobBase(remoteURL string) (string, error) {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(remoteURL))
	if m == nil {
		return "", fmt.Errorf("%s isn't a GitHub repository: media can only be published to one", remoteURL)
	}
	return "https://github.com/" + m[1] + "/" + m[2] + "/blob/" + Branch, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug is a file name for an item, from its name, or its id when the name
// has nothing usable in it.
func slug(name, id string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	if s == "" {
		s = strings.ToLower(id)
	}
	return s
}

func imageExt(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	return ".png"
}

// mdText keeps a name from breaking the Markdown it's put in.
func mdText(s string) string {
	return strings.NewReplacer("[", "(", "]", ")", "*", "", "\n", " ").Replace(s)
}

func size(p string) int64 {
	info, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return info.Size()
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
