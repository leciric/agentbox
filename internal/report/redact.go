package report

import (
	"regexp"
	"slices"
	"strings"
)

// Redaction runs over everything a report carries, the user's message
// included, before the user sees it and again before it's sent: whatever
// part of AgentBox wrote a line, nothing that looks like a secret, an email
// address or a home directory leaves the machine. It errs on the side of
// removing too much, since a report is read by people and a lost token can't
// be taken back. What it can't know is a secret with no shape and no name
// beside it, which is why the user sees the report before it goes.

// The placeholders a redacted value becomes, so a reader can tell what was
// there.
const (
	redacted     = "[redacted]"
	redactedKey  = "[private key]"
	redactedMail = "[email]"
	redactedUser = "[user]"
)

var (
	// A PEM block holding a private key, whole.
	privateKey = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----[\s\S]*?(-----END [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----|$)`)

	// Tokens recognisable by their shape alone, wherever they appear.
	tokenShapes = regexp.MustCompile(`\b(?:` + strings.Join([]string{
		`gh[pousr]_[A-Za-z0-9]{20,}`,                                 // GitHub
		`github_pat_[A-Za-z0-9_]{20,}`,                               // GitHub, fine-grained
		`sk-ant-[A-Za-z0-9_-]{10,}`,                                  // Anthropic, Claude Code's OAuth tokens too
		`sk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,}`,                   // OpenAI
		`xox[abposre]-[A-Za-z0-9-]{10,}`,                             // Slack
		`glpat-[A-Za-z0-9_-]{20,}`,                                   // GitLab
		`npm_[A-Za-z0-9]{30,}`,                                       // npm
		`AKIA[0-9A-Z]{16}`,                                           // AWS access key id
		`AIza[0-9A-Za-z_-]{35}`,                                      // Google API key
		`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`, // a JWT
	}, "|") + `)`)

	// An Authorization header's credentials, whatever their scheme, and a
	// bearer token anywhere.
	authHeader = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)(?:(?:bearer|basic|token|digest)\s+)?[^\s"',;]+`)
	bearer     = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)

	// A value given a name that says it's secret: token=…, "apiKey": "…",
	// PASSWORD: …, X-Api-Key: …. The name is kept, so a reader knows what
	// was there.
	namedSecret = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:token|secret|passw(?:or)?d|passphrase|api[_-]?key|access[_-]?key|private[_-]?key|credentials?|cookie|authorization|session[_-]?id)[A-Za-z0-9_.-]*)("?'?\s*[:=]\s*"?'?)([^\s"'&,;}\]]+)`)

	// The user and password in a URL: https://user:pass@host.
	urlUserinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)

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
	s = privateKey.ReplaceAllString(s, redactedKey)
	s = tokenShapes.ReplaceAllString(s, redacted)
	s = urlUserinfo.ReplaceAllString(s, "$1"+redacted+"@")
	s = authHeader.ReplaceAllString(s, "${1}"+redacted)
	s = bearer.ReplaceAllString(s, "${1}"+redacted)
	s = namedSecret.ReplaceAllStringFunc(s, func(m string) string {
		sub := namedSecret.FindStringSubmatch(m)
		if strings.HasPrefix(sub[3], "[") || isCount(sub[1], sub[3]) {
			return m // already redacted, a placeholder, or a count of tokens
		}
		return sub[1] + sub[2] + redacted
	})
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

// counted are names whose number is a count, not a secret: the token
// counts chats log, like input_tokens=1234.
var counted = regexp.MustCompile(`(?i)tokens|count|limit|window|max|min|total|used|size`)

func isCount(name, value string) bool {
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return counted.MatchString(name)
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
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-')
}
