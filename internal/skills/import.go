package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"agentbox/internal/state"
)

// Candidate is a skill found somewhere it can be imported from, read but not
// stored: what the import dialog lists and the user picks from.
type Candidate struct {
	// Name is what it would be stored as: its frontmatter's name, else its
	// folder's, made valid (Slug).
	Name        string
	Description string
	// Origin says whose it is: "claude", "claude-plugin", "codex", "agents",
	// "opencode", "cursor", "folder" or "git".
	Origin string
	// Plugin is the Claude Code plugin it comes with, for "claude-plugin".
	Plugin string
	// Dir is the folder it was read from, on this machine; Source is what
	// the stored skill records as where it came from.
	Dir    string
	Source string
	Files  []state.SkillFile
	// Problem is why it can't be imported as it is (no description, too
	// large), shown next to it rather than hiding it.
	Problem string
}

// Size is how many bytes the candidate's files hold.
func (c Candidate) Size() int64 {
	var n int64
	for _, f := range c.Files {
		n += int64(len(f.Content))
	}
	return n
}

// maxDepth is how deep under a folder Collect looks for SKILL.md: deep enough
// for a repository's skills/<name>/SKILL.md or a plugin's
// plugins/<p>/skills/<name>/SKILL.md, not for a home directory.
const maxDepth = 5

// skipDirs are never part of a skill or a place to look for one.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "__pycache__": true, ".DS_Store": true}

// Collect finds every skill in dir: dir itself when it holds a SKILL.md,
// else each folder below it that does, to maxDepth. A skill's own subfolders
// are its files, not more skills.
func Collect(dir, origin, source string) ([]Candidate, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if filepath.Base(dir) == File {
			dir = filepath.Dir(dir)
		} else {
			return nil, fmt.Errorf("%s isn't a folder or a SKILL.md", dir)
		}
	}
	var out []Candidate
	var walk func(d string, depth int) error
	walk = func(d string, depth int) error {
		if _, err := os.Stat(filepath.Join(d, File)); err == nil {
			c := read(d, origin, source)
			out = append(out, c)
			return nil
		}
		if depth >= maxDepth {
			return nil
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return nil // unreadable: nothing to import from it
		}
		for _, e := range entries {
			if skipDirs[e.Name()] || (strings.HasPrefix(e.Name(), ".") && depth > 0 && e.Name() != ".claude" && e.Name() != ".agents") {
				continue
			}
			p := filepath.Join(d, e.Name())
			// A symlink to a folder is followed one step, the way the tools
			// do for linked skill libraries.
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				if err := walk(p, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(dir, 0); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// read reads one skill folder. Whatever is wrong with it goes in Problem.
func read(dir, origin, source string) Candidate {
	c := Candidate{Origin: origin, Dir: dir, Source: source}
	if c.Source == "" {
		c.Source = dir
	}
	var total int
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Only regular files: a symlink inside a skill may point anywhere on
		// this machine, which isn't the skill's to hand out.
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if len(c.Files) >= MaxFiles {
			return fmt.Errorf("it has more than %d files", MaxFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFileBytes {
			return fmt.Errorf("%s is larger than %d MB", rel, MaxFileBytes>>20)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		total += len(content)
		if total > MaxBytes {
			return fmt.Errorf("it holds more than %d MB", MaxBytes>>20)
		}
		c.Files = append(c.Files, state.SkillFile{Path: filepath.ToSlash(rel), Mode: uint32(info.Mode().Perm()), Content: content})
		return nil
	})
	meta := Meta{}
	for _, f := range c.Files {
		if f.Path == File {
			meta, _, _ = Parse(string(f.Content))
		}
	}
	c.Name = Slug(meta.Name)
	if c.Name == "" {
		c.Name = Slug(filepath.Base(dir))
	}
	c.Description = meta.Description
	switch {
	case err != nil:
		c.Problem = err.Error()
		c.Files = nil
	case c.Name == "":
		c.Problem = "it has no name AgentBox can use"
	case strings.TrimSpace(meta.Description) == "":
		c.Problem = "its SKILL.md has no description"
	case len(meta.Description) > MaxDescription:
		c.Problem = fmt.Sprintf("its description is longer than %d characters", MaxDescription)
	}
	return c
}

// Prepare turns a candidate into what is stored: its files with SKILL.md's
// name matching the name it's stored under.
func (c Candidate) Prepare(n string) ([]state.SkillFile, Meta, error) {
	if c.Problem != "" {
		return nil, Meta{}, fmt.Errorf("skill %s can't be imported: %s", c.Name, c.Problem)
	}
	files := slices.Clone(c.Files)
	for i, f := range files {
		if f.Path == File {
			files[i].Content = []byte(WithName(string(f.Content), n))
		}
	}
	meta, err := Check(n, files)
	return files, meta, err
}

// Discover lists the skills the user's own AI tools have, in each home given
// (the VM's and, when it is shared, the host's): Claude Code's own and its
// plugins', Codex's, OpenCode's, Cursor's and the shared ~/.agents/skills.
func Discover(homes []string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(cs []Candidate) {
		for _, c := range cs {
			real, err := filepath.EvalSymlinks(c.Dir)
			if err != nil {
				real = c.Dir
			}
			if seen[real] {
				continue
			}
			seen[real] = true
			out = append(out, c)
		}
	}
	for _, home := range homes {
		if home == "" {
			continue
		}
		for _, place := range []struct{ dir, origin string }{
			{".claude/skills", "claude"},
			{".codex/skills", "codex"},
			{".agents/skills", "agents"},
			{".config/opencode/skills", "opencode"},
			{".config/opencode/skill", "opencode"},
			{".cursor/skills", "cursor"},
		} {
			add(collectChildren(filepath.Join(home, place.dir), place.origin))
		}
		add(pluginSkills(filepath.Join(home, ".claude", "plugins")))
	}
	return out
}

// collectChildren reads each folder of a tool's skills directory, one level
// down: that is all a tool reads there. Dot-folders are the tool's own
// (Codex keeps its bundled skills in .system).
func collectChildren(dir, origin string) []Candidate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if st, err := os.Stat(filepath.Join(p, File)); err != nil || st.IsDir() {
			continue
		}
		out = append(out, read(p, origin, origin+":"+e.Name()))
	}
	return out
}

// pluginSkills reads the skills of the Claude Code plugins installed under
// dir (~/.claude/plugins), from installed_plugins.json's install paths, or
// from its cache folder when that file can't be read.
func pluginSkills(dir string) []Candidate {
	var installs []struct{ plugin, path string }
	if data, err := os.ReadFile(filepath.Join(dir, "installed_plugins.json")); err == nil {
		var v2 struct {
			Plugins map[string]json.RawMessage `json:"plugins"`
		}
		if json.Unmarshal(data, &v2) == nil {
			for id, raw := range v2.Plugins {
				plugin, _, _ := strings.Cut(id, "@")
				var list []struct {
					InstallPath string `json:"installPath"`
				}
				var one struct {
					InstallPath string `json:"installPath"`
				}
				if json.Unmarshal(raw, &list) == nil {
					for _, it := range list {
						installs = append(installs, struct{ plugin, path string }{plugin, it.InstallPath})
					}
				} else if json.Unmarshal(raw, &one) == nil {
					installs = append(installs, struct{ plugin, path string }{plugin, one.InstallPath})
				}
			}
		}
	}
	if len(installs) == 0 {
		// cache/<marketplace>/<plugin>/<version>
		matches, _ := filepath.Glob(filepath.Join(dir, "cache", "*", "*", "*"))
		for _, m := range matches {
			installs = append(installs, struct{ plugin, path string }{filepath.Base(filepath.Dir(m)), m})
		}
	}
	sort.Slice(installs, func(i, j int) bool { return installs[i].path < installs[j].path })
	var out []Candidate
	for _, in := range installs {
		if in.path == "" {
			continue
		}
		for _, c := range collectChildren(filepath.Join(in.path, "skills"), "claude-plugin") {
			c.Plugin = in.plugin
			c.Source = "claude-plugin:" + in.plugin + "/" + filepath.Base(c.Dir)
			out = append(out, c)
		}
	}
	return out
}

// IsGitURL says whether a source is a repository to clone rather than a
// folder: an https or ssh URL, git@host:path, or github.com/owner/repo.
func IsGitURL(source string) bool {
	return strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") ||
		strings.HasPrefix(source, "ssh://") || strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "github.com/")
}

// gitSource splits a git source into what to clone, the branch or tag, and
// the folder in it: GitHub's /tree/<ref>/<path> links, and a #<path> suffix
// on any URL.
func gitSource(source string) (repo, ref, sub string) {
	repo = source
	if r, frag, ok := strings.Cut(repo, "#"); ok {
		repo, sub = r, strings.Trim(frag, "/")
	}
	if strings.HasPrefix(repo, "github.com/") {
		repo = "https://" + repo
	}
	if u, err := url.Parse(repo); err == nil && u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob") {
			ref = parts[3]
			if rest := strings.Join(parts[4:], "/"); rest != "" {
				sub = strings.TrimSuffix(rest, "/"+File)
				if rest == File {
					sub = ""
				}
			}
			parts = parts[:2]
		}
		if len(parts) >= 2 {
			u.Path = "/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
			repo = u.String()
		}
	}
	return repo, ref, sub
}

// Clone fetches a git source's newest commit into a temporary folder and
// returns the folder its skills are in; cleanup removes it all.
func Clone(ctx context.Context, source string) (dir string, cleanup func(), err error) {
	repo, ref, sub := gitSource(source)
	tmp, err := os.MkdirTemp("", "agentbox-skills-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", repo, tmp)
	cmd := exec.CommandContext(ctx, "git", args...)
	// Never stop to ask for a password: a private repository fails instead.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", nil, fmt.Errorf("couldn't clone %s: %s", repo, msg)
	}
	dir = tmp
	if sub != "" {
		dir = filepath.Join(tmp, filepath.FromSlash(sub))
		if rel, err := filepath.Rel(tmp, dir); err != nil || strings.HasPrefix(rel, "..") {
			cleanup()
			return "", nil, fmt.Errorf("invalid folder %q in %s", sub, source)
		}
		if _, err := os.Stat(dir); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("%s has no folder %s", repo, sub)
		}
	}
	return dir, cleanup, nil
}

// Scan finds the skills a source holds: a git URL, a folder or a SKILL.md,
// or, for "", the user's AI tools' own (Discover).
func Scan(ctx context.Context, source string, homes []string) ([]Candidate, error) {
	source = strings.TrimSpace(source)
	switch {
	case source == "":
		return Discover(homes), nil
	case IsGitURL(source):
		dir, cleanup, err := Clone(ctx, source)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		found, err := Collect(dir, "git", source)
		if err != nil {
			return nil, err
		}
		for i := range found {
			// Where in the repository each one was, so the source says
			// enough to fetch it again.
			if rel, err := filepath.Rel(dir, found[i].Dir); err == nil && rel != "." {
				found[i].Source = source + "#" + filepath.ToSlash(rel)
				if strings.Contains(source, "#") || strings.Contains(source, "/tree/") {
					found[i].Source = source
				}
			}
			found[i].Dir = ""
		}
		return found, nil
	default:
		if strings.HasPrefix(source, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				source = filepath.Join(home, source[2:])
			}
		}
		if !filepath.IsAbs(source) {
			return nil, fmt.Errorf("%q isn't a full path or a git URL", source)
		}
		found, err := Collect(source, "folder", "")
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("there's no folder %s on the machine AgentBox runs on", source)
		}
		return found, err
	}
}
