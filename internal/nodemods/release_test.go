package nodemods

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func releaseStore(t *testing.T) (*Store, *[]string) {
	var logs []string
	return &Store{Dir: t.TempDir(), Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}, &logs
}

// install makes what an agent installed for in: a marker and a package.
func install(t *testing.T, s *Store, instance string, in Install) {
	t.Helper()
	if _, err := s.Prepare(instance, "p", []Install{in}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	write(t, filepath.Join(s.MountDir(instance, "."), "react", "index.js"), "react", old)
	write(t, filepath.Join(s.MountDir(instance, "."), ".package-lock.json"), "{}", time.Time{})
}

func hasReact(s *Store, instance string) bool {
	b, err := os.ReadFile(filepath.Join(s.MountDir(instance, "."), "react", "index.js"))
	return err == nil && string(b) == "react"
}

func logged(logs []string, part string) bool {
	for _, l := range logs {
		if strings.Contains(l, part) {
			return true
		}
	}
	return false
}

// A stopped agent's install leaves the disk: kept as the project's snapshot
// when there's none, deleted when there is one, and copied back at start.
func TestReleaseThenRestore(t *testing.T) {
	g := Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	in := Install{Group: g, Key: Key(g, []byte("lock"), "137"), LockTime: time.Now().Add(-time.Hour)}
	for _, haveSnapshot := range []bool{false, true} {
		t.Run(fmt.Sprintf("snapshot=%v", haveSnapshot), func(t *testing.T) {
			s, logs := releaseStore(t)
			if haveSnapshot {
				install(t, s, "ab-p-agent-0", in)
				s.Capture("ab-p-agent-0", "p", []Install{in})
				if !s.HasSnapshot("p", in.Key) {
					t.Fatal("no snapshot to start from")
				}
			}
			install(t, s, "ab-p-agent-1", in)

			s.Release("ab-p-agent-1", "p", []Install{in})
			if hasReact(s, "ab-p-agent-1") {
				t.Fatal("the install is still there after Release")
			}
			if !s.HasSnapshot("p", in.Key) {
				t.Fatal("no snapshot after Release")
			}
			if !logged(*logs, "released ab-p-agent-1's at stop") {
				t.Errorf("logs = %q, want the release", *logs)
			}

			s.Restore("ab-p-agent-1", "p", []Install{in})
			if !hasReact(s, "ab-p-agent-1") {
				t.Fatal("the install didn't come back at Restore")
			}
			if _, err := os.Stat(s.AgentFile("ab-p-agent-1", releasedFile)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s is still there: %v", releasedFile, err)
			}
			if !logged(*logs, "gave ab-p-agent-1 back 1 of 1 installs") {
				t.Errorf("logs = %q, want the restore", *logs)
			}
		})
	}
}

// An install changed after it was made isn't the snapshot's: it stays.
func TestReleaseKeepsAChangedInstall(t *testing.T) {
	s, logs := releaseStore(t)
	g := Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	in := Install{Group: g, Key: Key(g, []byte("lock"), ""), LockTime: time.Now().Add(-time.Hour)}
	install(t, s, "i", in)
	write(t, filepath.Join(s.MountDir("i", "."), "react", "index.js"), "react", time.Now().Add(time.Hour))

	s.Release("i", "p", []Install{in})
	if !hasReact(s, "i") || s.HasSnapshot("p", in.Key) {
		t.Fatal("a changed install was released")
	}
	if !logged(*logs, "changed after the install") {
		t.Errorf("logs = %q, want why it was kept", *logs)
	}
	s.Restore("i", "p", []Install{in})
	if !hasReact(s, "i") {
		t.Error("Restore touched an install that wasn't released")
	}
}

// A snapshot evicted while the agent was stopped leaves its node_modules
// empty, for it to install, and says so.
func TestRestoreWithTheSnapshotGone(t *testing.T) {
	s, logs := releaseStore(t)
	g := Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	in := Install{Group: g, Key: Key(g, []byte("lock"), ""), LockTime: time.Now().Add(-time.Hour)}
	install(t, s, "i", in)
	s.Release("i", "p", []Install{in})
	if err := os.RemoveAll(s.snapshotDir("p", in.Key)); err != nil {
		t.Fatal(err)
	}

	s.Restore("i", "p", []Install{in})
	entries, err := os.ReadDir(s.MountDir("i", "."))
	if err != nil || len(entries) != 0 {
		t.Errorf("node_modules = %v, %v; want there and empty", entries, err)
	}
	if !logged(*logs, "no snapshot left of package-lock.json") {
		t.Errorf("logs = %q, want the missing snapshot", *logs)
	}
}

// Restore gives back the key Release kept the install under, not the one
// the agent's installs.json says: the agent may have moved to another
// Node.js, whose ABI the key has, since it was made.
func TestRestoreUsesTheKeyOfTheRelease(t *testing.T) {
	s, _ := releaseStore(t)
	g := Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	atStop := Install{Group: g, Key: Key(g, []byte("lock"), "141"), LockTime: time.Now().Add(-time.Hour)}
	install(t, s, "i", atStop)
	s.Release("i", "p", []Install{atStop})

	made := atStop
	made.Key = Key(g, []byte("lock"), "137")
	s.Restore("i", "p", []Install{made})
	if !hasReact(s, "i") {
		t.Fatal("Restore looked for the key installs.json has")
	}
}

// A machine made again gets its node_modules from Prepare: what was released
// before is forgotten, and no later Restore empties what it has by then.
func TestPrepareForgetsWhatWasReleased(t *testing.T) {
	s, _ := releaseStore(t)
	g := Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	in := Install{Group: g, Key: Key(g, []byte("lock"), ""), LockTime: time.Now().Add(-time.Hour)}
	install(t, s, "i", in)
	s.Release("i", "p", []Install{in})
	if _, err := s.Prepare("i", "p", []Install{in}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(s.MountDir("i", "."), "react", "index.js"), "patched", time.Time{})

	s.Restore("i", "p", []Install{in})
	if b, err := os.ReadFile(filepath.Join(s.MountDir("i", "."), "react", "index.js")); err != nil || string(b) != "patched" {
		t.Errorf("react/index.js = %q, %v; want the agent's own, untouched", b, err)
	}
}

// An agent given its install from a snapshot can release it too: the copy
// keeps every time the install had, its links' included, which untouched
// compares with the install's.
func TestARestoredInstallCanBeReleased(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("links keep their times on Linux, where agents run")
	}
	s, logs := releaseStore(t)
	g := Group{Lockfile: "package-lock.json", Dirs: []string{"."}}
	in := Install{Group: g, Key: Key(g, []byte("lock"), ""), LockTime: time.Now().Add(-3 * time.Hour)}
	installed := time.Now().Add(-2 * time.Hour)
	root := s.MountDir("ab-p-agent-0", ".")
	if _, err := s.Prepare("ab-p-agent-0", "p", []Install{in}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "react", "index.js"), "react", installed)
	if err := os.MkdirAll(filepath.Join(root, ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../react/index.js", filepath.Join(root, ".bin", "react")); err != nil {
		t.Fatal(err)
	}
	if err := linkTime(filepath.Join(root, ".bin", "react"), installed); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(root, ".bin"), filepath.Join(root, "react")} {
		if err := os.Chtimes(d, installed, installed); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, ".package-lock.json"), "{}", installed)
	if err := os.Chtimes(root, installed, installed); err != nil {
		t.Fatal(err)
	}
	s.Capture("ab-p-agent-0", "p", []Install{in})
	if _, err := s.Prepare("ab-p-agent-1", "p", []Install{in}); err != nil {
		t.Fatal(err)
	}

	s.Release("ab-p-agent-1", "p", []Install{in})
	if hasReact(s, "ab-p-agent-1") {
		t.Fatalf("a restored install wasn't released: %q", *logs)
	}
}
