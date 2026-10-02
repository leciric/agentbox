// Package naming validates agent names and makes the slugs projects are known
// by inside AgentBox.
package naming

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	valid   = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
	invalid = regexp.MustCompile(`[^a-z0-9]+`)
)

// Validate checks a name that becomes part of an Incus instance name
// (ab-<project>-<agent>, at most 63 characters).
func Validate(kind, name string, max int) error {
	if len(name) > max || !valid.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: use lowercase letters, digits and hyphens, start with a letter, at most %d characters", kind, name, max)
	}
	return nil
}

// Slug turns a directory name into a candidate name.
func Slug(s string) string {
	return strings.Trim(invalid.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// MaxProjectSlug keeps ab-<project>-<agent> within Incus' 63-character limit.
const MaxProjectSlug = 30

// ProjectSlug is the identifier a project with that display name is known by
// inside AgentBox: its Incus containers (ab-<slug>-<agent>), its folders
// under the data directory, its sockets, URLs and MCP ids. A display name may
// be anything, so the slug is made from it rather than checked: accents come
// off ("Café" is "cafe"), camelCase is split into words ("organicWebApp" is
// organic-web-app), anything else that isn't a letter or digit becomes a
// hyphen, and a slug that would still not do — empty, all Japanese, or
// starting with a digit — gets "project" in front. taken reports a slug
// already in use, which gets -2, -3… instead.
func ProjectSlug(display string, taken func(string) bool) string {
	var letters []rune
	for _, r := range norm.NFKD.String(display) {
		if !unicode.Is(unicode.Mn, r) {
			letters = append(letters, r)
		}
	}
	// camelCase is words too: a capital starts one after a small letter or a
	// digit ("organicWebApp"), and so does the last capital of a run that
	// a small letter follows ("HTTPServer").
	var b strings.Builder
	for i, r := range letters {
		if i > 0 && unicode.IsUpper(r) {
			prev := letters[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) ||
				unicode.IsUpper(prev) && i+1 < len(letters) && unicode.IsLower(letters[i+1]) {
				b.WriteRune('-')
			}
		}
		b.WriteRune(r)
	}
	base := Slug(b.String())
	if base == "" {
		base = "project"
	} else if !valid.MatchString(base) {
		base = "project-" + base
	}
	base = trim(base, MaxProjectSlug)
	slug := base
	for n := 2; taken != nil && taken(slug); n++ {
		suffix := "-" + strconv.Itoa(n)
		slug = trim(base, MaxProjectSlug-len(suffix)) + suffix
	}
	return slug
}

// trim cuts a slug to at most max bytes, never leaving a hyphen at the end.
func trim(slug string, max int) string {
	if len(slug) > max {
		slug = slug[:max]
	}
	return strings.TrimRight(slug, "-")
}
