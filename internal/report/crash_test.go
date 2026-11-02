package report

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestCrashRecordsAreRedactedDedupedAndCapped(t *testing.T) {
	home := "/home/someone"
	cs := Crashes{Dir: filepath.Join(t.TempDir(), "crashes")}
	r := NewRedactor(home)
	at := time.Now()
	c := Crash{Kind: CrashPanic, At: at, Version: "0.15.0", Value: "open " + home + "/x: token=abc", Stack: "goroutine 1 [running]:\nmain.f(0xc0001)\n\t" + home + "/f.go:3"}
	if err := cs.Record(r, c); err != nil {
		t.Fatal(err)
	}
	c.At = at.Add(time.Second)
	c.Stack = strings.Replace(c.Stack, "0xc0001", "0xc0099", 1) // another run of the same panic
	if err := cs.Record(r, c); err != nil {
		t.Fatal(err)
	}
	pending, err := cs.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %+v, %v", pending, err)
	}
	if p := pending[0]; p.Value != "open ~/x: token=[redacted]" || strings.Contains(p.Stack, home) {
		t.Errorf("not redacted: %+v", p.Crash)
	}
	for i := range MaxCrashes + 2 {
		if err := cs.Record(r, Crash{Kind: CrashPanic, At: at.Add(time.Duration(i+2) * time.Second), Value: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	pending, _ = cs.Pending()
	if len(pending) != MaxCrashes || pending[len(pending)-1].Value != string(rune('a'+MaxCrashes+1)) {
		t.Errorf("kept %d, newest %q", len(pending), pending[len(pending)-1].Value)
	}
	if err := pending[0].Remove(); err != nil {
		t.Fatal(err)
	}
	if pending, _ = cs.Pending(); len(pending) != MaxCrashes-1 {
		t.Errorf("removed: %d left", len(pending))
	}
}

func TestCrashSignatureIsWhereItPanicked(t *testing.T) {
	stack := func() (s string) {
		defer func() { _ = recover(); s = string(debug.Stack()) }()
		panic("boom")
	}
	a, b := Crash{Kind: CrashPanic, Value: "boom", Stack: stack()}, Crash{Kind: CrashPanic, Value: "boom", Stack: stack()}
	if a.Signature() != b.Signature() || !strings.Contains(a.Signature(), "TestCrashSignatureIsWhereItPanicked") {
		t.Errorf("signatures %q, %q", a.Signature(), b.Signature())
	}
}

func TestStartRecordsADaemonThatDiedOnTheSameBoot(t *testing.T) {
	r := NewRedactor()
	cs := Crashes{Dir: t.TempDir()}
	stop, err := cs.Start(r, "0.15.0", "boot-1", false)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := cs.Start(r, "0.15.0", "boot-1", false); err != nil { // not stopped: it died
		t.Fatal(err)
	}
	if pending, _ := cs.Pending(); len(pending) != 0 {
		t.Fatalf("a daemon that stopped was recorded: %+v", pending)
	}
	// The marker is this process's: as a daemon's, written by another.
	b, _ := json.Marshal(marker{Version: "0.14.0", PID: os.Getpid() + 1, Boot: "boot-1", Started: time.Now().Add(-time.Hour)})
	if err := os.WriteFile(cs.markerPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	stop, err = cs.Start(r, "0.15.0", "boot-1", false)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := cs.Pending()
	if len(pending) != 1 || pending[0].Kind != CrashDied || pending[0].Version != "0.14.0" ||
		!strings.Contains(pending[0].Sections()[1].Content, "(after 1h0m0s)") {
		t.Fatalf("pending %+v", pending)
	}
	_ = pending[0].Remove()
	stop()

	b, _ = json.Marshal(marker{Version: "0.14.0", PID: os.Getpid() + 1, Boot: "boot-1"})
	_ = os.WriteFile(cs.markerPath(), b, 0o600)
	if _, err := cs.Start(r, "0.15.0", "boot-2", false); err != nil {
		t.Fatal(err)
	}
	if pending, _ := cs.Pending(); len(pending) != 0 {
		t.Errorf("a machine that restarted was recorded as a crash: %+v", pending)
	}
}

// TestStartRecordsTheRuntimesTraceback crashes a process for real, with an
// unrecovered panic in a goroutine, and finds its traceback the next start.
func TestStartRecordsTheRuntimesTraceback(t *testing.T) {
	if dir := os.Getenv("CRASH_TEST_DIR"); dir != "" {
		if _, err := (Crashes{Dir: dir}).Start(NewRedactor(), "0.15.0", "boot-1", true); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			var m map[string]int
			m["x"] = 1 //nolint:staticcheck // the crash this test is about
		}()
		<-done
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStartRecordsTheRuntimesTraceback$")
	cmd.Env = append(os.Environ(), "CRASH_TEST_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "nil map") {
		t.Fatalf("the process didn't crash: %v\n%s", err, out)
	}
	cs := Crashes{Dir: dir}
	stop, err := cs.Start(NewRedactor(), "0.16.0", "boot-1", false)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	pending, _ := cs.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending %+v", pending)
	}
	c := pending[0]
	if c.Kind != CrashFatal || c.Version != "0.15.0" || c.Value != "panic: assignment to entry in nil map" ||
		!strings.Contains(c.Stack, "TestStartRecordsTheRuntimesTraceback") {
		t.Errorf("recorded %+v", c.Crash)
	}
	if c.Message() != "The daemon crashed: panic: assignment to entry in nil map" {
		t.Errorf("message %q", c.Message())
	}
}

func TestHeadKeepsTheBeginning(t *testing.T) {
	if s := head(strings.Repeat("é", 10), 5); s != "éé\n[… later lines left out]" {
		t.Errorf("head = %q", s)
	}
}
