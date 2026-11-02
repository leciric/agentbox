// Package worktreefs reads a worktree for the app's file tree and viewer: one
// folder's entries at a time, one file at a time, and what git says changed.
// Every path is relative to the worktree's root and checked to stay inside
// it, symlinks included, and the .git folder is never shown or read.
//
// It is modelled on T3Code's workspace (apps/server/src/workspace/
// WorkspaceEntries.ts and WorkspaceFileSystem.ts): a folder is read from the
// disk and git is asked which of its entries are ignored, rather than parsing
// .gitignore, and a file read is capped and refused when it is binary.
package worktreefs

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"agentbox/internal/api"
)

const (
	// EntriesLimit caps one folder's listing.
	EntriesLimit = 5000
	// TextLimit is how much of a text file is sent unless asked for all of it.
	TextLimit = 1 << 20
	// FullLimit is the most a text file sends even when asked for all of it.
	FullLimit = 16 << 20
	// ImageLimit is the largest image sent for preview.
	ImageLimit = 10 << 20
	// PDFLimit is the largest PDF sent for preview.
	PDFLimit = 32 << 20
	// ChangesLimit caps a status's paths.
	ChangesLimit = 20000
	// sniff is how much of a file is looked at to tell binary from text, as git does.
	sniff = 8000
)

// ErrOutside is a path that leaves the worktree, or names its .git.
var ErrOutside = errors.New("that path is outside the worktree")

// Rel turns what the app asked for into a clean path relative to root: "" for
// the root itself. An absolute path is accepted when it is inside root, as
// the paths in tool calls are. It refuses "..", NUL and .git, but doesn't
// look at the disk: Resolve does.
func Rel(root, p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", ErrOutside
	}
	p = filepath.ToSlash(p)
	if path.IsAbs(p) {
		r := strings.TrimRight(filepath.ToSlash(filepath.Clean(root)), "/")
		if p != r && !strings.HasPrefix(p, r+"/") {
			return "", ErrOutside
		}
		p = strings.TrimPrefix(p[len(r):], "/")
	}
	var parts []string
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", ErrOutside
		}
		parts = append(parts, part)
	}
	if len(parts) > 0 && parts[0] == ".git" {
		return "", ErrOutside
	}
	return strings.Join(parts, "/"), nil
}

// Resolve is Rel, then the path on disk with its symlinks followed, which must
// still be inside root.
func Resolve(root, p string) (rel, abs string, err error) {
	rel, err = Rel(root, p)
	if err != nil {
		return "", "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	abs, err = filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", err
	}
	if !inside(realRoot, abs) {
		return "", "", ErrOutside
	}
	return rel, abs, nil
}

func inside(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// List reads one folder. Ignored entries are left out unless withIgnored.
func List(root, dir string, withIgnored bool) (api.FileList, error) {
	rel, abs, err := Resolve(root, dir)
	if err != nil {
		return api.FileList{}, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return api.FileList{}, err
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	out := api.FileList{Path: rel, Entries: []api.FileEntry{}}
	var entries []api.FileEntry
	for _, de := range des {
		name := de.Name()
		if rel == "" && name == ".git" {
			continue
		}
		e := api.FileEntry{Name: name, Path: path.Join(rel, name)}
		mode := de.Type()
		if mode&fs.ModeSymlink != 0 {
			e.Symlink = true
			// A link to a folder inside the worktree opens like one; any other
			// link is a file, which Read refuses when it leads outside.
			if target, err := filepath.EvalSymlinks(filepath.Join(abs, name)); err == nil && inside(realRoot, target) {
				if st, err := os.Stat(target); err == nil {
					e.Dir = st.IsDir()
					if !e.Dir {
						e.Size = st.Size()
					}
				}
			}
		} else if de.IsDir() {
			e.Dir = true
		} else if mode.IsRegular() {
			if info, err := de.Info(); err == nil {
				e.Size = info.Size()
			}
		} else {
			continue // sockets, devices, pipes
		}
		entries = append(entries, e)
	}
	ignored := ignoredOf(root, entries)
	for _, e := range entries {
		e.Ignored = ignored[e.Path]
		if e.Ignored && !withIgnored {
			continue
		}
		out.Entries = append(out.Entries, e)
	}
	sortAndCap(&out)
	return out, nil
}

// sortAndCap puts a listing's folders first, each group by name, and cuts it
// at EntriesLimit.
func sortAndCap(out *api.FileList) {
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name) || strings.EqualFold(a.Name, b.Name) && a.Name < b.Name
	})
	if len(out.Entries) > EntriesLimit {
		out.Entries, out.Truncated = out.Entries[:EntriesLimit], true
	}
}

// ignoredOf asks git which entries its ignore rules leave out, all at once.
// Outside a repository, or when git fails, nothing is.
func ignoredOf(root string, entries []api.FileEntry) map[string]bool {
	if len(entries) == 0 {
		return nil
	}
	var in bytes.Buffer
	for _, e := range entries {
		in.WriteString(e.Path)
		if e.Dir {
			in.WriteByte('/') // so "build/" patterns match it
		}
		in.WriteByte(0)
	}
	// check-ignore exits 1 when none is ignored.
	out, err := git(root, &in, "check-ignore", "-z", "--stdin")
	if err != nil && out == "" {
		return nil
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ignored[strings.TrimSuffix(p, "/")] = true
		}
	}
	return ignored
}

// Read reads one file for the viewer. full asks for up to FullLimit of a text
// file instead of TextLimit.
func Read(root, p string, full bool) (api.FileContent, error) {
	rel, abs, err := Resolve(root, p)
	if err != nil {
		return api.FileContent{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return api.FileContent{}, err
	}
	if st.IsDir() {
		return api.FileContent{}, fmt.Errorf("%s is a folder", rel)
	}
	if !st.Mode().IsRegular() {
		return api.FileContent{}, fmt.Errorf("%s is not a regular file", rel)
	}
	return content(rel, st.Size(), full, func(limit int64) ([]byte, error) {
		f, err := os.Open(abs)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return io.ReadAll(io.LimitReader(f, limit))
	})
}

// content is a file of size bytes for the viewer, by Read's rules: an image
// whole unless larger than ImageLimit, a PDF unless larger than PDFLimit,
// other files refused when binary (and marked when an SQLite database) and cut
// at TextLimit (FullLimit when full) on a line's end. read reads up to limit
// bytes of it.
func content(rel string, size int64, full bool, read func(limit int64) ([]byte, error)) (api.FileContent, error) {
	out := api.FileContent{Path: rel, Size: size}
	if strings.EqualFold(path.Ext(rel), ".pdf") {
		out.PDF = true
		if size > PDFLimit {
			out.TooLarge = true
			return out, nil
		}
		data, err := read(PDFLimit)
		if err != nil {
			return out, err
		}
		out.Data = base64.StdEncoding.EncodeToString(data)
		return out, nil
	}
	if mime := imageType(rel); mime != "" {
		out.Image = mime
		if size > ImageLimit {
			out.TooLarge = true
			return out, nil
		}
		data, err := read(ImageLimit)
		if err != nil {
			return out, err
		}
		out.Data = base64.StdEncoding.EncodeToString(data)
		if mime == "image/svg+xml" {
			out.Text = string(data)
		}
		return out, nil
	}
	limit := int64(TextLimit)
	if full {
		limit = FullLimit
	}
	data, err := read(limit)
	if err != nil {
		return out, err
	}
	if IsBinary(data) {
		out.Binary = true
		out.SQLite = bytes.HasPrefix(data, sqliteHeader)
		return out, nil
	}
	if int64(len(data)) < size {
		out.Truncated = true
		// Not in the middle of a character, nor of a line.
		if i := bytes.LastIndexByte(data, '\n'); i > 0 {
			data = data[:i+1]
		}
		for i := 0; i < utf8.UTFMax-1 && len(data) > 0; i++ {
			if r, size := utf8.DecodeLastRune(data); r != utf8.RuneError || size > 1 {
				break
			}
			data = data[:len(data)-1]
		}
	}
	out.Text = string(data)
	return out, nil
}

// IsBinary is git's test: a NUL byte in the first 8000.
func IsBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), sniff)], 0) >= 0
}

var images = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".svg": "image/svg+xml", ".bmp": "image/bmp", ".ico": "image/x-icon", ".avif": "image/avif",
}

func imageType(p string) string {
	return images[strings.ToLower(path.Ext(p))]
}

// BaseText is what the file was at base, or missing when it wasn't there.
func BaseText(root, base, rel string) (text string, missing bool, err error) {
	out, err := git(root, nil, "cat-file", "-s", base+":"+rel)
	if err != nil {
		return "", true, nil
	}
	if n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64); n > FullLimit {
		return "", false, errors.New("the base version is too large to compare")
	}
	out, err = git(root, nil, "show", base+":"+rel)
	if err != nil {
		return "", false, err
	}
	if IsBinary([]byte(out)) {
		return "", false, errors.New("the base version is binary")
	}
	return out, false, nil
}

// Base is the commit a worktree's changes are measured against: where HEAD
// forked from branch, or HEAD itself when there is no branch or they share
// nothing. It says which.
func Base(root, branch string) (commit, name string) {
	if branch != "" {
		if out, err := git(root, nil, "merge-base", "HEAD", branch); err == nil {
			if c := strings.TrimSpace(out); c != "" {
				return c, branch
			}
		}
	}
	if out, err := git(root, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		return strings.TrimSpace(out), "HEAD"
	}
	return "", ""
}

// Status is what changed in the worktree since base (a commit; "" for a
// repository with none): every tracked file that differs, committed or not,
// and the untracked ones git doesn't ignore, a wholly untracked folder as one
// entry ending in '/'. It is two git commands, both of which read the index's
// cached stat data rather than every file.
func Status(root, base string) (map[string]string, bool, error) {
	changes := map[string]string{}
	truncated := false
	add := func(p, status string) {
		if len(changes) >= ChangesLimit {
			truncated = true
			return
		}
		changes[p] = status
	}
	if base != "" {
		out, err := git(root, nil, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", base, "--")
		if err != nil {
			return nil, false, err
		}
		fields := strings.Split(out, "\x00")
		for i := 0; i+1 < len(fields); i += 2 {
			switch fields[i] {
			case "A":
				add(fields[i+1], api.FileAdded)
			case "D":
				add(fields[i+1], api.FileDeleted)
			case "":
			default:
				add(fields[i+1], api.FileModified)
			}
		}
	}
	out, err := git(root, nil, "ls-files", "-z", "--others", "--exclude-standard", "--directory", "--no-empty-directory")
	if err != nil {
		return nil, false, err
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			add(p, api.FileUntracked)
		}
	}
	return changes, truncated, nil
}

func git(dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
