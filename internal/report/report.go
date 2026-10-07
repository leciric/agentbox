// Package report sends problem reports to AgentBox's developers: the user's
// own, from the app's "Report a problem" or `agentbox report`, and the app's
// reports of uncaught errors once the user turned them on. A report is a
// message and a few sections of text — the version and setup state, the end
// of the daemon's log, the app's — each redacted (redact.go), shown to the
// user in full, and left out if they choose. It goes to agentbox.linting.dev
// beside the update check, with the same four things the check sends.
//
// The approach follows t3code's (github.com/pingdotgg/t3code): logs kept in
// capped files that a report reads the end of, stack traces cut to their
// frames, and URLs stripped of their credentials. t3code stops at an "open
// the logs folder" button; AgentBox sends what the user agreed to.
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/update"
)

// The limits of one report. The server enforces the same ones
// (agentbox-landing's functions/api/v1/reports.ts); keep the two in step.
const (
	MaxMessage     = 5000      // characters
	MaxSections    = 10        //
	MaxTitle       = 80        // characters
	MaxSection     = 96 << 10  // bytes of one section's content
	MaxTotal       = 320 << 10 // bytes of every section's content together
	DefaultLogTail = 48 << 10  // bytes a log section keeps of its file's end
)

// VMLogEnv is where the VM supervisor's log is, as the front end tells the
// commands it runs in a Cloud Hypervisor VM (hostvm.VM.vmEnv): in the host's
// home, which the VM shares at the same path, so the daemon there can read it.
const VMLogEnv = "AGENTBOX_VM_LOG"

var sectionID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Clean is sections made fit to send: redacted, each cut to MaxSection
// (keeping its end, which is where a log's news is), the empty and the
// repeated left out, and no more than MaxSections or MaxTotal. A section with
// a bad ID or title is an error: that's a client's bug, not the user's.
func Clean(r Redactor, sections []api.ReportSection) ([]api.ReportSection, error) {
	out := make([]api.ReportSection, 0, len(sections))
	seen := map[string]bool{}
	total := 0
	for _, s := range sections {
		if !sectionID.MatchString(s.ID) {
			return nil, fmt.Errorf("a report section's id must be lowercase words joined by -, not %q", s.ID)
		}
		s.Title = strings.TrimSpace(s.Title)
		if s.Title == "" || utf8.RuneCountInString(s.Title) > MaxTitle {
			return nil, fmt.Errorf("report section %s needs a title of at most %d characters", s.ID, MaxTitle)
		}
		if seen[s.ID] {
			continue
		}
		s.Content = Tail(r.Redact(strings.TrimSpace(s.Content)), MaxSection)
		if s.Content == "" {
			continue
		}
		if total+len(s.Content) > MaxTotal || len(out) == MaxSections {
			break
		}
		seen[s.ID] = true
		total += len(s.Content)
		out = append(out, s)
	}
	return out, nil
}

// CleanMessage is the user's message, redacted, or why it can't be sent.
func CleanMessage(r Redactor, kind, message string) (string, error) {
	message = strings.TrimSpace(message)
	switch {
	case kind != api.ReportKindProblem && kind != api.ReportKindError:
		return "", fmt.Errorf("a report's kind is %q or %q, not %q", api.ReportKindProblem, api.ReportKindError, kind)
	case message == "":
		return "", errors.New("say what went wrong: a report needs a message")
	case utf8.RuneCountInString(message) > MaxMessage:
		return "", fmt.Errorf("the message is longer than %d characters", MaxMessage)
	}
	return r.Redact(message), nil
}

// Tail is the end of s, at most limit bytes, starting on a whole line when
// it had to be cut, and marked as cut.
func Tail(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	const mark = "[… earlier lines left out]\n"
	s = s[len(s)-(limit-len(mark)):]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return mark + s
}

// TailFile is the last limit bytes of the file at path, as Tail cuts them, or
// a line saying why it can't be read.
func TailFile(path string, limit int) string {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf("(%s doesn't exist)", path)
		}
		return fmt.Sprintf("(couldn't read %s: %v)", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Sprintf("(couldn't read %s: %v)", path, err)
	}
	// Read a little more than limit, so Tail can start on a whole line.
	start := max(info.Size()-int64(limit)-4096, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return fmt.Sprintf("(couldn't read %s: %v)", path, err)
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+4096))
	if err != nil {
		return fmt.Sprintf("(couldn't read %s: %v)", path, err)
	}
	if len(b) == 0 {
		return fmt.Sprintf("(%s is empty)", path)
	}
	return Tail(string(b), limit)
}

// Payload is what goes to the server: the update check's four things, and
// the report.
type Payload struct {
	Install  string              `json:"install"`
	Version  string              `json:"version"`
	OS       string              `json:"os"`
	Arch     string              `json:"arch"`
	Kind     string              `json:"kind"`
	Message  string              `json:"message"`
	Sections []api.ReportSection `json:"sections"`
}

// URL is where reports go: "reports" beside the update check's URL (base, or
// update.DefaultURL when empty), so AGENTBOX_UPDATE_URL moves all three.
func URL(base string) (string, error) {
	u, err := update.Endpoint(base)
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	return u.ResolveReference(&url.URL{Path: "reports"}).String(), nil
}

// Send posts p to URL(base), and is the server's ID for it.
func Send(ctx context.Context, base string, p Payload) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*update.Timeout)
	defer cancel()
	target, err := URL(base)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return "", fmt.Errorf("sending the report: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var answer struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", errors.New("too many reports were sent from this machine lately: try again in an hour")
	case resp.StatusCode/100 != 2:
		return "", fmt.Errorf("the report wasn't taken: %s%s", resp.Status, prefixed(": ", answer.Error))
	case answer.ID == "":
		return "", errors.New("the report server didn't say it kept the report")
	}
	return answer.ID, nil
}

func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}
