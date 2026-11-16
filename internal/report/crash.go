package report

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agentbox/internal/api"
)

// Crash records: the daemon's own crashes, which no client sees happen. Each
// is a file in the crashes folder, kept until a daemon sends it as an error
// report, which the next one does as it starts.
//
// Three things leave one:
//   - a panic the daemon recovered from, in an API handler (Panic);
//   - a crash it couldn't recover from, an unrecovered panic or a fatal
//     runtime error, whose traceback the Go runtime also writes to crash.out
//     (CrashOutput, runtime/debug.SetCrashOutput), so it is there however the
//     daemon was started and wherever its stderr went;
//   - a daemon that went without stopping and without a traceback (killed,
//     or out of memory): its run marker, running.json, is still there for the
//     next one, on the same boot. A machine that restarted isn't a crash.

// The kinds of crash.
const (
	CrashPanic = "panic" // recovered: the daemon carried on
	CrashFatal = "fatal" // the runtime's traceback: the daemon died of it
	CrashDied  = "died"  // gone without a traceback or stopping
)

// MaxCrashes is how many crash records are kept unsent: the newest.
const MaxCrashes = 5

// maxCrashStack is how much of a traceback a record keeps: its beginning,
// where the panic and the goroutine that crashed are. With GOTRACEBACK=all
// every other goroutine follows, which a report has no room for.
const maxCrashStack = 64 << 10

// Crash is one crash of the daemon.
type Crash struct {
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`      // when it crashed, or when that was found
	Version string    `json:"version"` // of the daemon that crashed
	Started time.Time `json:"started,omitzero"`
	Value   string    `json:"value"` // the panic's value, or the first line of the traceback
	Where   string    `json:"where,omitempty"`
	Stack   string    `json:"stack,omitempty"`
}

// Signature tells crashes apart: the same panic from the same place is one.
func (c Crash) Signature() string {
	return c.Kind + "|" + c.Value + "|" + crashFrame(c.Stack)
}

// crashFrame is the function a traceback crashed in: the first below its
// last panic, or the first that isn't the runtime's.
func crashFrame(stack string) string {
	var funcs []string
	for l := range strings.SplitSeq(stack, "\n") {
		// A traceback alternates a function's line with its file's, indented.
		if l == "" || strings.HasPrefix(l, "\t") || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "goroutine ") {
			continue
		}
		funcs = append(funcs, l)
	}
	from := 0
	for i, f := range funcs {
		if strings.HasPrefix(f, "panic(") {
			from = i + 1
		}
	}
	for _, f := range funcs[from:] {
		if !strings.HasPrefix(f, "runtime.") && !strings.HasPrefix(f, "runtime/debug.") && !strings.HasPrefix(f, "panic") && !strings.HasPrefix(f, "fatal error") {
			if i := strings.LastIndex(f, "("); i > 0 {
				f = f[:i] // its arguments, pointers that differ each run
			}
			return f
		}
	}
	return ""
}

// Message is the report's one line about c.
func (c Crash) Message() string {
	switch c.Kind {
	case CrashPanic:
		return cut("The daemon panicked"+prefixed(" in ", c.Where)+": "+c.Value, 1000)
	case CrashFatal:
		return cut("The daemon crashed: "+c.Value, 1000)
	default:
		return "The daemon stopped unexpectedly, without a panic or shutting down (killed, or out of memory?)"
	}
}

// Sections are the report's sections about c: the crash and its traceback,
// and the daemon that crashed. Clean redacts them like any.
func (c Crash) Sections() []api.ReportSection {
	var detail strings.Builder
	fmt.Fprintf(&detail, "Daemon:   %s\n", cmp.Or(c.Version, "unknown"))
	if !c.Started.IsZero() {
		fmt.Fprintf(&detail, "Started:  %s\n", c.Started.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&detail, "Crashed:  %s", c.At.UTC().Format(time.RFC3339))
	if !c.Started.IsZero() && c.At.After(c.Started) {
		fmt.Fprintf(&detail, " (after %s)", c.At.Sub(c.Started).Round(time.Second))
	}
	if c.Kind == CrashDied {
		detail.WriteString(" or before; found by the next daemon")
	}
	what := c.Value
	if c.Stack != "" {
		what += "\n\n" + c.Stack
	}
	return []api.ReportSection{
		{ID: api.ReportSectionError, Title: "The crash, in the daemon", Content: what},
		{ID: "daemon", Title: "The daemon that crashed", Content: detail.String()},
	}
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Crashes is the crashes folder.
type Crashes struct {
	Dir string
}

// Record keeps c, unless a record of the same crash is already waiting, and
// keeps no more than MaxCrashes records. r redacts it before it's written:
// nothing unredacted is left on disk for longer than a crash takes.
func (cs Crashes) Record(r Redactor, c Crash) error {
	c.Value, c.Where, c.Stack = r.Redact(c.Value), r.Redact(c.Where), r.Redact(head(c.Stack, maxCrashStack))
	pending, err := cs.Pending()
	if err != nil {
		return err
	}
	for _, p := range pending {
		if p.Signature() == c.Signature() {
			return nil
		}
	}
	if err := os.MkdirAll(cs.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	name := filepath.Join(cs.Dir, fmt.Sprintf("crash-%d.json", c.At.UnixNano()))
	if err := writeFile(name, b); err != nil {
		return err
	}
	for len(pending) >= MaxCrashes {
		_ = os.Remove(pending[0].file)
		pending = pending[1:]
	}
	return nil
}

// PendingCrash is a crash record on disk.
type PendingCrash struct {
	Crash
	file string
}

// Remove drops the record, once it was sent or won't be.
func (p PendingCrash) Remove() error { return os.Remove(p.file) }

// Pending is the crash records waiting to be sent, oldest first. A record
// that can't be read is dropped.
func (cs Crashes) Pending() ([]PendingCrash, error) {
	files, err := filepath.Glob(filepath.Join(cs.Dir, "crash-*.json"))
	if err != nil {
		return nil, err
	}
	var out []PendingCrash
	for _, f := range files {
		b, err := os.ReadFile(f)
		var c Crash
		if err == nil {
			err = json.Unmarshal(b, &c)
		}
		if err != nil {
			_ = os.Remove(f)
			continue
		}
		out = append(out, PendingCrash{c, f})
	}
	slices.SortFunc(out, func(a, b PendingCrash) int { return a.At.Compare(b.At) })
	return out, nil
}

// marker is running.json: the daemon that's running, until it stops.
type marker struct {
	Version string    `json:"version"`
	PID     int       `json:"pid"`
	Boot    string    `json:"boot"`
	Started time.Time `json:"started"`
}

func (cs Crashes) markerPath() string { return filepath.Join(cs.Dir, "running.json") }

// CrashOutput is where the runtime writes a fatal traceback.
func (cs Crashes) CrashOutput() string { return filepath.Join(cs.Dir, "crash.out") }

// Start is a daemon starting: it records how the last one ended, if that was
// a crash, and marks this one running. boot is the machine's boot ID ("" when
// unknown, and then a daemon that went without a trace isn't recorded). With
// output, the runtime's fatal tracebacks go to CrashOutput from now on. stop
// is the daemon stopping as it should.
func (cs Crashes) Start(r Redactor, version, boot string, output bool) (stop func(), err error) {
	if err := os.MkdirAll(cs.Dir, 0o700); err != nil {
		return nil, err
	}
	var last marker
	b, err := os.ReadFile(cs.markerPath())
	running := err == nil && json.Unmarshal(b, &last) == nil
	now := time.Now()
	if out, err := os.ReadFile(cs.CrashOutput()); err == nil && strings.TrimSpace(string(out)) != "" {
		c := Crash{Kind: CrashFatal, At: now, Value: firstLine(string(out)), Stack: string(out)}
		if running {
			c.Version, c.Started = last.Version, last.Started
		}
		if info, err := os.Stat(cs.CrashOutput()); err == nil {
			c.At = info.ModTime()
		}
		if err := cs.Record(r, c); err != nil {
			return nil, err
		}
	} else if running && boot != "" && last.Boot == boot && last.PID != os.Getpid() {
		if err := cs.Record(r, Crash{Kind: CrashDied, At: now, Version: last.Version, Started: last.Started}); err != nil {
			return nil, err
		}
	}
	b, err = json.Marshal(marker{Version: version, PID: os.Getpid(), Boot: boot, Started: now})
	if err != nil {
		return nil, err
	}
	if err := writeFile(cs.markerPath(), b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(cs.CrashOutput(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if output {
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	_ = f.Close() // SetCrashOutput keeps its own copy
	return func() {
		if output {
			_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		}
		_ = os.Remove(cs.markerPath())
		_ = os.Remove(cs.CrashOutput())
	}, nil
}

// BootID is this boot of the machine's ID, or "" where there's none.
func BootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return cut(l, 500)
		}
	}
	return ""
}

// head is the beginning of s, at most limit bytes, marked as cut.
func head(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") + "\n[… later lines left out]"
}

// writeFile writes b to name whole or not at all.
func writeFile(name string, b []byte) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
