// Package share writes what an agent did as one HTML page anyone can open:
// the task, its timeline with the screenshots in it, the conversation cut
// down to what was said to and by the agent, the files it changed and its
// pull request. The page is self-contained, with its pictures as data URLs,
// its styles and its few lines of script inline, and nothing it loads from
// the network, so it opens from a file, an email or a chat as it is.
//
// It is pure: the run goes in, already read by the daemon (agentshare.go),
// and every string of it passes through the Redactor before it is written,
// so a page leaves the machine without the agent's secrets, the values in
// its environment or the user's home.
package share

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/redact"
	"agentbox/internal/report"
)

// Run is what a page shows.
type Run struct {
	Project string
	Agent   string
	Title   string // what the user calls the agent; may be empty
	AI      string
	Branch  string
	Task    string
	// Timeline is the agent's steps, with their media (internal/timeline).
	Timeline api.Timeline
	Messages []Message
	Files    []File
	// FilesUncounted is true when Files are only the paths its edits named,
	// with no counts: its worktree is gone.
	FilesUncounted bool
	PR             *api.PullRequest
	Exported       time.Time
}

// Message is one thing said in the agent's chat: by the user, or by the
// agent at the end of its turn.
type Message struct {
	From string // user or agent
	Text string
	At   time.Time
}

// File is one file the agent changed.
type File struct {
	Path           string
	Added, Removed int
	Binary         bool
}

// How much of the chat a page keeps.
const (
	maxMessages     = 40
	maxMessageRunes = 1800
)

// Messages is the chat's items cut down to its messages: each one the user
// sent, and the agent's last words in the turn it started, which its brief
// asks it to make a summary. The first message, the task, is left out: a
// page shows it on its own. What AgentBox wrote for the AI tool alone
// (hidden items, turns it woke for) is left out too.
func Messages(items []api.ChatItem) []Message {
	var out []Message
	last := map[string]int{} // turn → index in out of its agent message
	first := true
	for _, it := range items {
		if it.Hidden || it.Parent != "" {
			continue
		}
		text := strings.TrimSpace(it.Text)
		switch it.Kind {
		case "user", "aside":
			if it.Woken || text == "" {
				continue
			}
			if first {
				first = false
				continue
			}
			out = append(out, Message{From: "user", Text: clip(text), At: it.CreatedAt})
		case "assistant":
			if text == "" {
				continue
			}
			first = false
			m := Message{From: "agent", Text: clip(text), At: it.CreatedAt}
			if i, ok := last[it.Turn]; ok && it.Turn != "" {
				out[i] = m
				continue
			}
			last[it.Turn] = len(out)
			out = append(out, m)
		}
	}
	if len(out) > maxMessages {
		out = out[len(out)-maxMessages:]
	}
	return out
}

// Task is the first message the agent was sent.
func Task(items []api.ChatItem) string {
	for _, it := range items {
		if (it.Kind == "user" || it.Kind == "aside") && !it.Hidden && !it.Woken && it.Parent == "" {
			if text := strings.TrimSpace(it.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxMessageRunes {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:maxMessageRunes])) + "…"
}

// Redactor takes out of a page's text what mustn't leave the machine.
type Redactor struct {
	values []string
	roots  []string
	homes  report.Redactor
}

// envLine is a line that is only an environment variable and its value, as
// env, printenv and export print them: the value goes, the name stays.
var envLine = regexp.MustCompile(`(?m)^(\s*(?:export\s+|declare -x\s+)?[A-Za-z_][A-Za-z0-9_]*=)("[^"\n]*"|'[^'\n]*'|[^\s'"]+)[ \t]*$`)

// NewRedactor removes values, the agent's secrets as its environment has
// them, wherever they appear; shows paths under roots, its worktree, as
// relative ones; and does what report.Redactor does for homes (they become
// ~ or /home/[user]), secrets of a known shape or name, and emails.
func NewRedactor(values, roots []string, homes ...string) Redactor {
	r := Redactor{homes: report.NewRedactor(homes...)}
	for _, v := range values {
		// A value this short would take out ordinary words with it.
		if len(strings.TrimSpace(v)) >= 6 {
			r.values = append(r.values, v)
		}
	}
	// The longest first, so a value inside another goes with it.
	slices.SortFunc(r.values, func(a, b string) int { return len(b) - len(a) })
	for _, root := range roots {
		if root = strings.TrimRight(root, "/"); len(root) > 1 {
			r.roots = append(r.roots, root)
		}
	}
	slices.SortFunc(r.roots, func(a, b string) int { return len(b) - len(a) })
	return r
}

// Redact is s with every secret, environment value and home replaced.
func (r Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, redact.Placeholder)
	}
	for _, root := range r.roots {
		s = strings.ReplaceAll(s, root+"/", "")
		s = replaceWhole(s, root, ".")
	}
	s = envLine.ReplaceAllStringFunc(s, func(m string) string {
		sub := envLine.FindStringSubmatch(m)
		if strings.HasPrefix(sub[2], "[") {
			return m
		}
		return sub[1] + redact.Placeholder
	})
	return r.homes.Redact(s)
}

// replaceWhole replaces old in s where it isn't the start of a longer name.
func replaceWhole(s, old, with string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, old)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(old)
		b.WriteString(s[:i])
		if end < len(s) && (s[end] == '-' || s[end] == '_' || s[end] == '.' || isWord(s[end])) {
			b.WriteString(old)
		} else {
			b.WriteString(with)
		}
		s = s[end:]
	}
}

func isWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Redacted is run with every string it shows passed through r.
func (r Redactor) Redacted(run Run) Run {
	run.Title, run.Task, run.Branch = r.Redact(run.Title), r.Redact(run.Task), r.Redact(run.Branch)
	steps := make([]api.TimelineStep, len(run.Timeline.Steps))
	for i, s := range run.Timeline.Steps {
		s.Title = r.Redact(s.Title)
		s.Lines = r.all(s.Lines)
		s.Paths = r.all(s.Paths)
		media := make([]api.MediaItem, len(s.Media))
		for j, m := range s.Media {
			m.Name, m.Text, m.Path, m.Meta.URL = r.Redact(m.Name), r.Redact(m.Text), "", r.Redact(m.Meta.URL)
			media[j] = m
		}
		s.Media = media
		pages := make([]api.TimelinePage, len(s.Pages))
		for j, p := range s.Pages {
			p.Title = r.Redact(p.Title)
			pages[j] = p
		}
		s.Pages = pages
		steps[i] = s
	}
	run.Timeline.Steps = steps
	messages := make([]Message, len(run.Messages))
	for i, m := range run.Messages {
		m.Text = r.Redact(m.Text)
		messages[i] = m
	}
	run.Messages = messages
	files := make([]File, len(run.Files))
	for i, f := range run.Files {
		f.Path = r.Redact(f.Path)
		files[i] = f
	}
	run.Files = files
	if run.PR != nil {
		pr := *run.PR
		pr.Title = r.Redact(pr.Title)
		run.PR = &pr
	}
	return run
}

func (r Redactor) all(lines []string) []string {
	if lines == nil {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = r.Redact(l)
	}
	return out
}

// EditedPaths are the files a timeline's edits named, for a run whose diff
// can no longer be read.
func EditedPaths(tl api.Timeline) []File {
	var out []File
	seen := map[string]bool{}
	for _, s := range tl.Steps {
		if s.Kind != api.TimelineKindEdit {
			continue
		}
		for _, p := range s.Paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, File{Path: p})
			}
		}
	}
	return out
}
