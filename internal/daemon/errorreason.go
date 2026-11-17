package daemon

import (
	"context"
	"errors"
	"math"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/hostos"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// Why an error event's error happened, so the commonest can be told apart
// without the error itself: a class from the fixed list below, and a reason,
// the error's own words with everything that could say whose it was taken
// out (errorReason). Never a prompt, a file's contents or anything typed:
// what reaches here is an error AgentBox or a tool wrote, and even that
// loses its paths, hosts, names, quoted text and secrets.

// The classes, the most specific first. The server takes exactly these
// (agentbox-landing's functions/api/v1/events.ts).
const (
	classOOMKilled  = "oom_killed" // killed for memory: its machine's limit, or the VM's
	classKilled     = "killed"     // SIGKILL (exit 137) with no sign it was for memory
	classTerminated = "terminated" // SIGTERM (exit 143)
	classSignal     = "signal"     // another signal
	classACPClosed  = "acp_closed" // the ACP link to the adapter closed under it
	classIncusDown  = "incus_down" // Incus wasn't answering, or had just restarted
	classNoSpace    = "no_space"   // a disk was full
	classNetwork    = "network"    // a connection failed: DNS, refused, reset, TLS
	classTimeout    = "timeout"    // something took too long
	classAuth       = "auth"       // a login, key or token was refused
	classCancelled  = "cancelled"  // stopped on purpose
	classNoImage    = "image_missing"
	classNotFound   = "not_found"
	classPermission = "permission"
	classChecksum   = "checksum"  // a download didn't match its checksum
	classExitCode   = "exit_code" // a command exited non-zero, for no reason found
	classOther      = "other"
)

// errorClasses is every class, as the server takes them.
var errorClasses = []string{
	classOOMKilled, classKilled, classTerminated, classSignal, classACPClosed, classIncusDown,
	classNoSpace, classNetwork, classTimeout, classAuth, classCancelled, classNoImage, classNotFound,
	classPermission, classChecksum, classExitCode, classOther,
}

// maxReason is the longest reason sent, in bytes, all of them ASCII.
const maxReason = 120

// textClasses are what an error's text says, in the order they are tried:
// how it died before what it was doing.
var textClasses = []struct {
	class string
	words []string
}{
	{classNoSpace, []string{"no space left", "enospc", "disk quota exceeded", "not enough space", "not enough free space", "disk is full"}},
	{classOOMKilled, []string{"out of memory", "oom-kill", "oom_kill", "oom killed", "cannot allocate memory", "javascript heap out of memory"}},
	{classKilled, []string{"signal: killed", "exit status 137", "sigkill"}},
	{classTerminated, []string{"signal: terminated", "exit status 143", "sigterm"}},
	{classSignal, []string{"signal: "}},
	{classACPClosed, []string{"acp connection closed", strings.ToLower(acpClosedText)}},
	{classIncusDown, []string{strings.ToLower(incus.ErrNotAnswering.Error()), "incus.socket", "unix.socket: connect"}},
	{classAuth, []string{"unauthorized", "unauthenticated", "authentication", "bad credentials", "invalid api key", "invalid x-api-key", "not logged in", "login required", "permission denied (publickey", "token expired", "token was rejected", "forbidden"}},
	{classNetwork, []string{"dial tcp", "dial udp", "no such host", "connection refused", "connection reset", "network is unreachable", "no route to host", "tls handshake", "temporary failure in name resolution", "i/o timeout", "could not resolve host", "unexpected eof"}},
	{classTimeout, []string{"deadline exceeded", "timed out", "timeout"}},
	{classCancelled, []string{"context canceled", "cancelled", "canceled"}},
	{classNoImage, []string{"base image isn't built", "image not found", "no such image"}},
	{classChecksum, []string{"checksum", "sha256 mismatch", "doesn't match its sha256"}},
	{classPermission, []string{"permission denied", "operation not permitted", "access is denied", "eacces"}},
	{classNotFound, []string{"not found", "no such file", "does not exist", "doesn't exist", "enoent"}},
	{classExitCode, []string{"exit status ", "exit code "}},
}

// acpClosedText is the ACP connection's own words for an adapter that went
// away (acp.ErrClosed).
const acpClosedText = "the agent closed the connection"

// errorClass is the class of err, whose text is text (an error's, or what an
// adapter's exit said).
func errorClass(err error, text string) string {
	if err != nil {
		var exit *exec.ExitError
		var ne net.Error
		switch {
		case errors.Is(err, syscall.ENOSPC):
			return classNoSpace
		case errors.Is(err, context.Canceled):
			return classCancelled
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
			return classTimeout
		case errors.Is(err, incus.ErrNotAnswering):
			return classIncusDown
		case errors.As(err, &exit):
			if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				switch ws.Signal() {
				case syscall.SIGKILL:
					return classKilled
				case syscall.SIGTERM:
					return classTerminated
				}
				return classSignal
			}
		case errors.As(err, &ne):
			return classNetwork
		}
		if text == "" {
			text = err.Error()
		}
	}
	lower := strings.ToLower(text)
	for _, c := range textClasses {
		for _, w := range c.words {
			if strings.Contains(lower, w) {
				return c.class
			}
		}
	}
	return classOther
}

var (
	reasonURL     = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S*`)
	reasonEmail   = regexp.MustCompile(`[^\s"'<>()\[\]{},;:]+@[^\s"'<>()\[\]{},;:]+`)
	reasonQuoted  = regexp.MustCompile("\"[^\"]*\"|'[^']*'|`[^`]*`|“[^”]*”|‘[^’]*’|«[^»]*»")
	reasonWinPath = regexp.MustCompile(`(?i)\b[a-z]:\\[^\s"'<>|]*`)
	// Anything with a slash in it: a path, a branch, a project/agent ref.
	// Brackets may be inside, as in the Redactor's /home/[user]/src.
	reasonPath = regexp.MustCompile(`[^\s"'(){}<>,;=]*[/\\][^\s"'(){}<>,;]*`)
	reasonUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reasonHex  = regexp.MustCompile(`(?i)\b[0-9a-f]{16,}\b`)
	reasonIPv4 = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	reasonIPv6 = regexp.MustCompile(`(?i)\b[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){3,}\b`)
	// A dotted name: a host (api.example.com) or a file (notes.md), either
	// of which may be the user's. Versions (0.16.0) have no letters at the end.
	reasonDotted = regexp.MustCompile(`(?i)\b[a-z0-9_-]+(?:\.[a-z0-9_-]+)*\.[a-z][a-z0-9-]*\b`)
	reasonSpaces = regexp.MustCompile(`\s+`)
	// A word with anything but ASCII in it goes whole: a name, or text in
	// another language, which the fixed wording around it explains.
	reasonNonASCII = regexp.MustCompile(`\S*[^\x00-\x7f]\S*`)
)

// keptDotted are dotted names that are AgentBox's own, worth keeping.
var keptDotted = []string{"incus.service", "incus.socket", "state.db", "provision.sh", "tools.txt", "package.json", "node.js"}

// errorReason is text as an error event may carry it: secrets, emails and
// homes out first (the error report's Redactor, which runs internal/redact),
// then every name in names (the project's, the agent's, its title and
// branch, the user's), and anything shaped like a URL, an email, a path, a
// host or file name, an address, an id or quoted text; then ASCII only, on
// one line, at most maxReason bytes. What's left is the error's wording.
func (s *Server) errorReason(text string, names ...string) string {
	text = s.redactor().Redact(text)
	return scrubReason(text, append(names, s.cfg.User.Name, hostname())...)
}

func scrubReason(text string, names ...string) string {
	text = reasonURL.ReplaceAllString(text, "[url]")
	text = reasonEmail.ReplaceAllString(text, "[email]")
	text = reasonQuoted.ReplaceAllString(text, `"[text]"`)
	text = reasonWinPath.ReplaceAllString(text, "[path]")
	text = reasonPath.ReplaceAllStringFunc(text, func(m string) string {
		// Keep the colon an error puts after a path: "[path]: no such file".
		if strings.HasSuffix(m, ":") {
			return "[path]:"
		}
		return "[path]"
	})
	text = reasonUUID.ReplaceAllString(text, "[id]")
	text = reasonHex.ReplaceAllString(text, "[id]")
	text = reasonIPv4.ReplaceAllString(text, "[ip]")
	text = reasonIPv6.ReplaceAllString(text, "[ip]")
	text = reasonDotted.ReplaceAllStringFunc(text, func(m string) string {
		if slices.Contains(keptDotted, strings.ToLower(m)) {
			return m
		}
		return "[name]"
	})
	// The names last, so a path that holds one goes whole. Longest first,
	// so a name inside another goes with it.
	names = slices.DeleteFunc(slices.Clone(names), func(n string) bool { return len(strings.TrimSpace(n)) < 3 })
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	for _, n := range names {
		text = replaceFold(text, strings.TrimSpace(n), "[name]")
	}
	text = reasonNonASCII.ReplaceAllString(strings.ReplaceAll(text, "…", "..."), "?")
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '/' || r == '\\' || r == '@':
			b.WriteByte(' ') // none left, whatever got past the above
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	text = strings.TrimSpace(reasonSpaces.ReplaceAllString(b.String(), " "))
	if len(text) > maxReason {
		text = strings.TrimSpace(text[:maxReason-3]) + "..."
	}
	return text
}

// replaceFold replaces every old in s, whatever its case, with repl.
func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lower, lowerOld := strings.ToLower(s), strings.ToLower(old)
	if len(lower) != len(s) || len(lowerOld) != len(old) {
		return strings.ReplaceAll(s, old, repl) // a case change that resizes: exact only
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, lowerOld)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, lower = s[i+len(old):], lower[i+len(old):]
	}
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

// agentNames are an agent's names a reason must not carry: its project's, its
// own, its title, its branch and machine.
func agentNames(a state.Agent) []string {
	return []string{a.Project, a.Name, a.Title, a.Branch, a.Instance, a.Worktree}
}

// createReason is a failed create's class and reason: the step it failed at
// (agent.CreateError), which is AgentBox's own words, then the error's.
func (s *Server) createReason(err error, names ...string) (class, reason string) {
	text := err.Error()
	var ce *agent.CreateError
	if errors.As(err, &ce) {
		text = ce.Step + ": " + ce.Err.Error()
	}
	return errorClass(err, text), s.errorReason(text, names...)
}

// lostWindow is how long before an adapter exits a sign of why still counts:
// an OOM kill in its machine, Incus restarting.
const lostWindow = 2 * time.Minute

// lostClass is why an agent's AI tool exited mid-turn, from what its exit
// said (why: "Claude Code exited (exit status 137): …") and what the daemon
// saw around it: a process of its machine killed for memory, or Incus
// restarting, which cuts every `incus exec` the chats run on, so each
// adapter reads the end of its input and says "ACP connection closed".
func (s *Server) lostClass(a state.Agent, why string) string {
	class := errorClass(nil, why)
	if s.oomKilledLately(a.Instance) {
		switch class {
		case classKilled, classSignal, classACPClosed, classExitCode, classOther:
			return classOOMKilled
		}
	}
	if s.incusTroubleLately() {
		switch class {
		case classACPClosed, classExitCode, classOther, classTerminated:
			return classIncusDown
		}
	}
	return class
}

// oomKilledLately reports whether a process of instance's machine was killed
// for memory in the last lostWindow, or since the memory watch last looked.
func (s *Server) oomKilledLately(instance string) bool {
	if instance == "" || s.memoryEvents == nil {
		return false
	}
	now, ok := s.memoryEvents(instance)
	s.mu.Lock()
	defer s.mu.Unlock()
	if seen, known := s.oomSeen[instance]; ok && known && now.Kills > seen.Kills {
		return true
	}
	at, ok := s.oomAt[instance]
	return ok && time.Since(at) < lostWindow
}

// incusTroubleLately reports whether Incus isn't answering, or the watch
// restarted it in the last lostWindow.
func (s *Server) incusTroubleLately() bool {
	w := s.incus
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.status.Answering {
		return true
	}
	return !w.lastRestart.IsZero() && time.Since(w.lastRestart) < lostWindow
}

// Usage events carry the installation's UTC offset in whole hours, so the
// stats can be read in local time: the timezone the app last ran in
// (Settings.Timezone), since the VM's clock is in UTC, else this machine's
// own outside a VM, else "unknown".
const offsetUnknown = "unknown"

// utcOffsets are every offset sent, as the server takes them.
var utcOffsets = func() []string {
	var out []string
	for h := -12; h <= 14; h++ {
		out = append(out, strconv.Itoa(h))
	}
	return append(out, offsetUnknown)
}()

func (s *Server) usageUTCOffset(ctx context.Context, now time.Time) string {
	name, _ := s.store.Setting(ctx, state.SettingTimezone)
	var loc *time.Location
	if name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	if loc == nil && !hostos.InVM() {
		loc = time.Local
	}
	if loc == nil {
		return offsetUnknown
	}
	return utcOffset(now.In(loc))
}

// utcOffset is t's zone offset rounded to the hour: +5:30 is 6, -9:30 is -10.
func utcOffset(t time.Time) string {
	_, secs := t.Zone()
	h := int(math.Round(float64(secs) / 3600))
	return strconv.Itoa(min(max(h, -12), 14))
}
