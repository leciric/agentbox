package api

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Is reports whether name names this project, the way the daemon reads a
// project in a request: its slug exactly, or what it is called, in any case.
func (p Project) Is(name string) bool {
	name = strings.TrimSpace(name)
	return name == p.Name || strings.EqualFold(norm.NFC.String(name), norm.NFC.String(p.DisplayName))
}

// Called is what the user calls the project: its display name, or its slug
// from a daemon too old to have display names.
func (p Project) Called() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}
