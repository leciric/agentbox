// Package redact finds secrets in text by their shape or by the name written
// beside them, and replaces them with a placeholder. It's the one set of
// patterns AgentBox has for that: an error report runs it before it leaves
// the machine (internal/report, which also removes emails and homes), and
// project memory runs it before anything is stored (internal/memory), since
// whatever memory keeps is shown to every later agent. It errs on the side of
// removing too much, and leaves alone what has no name and no shape: a commit
// hash, an id, a number.
package redact

import (
	"regexp"
	"strings"
)

// The placeholders a redacted value becomes, so a reader can tell what was
// there.
const (
	Placeholder = "[redacted]"
	PrivateKey  = "[private key]"
)

// secretWords are what a name says when its value is a secret: token=…,
// "apiKey": "…", PASSWORD: …, X-Api-Key: ….
const secretWords = `(?:token|secret|passw(?:or)?d|passphrase|api[_-]?key|access[_-]?key|private[_-]?key|credentials?|cookie|authorization|session[_-]?id)`

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

	// A value given a name that says it's secret. The name is kept, so a
	// reader knows what was there.
	namedSecret = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*` + secretWords + `[A-Za-z0-9_.-]*)("?'?\s*[:=]\s*"?'?)([^\s"'&,;}\]]+)`)
	secretName  = regexp.MustCompile(`(?i)` + secretWords)

	// The user and password in a URL: https://user:pass@host.
	urlUserinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
)

// Secrets is s with every secret replaced. Running it twice changes nothing
// more.
func Secrets(s string) string {
	if s == "" {
		return s
	}
	s = privateKey.ReplaceAllString(s, PrivateKey)
	s = tokenShapes.ReplaceAllString(s, Placeholder)
	s = urlUserinfo.ReplaceAllString(s, "$1"+Placeholder+"@")
	s = authHeader.ReplaceAllString(s, "${1}"+Placeholder)
	s = bearer.ReplaceAllString(s, "${1}"+Placeholder)
	return namedSecret.ReplaceAllStringFunc(s, func(m string) string {
		sub := namedSecret.FindStringSubmatch(m)
		if strings.HasPrefix(sub[3], "[") || isCount(sub[1], sub[3]) {
			return m // already redacted, a placeholder, or a count of tokens
		}
		return sub[1] + sub[2] + Placeholder
	})
}

// Named is value as it may be kept under name, a field of a JSON document
// say, where the name isn't in the text for Secrets to see: the placeholder
// when the name says it's secret, value with its own secrets removed
// otherwise.
func Named(name, value string) string {
	if value != "" && secretName.MatchString(name) && !strings.HasPrefix(value, "[") && !isCount(name, value) {
		return Placeholder
	}
	return Secrets(value)
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
