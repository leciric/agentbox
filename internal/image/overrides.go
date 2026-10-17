package image

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Overrides are versions the base image runs instead of the ones tools.txt
// pins, by mise's name for the tool: "claude" → "2.1.302". The daemon records
// them in state once an update to them proved itself ("Keep Claude Code up to
// date", internal/daemon/toolupdates.go); tools.txt is never edited for them.
//
// A pin stays the floor: an override older than its pin, or that isn't a
// version, counts for nothing, so moving a pin on in tools.txt past it takes
// over as it always has.
type Overrides map[string]string

// Version is the version of the tool mise calls name the image runs: the
// override when it is newer than the pin, the pin otherwise.
func (o Overrides) Version(name string) string {
	return pinnedVersion(o.Spec(name))
}

// Spec is the spec of the tool mise calls name, with the version it runs:
// Pin(name), or the override. It panics as Pin does.
func (o Overrides) Spec(name string) string {
	return o.apply(Tool{Spec: Pin(name)}).Spec
}

// Apply is tools with their overrides in place.
func (o Overrides) Apply(tools []Tool) []Tool {
	if len(o) == 0 {
		return tools
	}
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, o.apply(t))
	}
	return out
}

func (o Overrides) apply(t Tool) Tool {
	if v := o[t.Name()]; NewerVersion(v, pinnedVersion(t.Spec)) {
		t.Spec = t.Name() + "@" + v
	}
	return t
}

// NewerVersion reports whether version a is newer than b, both semantic
// versions without a "v": false when either isn't one.
func NewerVersion(a, b string) bool {
	a, b = "v"+a, "v"+b
	return semver.IsValid(a) && semver.IsValid(b) && semver.Compare(a, b) > 0
}

// pinnedVersion is the version in a spec: "2.1.280" of "claude@2.1.280".
func pinnedVersion(spec string) string {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[i+1:]
	}
	return ""
}
