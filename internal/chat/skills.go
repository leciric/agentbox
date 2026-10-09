package chat

import (
	"context"
	"strings"
	"unicode"

	"agentbox/internal/state"
)

// Skill mentions, after T3 Code (ClaudeSkillDispatch.ts, CursorSkills.ts).
// Picking a skill in the composer inserts `$name`, whatever the tool, and the
// prompt is rewritten here into what that tool really runs:
//
//   - Codex parses `$name` itself, so it goes as it is.
//   - Claude Code's only user-side invocation is a text block whose first
//     character is `/`: `/name args` expands into the SKILL.md, checked on
//     the message's last text block, one skill per message. So the last
//     mention becomes a block of its own, `/name` and the text after it, and
//     earlier ones become `/name` inline, which the model still follows with
//     its Skill tool.
//   - Cursor invokes skills with `/name` anywhere.
//   - OpenCode has no user-side invocation: its model loads a skill with its
//     skill tool when told to, so the prompt says which.
//
// A `$word` that isn't an installed skill's name stays as it is: `$HOME` in
// prose must not become a command.

// skillMention is one `$name` in a prompt, by byte offsets: start at the `$`.
type skillMention struct {
	name       string
	start, end int
}

// skillMentions finds the `$name` tokens of known skills: a `$` at the start or
// after whitespace, then a name running to whitespace or the end.
func skillMentions(text string, known map[string]bool) []skillMention {
	var out []skillMention
	for i := 0; i < len(text); i++ {
		if text[i] != '$' || (i > 0 && !unicode.IsSpace(rune(text[i-1]))) {
			continue
		}
		j := i + 1
		for j < len(text) && isNameByte(text[j]) {
			j++
		}
		if j == i+1 || (j < len(text) && !unicode.IsSpace(rune(text[j]))) {
			continue
		}
		if known[text[i+1:j]] {
			out = append(out, skillMention{name: text[i+1 : j], start: i, end: j})
		}
		i = j - 1
	}
	return out
}

func isNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == ':'
}

// skillPrompt rewrites a prompt's skill mentions for tool, and returns it as
// the text blocks it goes in: one, or for Claude Code two when a mention isn't
// at the very start.
func skillPrompt(tool, text string, known map[string]bool) []string {
	mentions := skillMentions(text, known)
	if len(mentions) == 0 {
		return []string{text}
	}
	slashes := func(text string, ms []skillMention) string {
		for i := len(ms) - 1; i >= 0; i-- {
			text = text[:ms[i].start] + "/" + ms[i].name + text[ms[i].end:]
		}
		return text
	}
	switch tool {
	case "claude":
		last := mentions[len(mentions)-1]
		leading := strings.TrimRight(slashes(text[:last.start], mentions[:len(mentions)-1]), " \t\r\n")
		command := strings.TrimRight("/"+last.name+text[last.end:], " \t\r\n")
		if leading == "" {
			return []string{command}
		}
		return []string{leading, command}
	case "cursor":
		return []string{slashes(text, mentions)}
	case "opencode":
		var names []string
		seen := map[string]bool{}
		for _, m := range mentions {
			if !seen[m.name] {
				seen[m.name] = true
				names = append(names, `"`+m.name+`"`)
			}
		}
		word, it := "skill", "it"
		if len(names) > 1 {
			word, it = "skills", "them"
		}
		return []string{text + "\n\n[Use the " + strings.Join(names, ", ") + " " + word + " for this: load " + it + " with your skill tool first.]"}
	}
	return []string{text}
}

// skillNames is the skills installed for a chat's agent, by name: what a
// mention may name. A store that can't be read leaves mentions as typed.
func (m *Manager) skillNames(a state.Agent) map[string]bool {
	project := a.Project
	if a.IsHome() {
		project = ""
	}
	all, err := m.Store.Skills(context.Background())
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, sk := range all {
		if sk.EnabledFor(project) {
			out[sk.Name] = true
		}
	}
	return out
}
