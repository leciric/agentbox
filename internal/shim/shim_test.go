//go:build !windows

package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A project's chat runs with its own HOME, so anything it runs must be a real
// executable. A version manager's shim is the manager itself, working out what
// to run from directories under HOME, so a shim in the lead's environment fails
// with "not a valid shim" and takes the adapter down with it. Found on a real
// machine after 0.3.0, which is why each of these is pinned.
func TestIs(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/home/you/.local/share/mise/shims/claude-agent-acp":        true,
		"/home/you/.local/share/mise/shims/node":                    true,
		"/opt/asdf/shims/node":                                      true,
		"/home/you/.local/share/mise/installs/claude/latest/claude": false,
		"/home/you/.local/share/agentbox/tools/.local/bin/claude":   false,
		"/usr/bin/node":                       false,
		"/home/you/shims-are-not-here/claude": false,
		"":                                    false,
	} {
		if got := Is(path); got != want {
			t.Errorf("Is(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestWithoutShimsDropsOnlyShimDirectories(t *testing.T) {
	t.Parallel()
	sep := string(os.PathListSeparator)
	path := strings.Join([]string{"/home/you/.local/share/mise/shims", "/usr/bin", "/opt/asdf/shims", "", "/home/you/.local/bin", "/home/you/shims-not"}, sep)
	got := filepath.SplitList(WithoutShims(path))
	want := []string{"/usr/bin", "/home/you/.local/bin", "/home/you/shims-not"}
	if !slices.Equal(got, want) {
		t.Errorf("WithoutShims() = %q, want %q", got, want)
	}
}

func TestResolveLeavesRealBinariesAlone(t *testing.T) {
	t.Parallel()
	real := "/home/you/.local/share/mise/installs/claude/latest/claude"
	if got := Resolve(real); got != real {
		t.Errorf("Resolve(%q) = %q, want it unchanged", real, got)
	}
	if got := Resolve(""); got != "" {
		t.Errorf("Resolve(\"\") = %q", got)
	}
}

// A mise machine: shims that are symlinks to mise, which fails as a shim for
// want of configuration and answers `mise which` only when resolves is true.
// whichDir records where `mise which` was asked from.
type miseMachine struct {
	shims, mise, real, whichDir string
}

func newMiseMachine(t *testing.T, resolves bool, tools ...string) miseMachine {
	t.Helper()
	root := t.TempDir()
	mm := miseMachine{
		shims:    filepath.Join(root, "mise", "shims"),
		mise:     filepath.Join(root, "mise-bin", "mise"),
		real:     filepath.Join(root, "mise", "installs", "fake-tool", "24", "bin"),
		whichDir: filepath.Join(root, "which-dir"),
	}
	which := `echo "mise ERROR No version is set for shim: $2" >&2; exit 1`
	if resolves {
		which = fmt.Sprintf(`pwd > %q; echo %s/"$2"`, mm.whichDir, mm.real)
	}
	writeExec(t, mm.mise, fmt.Sprintf(`#!/bin/sh
name=$(basename "$0")
if [ "$name" != mise ]; then
	echo "mise ERROR No version is set for shim: $name" >&2
	echo "mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information" >&2
	exit 1
fi
if [ "$1" = which ]; then %s; exit 0; fi
exit 1
`, which))
	if err := os.MkdirAll(mm.shims, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if err := os.Symlink(mm.mise, filepath.Join(mm.shims, tool)); err != nil {
			t.Fatal(err)
		}
		writeExec(t, filepath.Join(mm.real, tool), "#!/bin/sh\n")
	}
	return mm
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// pathOf is a PATH of dirs, then the system's, which the fake mise's own
// script needs.
func pathOf(dirs ...string) string {
	return strings.Join(append(dirs, "/usr/bin", "/bin"), string(os.PathListSeparator))
}

// mise answers for the shim from the user's home, where their global versions
// are, not from the directory the process was started in: a desktop app's is
// often the filesystem root.
func TestResolveAsksMiseFromTheUsersHome(t *testing.T) {
	mm := newMiseMachine(t, true, "fake-tool")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", pathOf(mm.shims, filepath.Dir(mm.mise)))
	if got, want := Resolve(filepath.Join(mm.shims, "fake-tool")), filepath.Join(mm.real, "fake-tool"); got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
	asked, _ := os.ReadFile(mm.whichDir)
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(asked))); got != mustEval(t, home) {
		t.Errorf("mise was asked from %q, want the home %q", got, home)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// A shim that can't be resolved is not a tool the chat can run, so it reports
// nothing. Reporting the shim would hand the chat something that fails the
// moment it starts.
func TestResolveReportsNothingWhenItCannotResolve(t *testing.T) {
	t.Run("no mise to ask", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got := Resolve("/home/you/.local/share/mise/shims/claude-agent-acp"); got != "" {
			t.Errorf("Resolve() = %q, want nothing", got)
		}
	})
	t.Run("mise has no version", func(t *testing.T) {
		mm := newMiseMachine(t, false, "fake-tool")
		t.Setenv("PATH", pathOf(mm.shims, filepath.Dir(mm.mise)))
		if got := Resolve(filepath.Join(mm.shims, "fake-tool")); got != "" {
			t.Errorf("Resolve() = %q, want nothing", got)
		}
	})
}

// Mise's shims are symlinks to it, which finds it when a desktop launcher
// started the process with the shims on its PATH and not mise itself.
func TestResolveFindsMiseThroughTheShimsLink(t *testing.T) {
	mm := newMiseMachine(t, true, "fake-tool")
	t.Setenv("PATH", pathOf(mm.shims))
	if got, want := Resolve(filepath.Join(mm.shims, "fake-tool")), filepath.Join(mm.real, "fake-tool"); got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestLookFindsARealBinary(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "a-real-tool")
	writeExec(t, tool, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir)
	if got := Look("a-real-tool"); got != tool {
		t.Errorf("Look() = %q, want %q", got, tool)
	}
	if got := Look("no-such-tool"); got != "" {
		t.Errorf("Look(no-such-tool) = %q", got)
	}
}

func TestLookResolvesAShimAndPassesOverOneItCannot(t *testing.T) {
	t.Run("resolves", func(t *testing.T) {
		mm := newMiseMachine(t, true, "fake-tool")
		t.Setenv("PATH", pathOf(mm.shims, filepath.Dir(mm.mise)))
		if got, want := Look("fake-tool"), filepath.Join(mm.real, "fake-tool"); got != want {
			t.Errorf("Look() = %q, want mise's real %q", got, want)
		}
	})
	t.Run("does not", func(t *testing.T) {
		mm := newMiseMachine(t, false, "fake-tool")
		t.Setenv("PATH", pathOf(mm.shims, filepath.Dir(mm.mise)))
		if got := Look("fake-tool"); got != "" {
			t.Errorf("Look() = %q, want nothing: the shim fails here", got)
		}
	})
	t.Run("falls through to a real one after it", func(t *testing.T) {
		mm := newMiseMachine(t, false, "fake-tool")
		system := t.TempDir()
		writeExec(t, filepath.Join(system, "fake-tool"), "#!/bin/sh\n")
		t.Setenv("PATH", pathOf(mm.shims, filepath.Dir(mm.mise), system))
		if got, want := Look("fake-tool"), filepath.Join(system, "fake-tool"); got != want {
			t.Errorf("Look() = %q, want the system's %q", got, want)
		}
	})
}
