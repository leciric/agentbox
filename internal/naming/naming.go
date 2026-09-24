// Package naming validates project and agent names.
package naming

import (
	"fmt"
	"regexp"
	"strings"
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
