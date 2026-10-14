package chat

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"agentbox/internal/state"
)

// Project mentions: the composer's "@" popup inserts `@<project name>`, and
// the message the AI tool receives gets each one replaced with the name and
// the repository's path on the host, `@saiapp4 (project at /home/me/saiapp4)`.
// Nothing else about the project goes in. The stored chat item keeps what the
// user typed; only the prompt is rewritten.

// projectMentionText rewrites the `@name` tokens of known projects (by slug) in
// text. A mention is an `@` at the start or after whitespace, and the name must
// end the token: `@saiapp4/src` or `@saiapp4x` is a file, not the project.
func projectMentionText(text string, roots map[string]string) string {
	if len(roots) == 0 || !strings.Contains(text, "@") {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '@' || (i > 0 && !isSpaceBefore(text[:i])) {
			b.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) && isProjectByte(text[j]) {
			j++
		}
		name := text[i+1 : j]
		root, ok := roots[name]
		// A trailing "." or "-" is the sentence's, unless a name continues after it.
		for !ok && name != "" && strings.ContainsAny(name[len(name)-1:], ".-_") {
			name = name[:len(name)-1]
			j--
			root, ok = roots[name]
		}
		if !ok || name == "" || !endsToken(text[j:]) {
			b.WriteByte('@')
			i++
			continue
		}
		b.WriteString("@" + name + " (project at " + root + ")")
		i = j
	}
	return b.String()
}

func isSpaceBefore(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(r)
}

func isProjectByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// endsToken is true when rest doesn't continue a path or a word.
func endsToken(rest string) bool {
	if rest == "" {
		return true
	}
	// A sentence's full stop ends it; a dot with more name after it doesn't.
	if rest[0] == '.' && (len(rest) == 1 || !isProjectByte(rest[1]) && rest[1] != '/') {
		return true
	}
	return rest[0] != '/' && rest[0] != '\\' && !isProjectByte(rest[0]) && rest[0] < utf8.RuneSelf
}

// projectRoots is the user's projects by slug, to their repositories' paths. A
// store that can't be read leaves mentions as typed.
func (m *Manager) projectRoots() map[string]string {
	all, err := m.Store.Projects(context.Background())
	if err != nil {
		return nil
	}
	return rootsOf(all)
}

func rootsOf(all []state.Project) map[string]string {
	out := make(map[string]string, len(all))
	for _, p := range all {
		out[p.Name] = p.Root
	}
	return out
}
