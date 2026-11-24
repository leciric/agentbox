// Package shim finds the programs a version manager's shims stand for.
//
// A tool installed with mise (or asdf) is usually reached through a shim, and a
// shim is the manager itself: it works out what to run from its own
// configuration, which it looks for under HOME and in the directory it is
// started from. AgentBox runs AI tools and their adapters from folders of its
// own (the project chat's private HOME, the Home chat's folder), where nothing
// of the user's configuration is in reach, so a shim there fails with "mise
// ERROR" and takes the adapter down with it, before the chat says anything.
// What runs there has to be the real executable, which `mise which` names.
package shim

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// whichTimeout bounds a `mise which`, which a machine finds its tools with
// before the chat starts.
const whichTimeout = 10 * time.Second

// Is reports whether path is in a version manager's shim directory.
func Is(path string) bool {
	return path != "" && strings.Contains(path, string(os.PathSeparator)+"shims"+string(os.PathSeparator))
}

// WithoutShims is a PATH with every version manager's shim directory taken
// out: whatever inherits it would otherwise find a shim before the real tool.
func WithoutShims(path string) string {
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if dir != "" && !Is(filepath.Join(dir, "x")) {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// Resolve turns a shim into the real executable, and leaves anything else
// alone. mise answers `mise which` with the real path, and that path needs no
// manager to run. It is asked from the user's home folder, where their global
// versions and ~/.tool-versions apply, not from wherever this process happens
// to have been started.
//
// It returns "" for a shim mise can't resolve from there either (no version
// set, a config it doesn't trust, no mise to ask): that is not something the
// chat can run, and the caller installs AgentBox's own copy or reports the tool
// missing.
func Resolve(path string) string {
	if !Is(path) {
		return path
	}
	mise := findMise(path)
	if mise == "" {
		return ""
	}
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), whichTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mise, "which", name)
	cmd.Dir, _ = os.UserHomeDir()
	out, err := cmd.Output()
	real := strings.TrimSpace(string(out))
	if err != nil || real == "" || Is(real) {
		return ""
	}
	return real
}

// Look finds the command name on the PATH as something that runs without a
// version manager: the first executable of that name that isn't a shim, or
// the real one a shim resolves to. A shim mise can't resolve is passed over for
// whatever comes after it on the PATH, so "" is a command with nothing that
// runs here.
func Look(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		path, err := exec.LookPath(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if real := Resolve(path); real != "" {
			return real
		}
	}
	return ""
}

// findMise is the mise that answers for a shim: the PATH's, else the one the
// shim links to. A desktop launcher can start a process with the shims on its
// PATH but not mise itself, and mise's shims are symlinks to it.
func findMise(shim string) string {
	if mise, err := exec.LookPath("mise"); err == nil {
		return mise
	}
	if real, err := filepath.EvalSymlinks(shim); err == nil && strings.TrimSuffix(filepath.Base(real), ".exe") == "mise" && usable(real) {
		return real
	}
	return ""
}

func usable(path string) bool {
	info, err := os.Stat(path)
	// Windows has no execute bits: a file there runs by its extension.
	return err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0)
}
