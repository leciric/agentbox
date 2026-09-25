// Package update asks agentbox.linting.dev whether a newer AgentBox has been
// released. The same request is how active installations are counted, so it
// carries exactly four things — a random ID made for the purpose, the version,
// the OS and the architecture — and nothing else about the machine or its user.
// Alongside it, unless the user turned it off, SendUsage reports how many times
// each feature was used (usage.go): the same four things, and counts keyed by
// api.Feature names. The README's "Update check" section says the same to the
// user; keep the two in step.
package update

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"agentbox/internal/hostos"
)

// DefaultURL is where the check goes unless AGENTBOX_UPDATE_URL says otherwise.
const DefaultURL = "https://agentbox.linting.dev/api/v1/latest"

// Timeout bounds one check. Anything slower is treated like any other failure:
// silently, as no news.
const Timeout = 5 * time.Second

// Interval is how often a running daemon asks again.
const Interval = 24 * time.Hour

// Latest is the server's answer.
type Latest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// Request is everything a check sends.
type Request struct {
	Install string // a random UUID, kept in state.db
	Version string
	OS      string
	Arch    string
}

// NewRequest is a request for this build of AgentBox. The OS is the machine's,
// not the daemon's: on a Mac or on Windows the daemon is Linux in a VM
// (D92, D94), and says so to nobody but hostos. The VM's architecture is the
// machine's own, so GOARCH is right everywhere.
func NewRequest(install, version string) Request {
	return Request{Install: install, Version: version, OS: cmp.Or(hostos.OS(), runtime.GOOS), Arch: runtime.GOARCH}
}

// Check asks base (DefaultURL when empty) for the latest release.
func Check(ctx context.Context, base string, req Request) (Latest, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	u, err := url.Parse(cmp.Or(base, DefaultURL))
	if err != nil {
		return Latest{}, err
	}
	u.RawQuery = url.Values{
		"install": {req.Install},
		"version": {req.Version},
		"os":      {req.OS},
		"arch":    {req.Arch},
	}.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Latest{}, err
	}
	// Go's default User-Agent names only Go. It says nothing about the machine,
	// and the query already carries the version, so it stays.
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return Latest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Latest{}, fmt.Errorf("update check: %s", resp.Status)
	}
	var out Latest
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&out); err != nil {
		return Latest{}, err
	}
	return out, nil
}

// Newer says whether latest is a later release than current. Anything that
// isn't a version ("dev", an empty answer) is never newer.
func Newer(latest, current string) bool {
	l, c := canonical(latest), canonical(current)
	return semver.IsValid(l) && semver.IsValid(c) && semver.Compare(l, c) > 0
}

func canonical(v string) string {
	return "v" + strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// Blocked says why the check is off whatever the setting says, or "" when it
// isn't: a development build has no release to compare with, and
// AGENTBOX_NO_UPDATE_CHECK=1 or DO_NOT_TRACK=1 in the daemon's environment
// turns it off outright.
func Blocked(version string) string {
	switch {
	case version == "dev" || version == "":
		return "this is a development build"
	case os.Getenv("AGENTBOX_NO_UPDATE_CHECK") == "1":
		return "AGENTBOX_NO_UPDATE_CHECK=1 is set"
	case os.Getenv("DO_NOT_TRACK") == "1":
		return "DO_NOT_TRACK=1 is set"
	}
	return ""
}
