// Package machinesmedia is the central store of the screenshots and
// recordings that `agentbox machines mcp` takes for AI tools running on the
// user's own machine, outside AgentBox's agents, and that `agentbox machines
// serve` shows.
//
// The store is a flat directory, Dir: each item is a media file <id>.<ext>
// beside a sidecar <id>.json holding its Item. The sidecar is written last, by
// rename, so an item exists once its sidecar does and a reader never sees half
// of one; a media file with no sidecar is an item still being written, or the
// leftover of a crash, and is ignored. Anything that can write a file can add
// an item, but Write and Save are the way to do it.
package machinesmedia

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"agentbox/internal/paths"
)

// Kinds of item.
const (
	Screenshot = "screenshot"
	Recording  = "recording"
)

// Tools an item can come from.
const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
	ToolOther  = "other"
)

// Item is an item's sidecar, <id>.json.
type Item struct {
	// ID names the item's two files. NewID makes one that sorts by time.
	ID   string `json:"id"`
	Kind string `json:"kind"` // Screenshot or Recording
	// Created is when it was taken: for a recording, when it started.
	Created time.Time `json:"created"`
	// Worktree is the absolute path of the git worktree (or plain directory)
	// the AI tool was working in.
	Worktree string `json:"worktree,omitempty"`
	// Repo names the repository: its origin's owner/name when it has one,
	// else its main checkout's directory name (Describe). Worktrees of one
	// repository share it.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Tool is the AI tool that took it: ToolClaude, ToolCodex or ToolOther.
	Tool string `json:"tool,omitempty"`
	// Session identifies the tool's session (or conversation), so one
	// session's items can be shown together; free-form.
	Session    string `json:"session,omitempty"`
	Caption    string `json:"caption,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"` // recordings only
	Bytes      int64  `json:"bytes"`                // the media file's size
}

// Dir is the store's directory: <Data>/machines/media.
func Dir(p paths.Paths) string { return filepath.Join(p.Data, "machines", "media") }

// Store is the store in one directory.
type Store struct{ Dir string }

// Open is the store in Dir(p).
func Open(p paths.Paths) Store { return Store{Dir: Dir(p)} }

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidID reports whether id can name an item: letters, digits, '.', '_' and
// '-', so it never reaches outside the directory.
func ValidID(id string) bool { return idPattern.MatchString(id) && !strings.Contains(id, "..") }

// NewID makes an id that sorts by when it was made, with a random suffix so
// two items taken in the same millisecond don't collide:
// 20261004-153012345-1a2b3c.
func NewID(t time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return strings.Replace(t.UTC().Format("20060102-150405.000"), ".", "", 1) + "-" + hex.EncodeToString(b[:])
}

// Write adds an item: its media file, <id><ext>, from r, then its sidecar.
// item.ID is made when empty, item.Created set to now when zero, and
// item.Bytes always set to what was written. ext includes its dot (".png",
// ".mp4").
func (s Store) Write(item Item, ext string, r io.Reader) (Item, error) {
	if item.ID == "" {
		item.ID = NewID(time.Now())
	}
	if !ValidID(item.ID) {
		return Item{}, fmt.Errorf("invalid media id %q", item.ID)
	}
	if !validExt(ext) {
		return Item{}, fmt.Errorf("invalid media extension %q", ext)
	}
	if item.Kind != Screenshot && item.Kind != Recording {
		return Item{}, fmt.Errorf("invalid media kind %q", item.Kind)
	}
	if item.Created.IsZero() {
		item.Created = time.Now()
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Item{}, err
	}
	if _, err := s.mediaFile(item.ID); err == nil {
		return Item{}, fmt.Errorf("media %s already exists", item.ID)
	}
	n, err := writeAtomic(filepath.Join(s.Dir, item.ID+ext), r)
	if err != nil {
		return Item{}, err
	}
	item.Bytes = n
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return Item{}, err
	}
	if _, err := writeAtomic(filepath.Join(s.Dir, item.ID+".json"), strings.NewReader(string(data)+"\n")); err != nil {
		_ = os.Remove(filepath.Join(s.Dir, item.ID+ext))
		return Item{}, err
	}
	return item, nil
}

// Save adds an item from a file already on disk, copying it: the file's
// extension is the item's.
func (s Store) Save(item Item, path string) (Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = f.Close() }()
	return s.Write(item, strings.ToLower(filepath.Ext(path)), f)
}

func validExt(ext string) bool {
	return len(ext) >= 2 && len(ext) <= 8 && ext[0] == '.' && ext != ".json" && ValidID(ext[1:])
}

// writeAtomic writes path through a temporary file beside it, renamed into
// place once complete.
func writeAtomic(path string, r io.Reader) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	return n, nil
}

// Entry is an item as List finds it: its sidecar, and where its media file
// is.
type Entry struct {
	Item
	Path string `json:"path"`
	// Modified is the sidecar's modification time, which a caller keeping an
	// index can compare to tell a changed item from an unchanged one.
	Modified time.Time `json:"-"`
}

// List reads every item, newest first. A sidecar that can't be read, or
// whose media file is missing, is skipped; a missing directory is an empty
// store.
func (s Store) List() ([]Entry, error) {
	return s.ListCached(nil)
}

// ListCached is List, reusing an earlier list's entry for any sidecar not
// modified since, so a caller watching thousands of items rereads only what
// changed.
func (s Store) ListCached(prev map[string]Entry) ([]Entry, error) {
	des, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	media := map[string]string{} // id -> media file name
	var sidecars []fs.DirEntry
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		ext := filepath.Ext(name)
		id := strings.TrimSuffix(name, ext)
		if ext == ".json" {
			sidecars = append(sidecars, de)
		} else if ValidID(id) {
			media[id] = name
		}
	}
	out := make([]Entry, 0, len(sidecars))
	for _, de := range sidecars {
		id := strings.TrimSuffix(de.Name(), ".json")
		file, ok := media[id]
		if !ok || !ValidID(id) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		if e, ok := prev[id]; ok && e.Modified.Equal(info.ModTime()) && filepath.Base(e.Path) == file {
			out = append(out, e)
			continue
		}
		item, err := s.readSidecar(id)
		if err != nil {
			continue
		}
		out = append(out, Entry{Item: item, Path: filepath.Join(s.Dir, file), Modified: info.ModTime()})
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out, nil
}

func (s Store) readSidecar(id string) (Item, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, id+".json"))
	if err != nil {
		return Item{}, err
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil {
		return Item{}, fmt.Errorf("media %s: %w", id, err)
	}
	item.ID = id // the file's name wins over what's in it
	return item, nil
}

// Get reads one item.
func (s Store) Get(id string) (Entry, error) {
	if !ValidID(id) {
		return Entry{}, fs.ErrNotExist
	}
	file, err := s.mediaFile(id)
	if err != nil {
		return Entry{}, err
	}
	item, err := s.readSidecar(id)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Item: item, Path: file}, nil
}

// mediaFile finds an item's media file, whatever its extension.
func (s Store) mediaFile(id string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(s.Dir, globEscape(id)+".*"))
	for _, m := range matches {
		if filepath.Ext(m) != ".json" && strings.TrimSuffix(filepath.Base(m), filepath.Ext(m)) == id {
			return m, nil
		}
	}
	return "", fs.ErrNotExist
}

func globEscape(s string) string {
	return strings.NewReplacer(`*`, `\*`, `?`, `\?`, `[`, `\[`, `\`, `\\`).Replace(s)
}

// Delete removes an item: its sidecar first, so it is gone at once, then its
// media file.
func (s Store) Delete(id string) error {
	if !ValidID(id) {
		return fs.ErrNotExist
	}
	file, ferr := s.mediaFile(id)
	if err := os.Remove(filepath.Join(s.Dir, id+".json")); err != nil {
		return err
	}
	if ferr == nil {
		return os.Remove(file)
	}
	return nil
}

// Describe fills in where an AI tool working in dir is: the worktree's top
// level, its branch, and the repository's name (Item.Repo). It leaves
// whatever git can't tell empty, with dir itself as the worktree outside a
// repository.
func Describe(ctx context.Context, dir string) (worktree, repo, branch string) {
	git := func(args ...string) string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	worktree = git("rev-parse", "--show-toplevel")
	if worktree == "" {
		abs, _ := filepath.Abs(dir)
		return abs, "", ""
	}
	branch = git("rev-parse", "--abbrev-ref", "HEAD")
	if branch == "HEAD" {
		branch = git("rev-parse", "--short", "HEAD")
	}
	repo = RepoName(git("remote", "get-url", "origin"))
	if repo == "" {
		common := git("rev-parse", "--path-format=absolute", "--git-common-dir")
		if filepath.Base(common) == ".git" {
			repo = filepath.Base(filepath.Dir(common))
		} else if common != "" {
			repo = strings.TrimSuffix(filepath.Base(common), ".git")
		} else {
			repo = filepath.Base(worktree)
		}
	}
	return worktree, repo, branch
}

// RepoName is owner/name from a git remote URL (https, ssh or scp-like), or
// "" when it has no such path.
func RepoName(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(remote), "/"), ".git")
	if remote == "" || strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, ".") {
		return "" // a local path: Describe names it by its directory
	}
	if i := strings.Index(remote, "://"); i >= 0 {
		remote = remote[i+3:]
		if j := strings.Index(remote, "/"); j >= 0 {
			remote = remote[j+1:]
		} else {
			return ""
		}
	} else if i := strings.Index(remote, ":"); i >= 0 {
		remote = remote[i+1:]
	}
	parts := strings.Split(strings.Trim(remote, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}
