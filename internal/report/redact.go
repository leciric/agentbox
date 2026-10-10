package report

import (
	"regexp"
	"slices"
	"strings"

	"agentbox/internal/redact"
)

// Redaction runs over everything a report carries, the user's message
// included, before the user sees it and again before it's sent: whatever
// part of AgentBox wrote a line, nothing that looks like a secret (the
// patterns are internal/redact's, shared with project memory), an email
// address or a home directory leaves the machine. It errs on the side of
// removing too much, since a report is read by people and a lost token can't
// be taken back. What it can't know is a secret with no shape and no name
// beside it, which is why the user sees the report before it goes.

// The placeholders a redacted value becomes, so a reader can tell what was
// there. Secrets become internal/redact's.
const (
	redactedMail = "[email]"
	redactedUser = "[user]"
)

var (
	email = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)

	// Home directories of whoever: the user's own are known (Redactor.Homes)
	// and become ~, and these catch the rest, and paths from other machines
	// in pasted output.
	homeDirs = []struct {
		re  *regexp.Regexp
		out string
	}{
		{regexp.MustCompile(`/home/[^/\s"':]+`), "/home/" + redactedUser},
		{regexp.MustCompile(`/Users/[^/\s"':]+`), "/Users/" + redactedUser},
		{regexp.MustCompile(`(?i)\b([A-Z]:)\\{1,2}Users\\{1,2}[^\\\s"':]+`), `$1\Users\` + redactedUser},
	}
)

// notAnEmail are the addresses that aren't anybody's: git's ssh user, and
// the noreply addresses commits are written with.
var notAnEmail = []string{"git@", "noreply@", "no-reply@"}

// Redactor removes secrets, email addresses and home directories from text.
type Redactor struct {
	// Homes are home directories to show as ~: the daemon's own, and the
	// host's when it runs in a VM. The longest is replaced first, so a home
	// inside another (a VM's /home/lint.linux beside /home/lint) stays whole.
	Homes []string
}

// NewRedactor is a Redactor for homes, ignoring the empty ones and "/".
func NewRedactor(homes ...string) Redactor {
	var r Redactor
	for _, h := range homes {
		h = strings.TrimRight(h, `/\`)
		if len(h) > 1 && !slices.Contains(r.Homes, h) {
			r.Homes = append(r.Homes, h)
		}
	}
	slices.SortFunc(r.Homes, func(a, b string) int { return len(b) - len(a) })
	return r
}

// Redact is s with every secret, email address and home directory replaced.
// Running it twice changes nothing more.
func (r Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	s = redact.Secrets(s)
	s = email.ReplaceAllStringFunc(s, func(m string) string {
		lower := strings.ToLower(m)
		for _, keep := range notAnEmail {
			if strings.HasPrefix(lower, keep) {
				return m
			}
		}
		if strings.HasSuffix(lower, "@users.noreply.github.com") {
			return m
		}
		return redactedMail
	})
	for _, h := range r.Homes {
		s = replaceHome(s, h)
	}
	for _, h := range homeDirs {
		s = h.re.ReplaceAllString(s, h.out)
	}
	return s
}

// replaceHome turns home into ~ where it's a whole path: /home/lint and
// /home/lint/x, not /home/linter.
func replaceHome(s, home string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, home)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(home)
		if end < len(s) && !isPathEnd(s[end]) {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		b.WriteString(s[:i])
		b.WriteString("~")
		s = s[end:]
	}
}

func isPathEnd(c byte) bool {
	return (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-'
}
