// Package skills reads, checks and packs Agent Skills: a folder holding a
// SKILL.md (YAML frontmatter with a name and a description, then the
// instructions) and whatever files it points to.
//
// The daemon stores skills whole in state.db and installs the ones a project
// has on into every agent's and lead's machine, where its AI tool reads them:
// ~/.claude/skills for Claude Code (and OpenCode, and Cursor), ~/.agents/skills
// for Codex, Cursor and OpenCode (InstallDirs). Skills come from the editor in
// the app, a folder or a git repository (Collect, Clone), or the places the
// user's own AI tools keep theirs (Discover).
package skills

import (
	"bufio"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"agentbox/internal/state"
)

// Limits on one skill, well above any real one, so a folder picked by mistake
// (a home directory, a repository with its build output) fails here rather
// than landing in every agent.
const (
	MaxFiles     = 200
	MaxFileBytes = 2 << 20
	MaxBytes     = 8 << 20
	// MaxDescription bounds a description. The Agent Skills spec says 1024,
	// and Anthropic's own skills go past it, so a longer one is let through.
	MaxDescription = 4096
	// File is the one file every skill has.
	File = "SKILL.md"
)

// InstallDirs are where skills go inside an agent's HOME, and a lead's. Claude
// Code reads only the first; Codex the second; OpenCode and Cursor read both,
// and keep one of a name.
var InstallDirs = []string{".claude/skills", ".agents/skills"}

// name is the Agent Skills spec's: lowercase letters, digits and hyphens,
// neither starting nor ending with a hyphen. It is a folder name in every
// agent and a word typed after $ or / in the composer, so nothing else.
var name = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidateName checks a skill's name.
func ValidateName(n string) error {
	switch {
	case n == "":
		return errors.New("a skill needs a name, like review-pr")
	case !name.MatchString(n) || strings.Contains(n, "--"):
		return fmt.Errorf("invalid skill name %q: use lowercase letters, digits and single hyphens, at most 64 characters (like review-pr)", n)
	}
	return nil
}

// Slug turns any folder or plugin name into a valid skill name, or "" when
// nothing of it survives: My_Skill becomes my-skill.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 64 {
		out = strings.TrimRight(out[:64], "-")
	}
	return out
}

// Meta is what a SKILL.md's frontmatter says.
type Meta struct {
	Name        string
	Description string
	// UserInvocable is false when the skill says `user-invocable: false`: the
	// model may use it, but the composer doesn't offer it.
	UserInvocable bool
	// Fields are every top-level key it has, as text, for the preview.
	Fields map[string]string
}

// Parse reads a SKILL.md: its frontmatter and the instructions after it. A
// file without frontmatter is all instructions. Only what skills use is
// understood — top-level `key: value` pairs, quoted or not, and `>`/`|`
// blocks — so a nested map is kept as its raw text rather than refused.
func Parse(content string) (Meta, string, error) {
	meta := Meta{UserInvocable: true, Fields: map[string]string{}}
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return meta, content, nil
	}
	rest := content[strings.Index(content, "\n")+1:]
	var front []string
	body := ""
	closed := false
	sc := bufio.NewScanner(strings.NewReader(rest))
	sc.Buffer(make([]byte, 0, 64<<10), MaxFileBytes)
	consumed := 0
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		consumed += len(sc.Text()) + 1
		if line == "---" {
			closed = true
			break
		}
		front = append(front, line)
	}
	if !closed {
		return meta, "", errors.New("SKILL.md's frontmatter has no closing ---")
	}
	if consumed < len(rest) {
		body = rest[consumed:]
	}
	for i := 0; i < len(front); i++ {
		line := front[i]
		if line == "" || strings.HasPrefix(line, "#") || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// What's indented below a key belongs to it: a block scalar's lines,
		// or a nested map kept as text.
		var block []string
		for i+1 < len(front) && (front[i+1] == "" || front[i+1][0] == ' ' || front[i+1][0] == '\t') {
			i++
			block = append(block, strings.TrimSpace(front[i]))
		}
		switch {
		case strings.HasPrefix(value, ">"):
			value = strings.TrimSpace(strings.Join(block, " "))
		case strings.HasPrefix(value, "|"):
			value = strings.TrimSpace(strings.Join(block, "\n"))
		case value == "" && len(block) > 0:
			value = strings.TrimSpace(strings.Join(block, "\n"))
		default:
			value = unquote(value)
			if len(block) > 0 { // a plain scalar folded over several lines
				value = strings.TrimSpace(value + " " + strings.Join(block, " "))
			}
		}
		meta.Fields[key] = value
	}
	meta.Name = meta.Fields["name"]
	meta.Description = meta.Fields["description"]
	if v, ok := meta.Fields["user-invocable"]; ok && isFalse(v) {
		meta.UserInvocable = false
	}
	return meta, body, nil
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"') {
		return strings.ReplaceAll(strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`), `\n`, "\n")
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

func isFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "no", "off", "0":
		return true
	}
	return false
}

// WithName returns content with its frontmatter's name set to n, adding the
// frontmatter when there is none, so that the folder an agent gets and the
// name inside always agree (the tools that check, Codex among them, skip a
// skill whose two names differ).
func WithName(content, n string) string {
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "---\nname: " + n + "\ndescription: \n---\n\n" + content
	}
	lines := strings.SplitAfter(content, "\n")
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r\n")
		if line == "---" {
			// No name in it: add one at the top.
			return lines[0] + "name: " + n + "\n" + strings.Join(lines[1:], "")
		}
		if strings.HasPrefix(line, "name:") {
			lines[i] = "name: " + n + "\n"
			return strings.Join(lines, "")
		}
	}
	return content
}

// Template is a new skill's SKILL.md.
func Template(n, description string) string {
	if description == "" {
		description = "What this skill does, and when to use it."
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n\nSay here, step by step, what the agent should do when it uses this skill.\n", n, description, n)
}

// Check validates one skill's files: a SKILL.md, a description, clean paths,
// and the limits above. It returns what its SKILL.md says.
func Check(n string, files []state.SkillFile) (Meta, error) {
	if err := ValidateName(n); err != nil {
		return Meta{}, err
	}
	if len(files) > MaxFiles {
		return Meta{}, fmt.Errorf("skill %s has %d files, more than the %d a skill may have", n, len(files), MaxFiles)
	}
	var total int
	var skill *state.SkillFile
	seen := map[string]bool{}
	for i, f := range files {
		if err := checkPath(f.Path); err != nil {
			return Meta{}, fmt.Errorf("skill %s: %w", n, err)
		}
		if seen[f.Path] {
			return Meta{}, fmt.Errorf("skill %s has %s twice", n, f.Path)
		}
		seen[f.Path] = true
		if len(f.Content) > MaxFileBytes {
			return Meta{}, fmt.Errorf("skill %s: %s is larger than %d MB", n, f.Path, MaxFileBytes>>20)
		}
		total += len(f.Content)
		if f.Path == File {
			skill = &files[i]
		}
	}
	if total > MaxBytes {
		return Meta{}, fmt.Errorf("skill %s holds %d MB, more than the %d MB a skill may", n, total>>20, MaxBytes>>20)
	}
	if skill == nil {
		return Meta{}, fmt.Errorf("skill %s has no SKILL.md", n)
	}
	meta, _, err := Parse(string(skill.Content))
	if err != nil {
		return Meta{}, fmt.Errorf("skill %s: %w", n, err)
	}
	if strings.TrimSpace(meta.Description) == "" {
		return Meta{}, fmt.Errorf("skill %s: its SKILL.md needs a description in its frontmatter, which is how the agent knows when to use it", n)
	}
	if len(meta.Description) > MaxDescription {
		return Meta{}, fmt.Errorf("skill %s: its description is longer than %d characters", n, MaxDescription)
	}
	return meta, nil
}

func checkPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." {
		return fmt.Errorf("invalid file path %q", p)
	}
	return nil
}
