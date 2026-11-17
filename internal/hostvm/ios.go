package hostvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentbox/internal/iossim"
)

// iosHost reports whether a VM can have the iOS Simulator beside it: a Mac's
// Lima VM, which reaches the Mac's 127.0.0.1 at host.lima.internal, where the
// simulator helper listens (internal/iossim). A variable for tests.
var iosHost = func(v *VM) bool { return runtime.GOOS == "darwin" && v.CHV == nil }

// frontVersion is this agentbox's version, which the helper it starts has to
// match: set by Main.
var frontVersion = "dev"

// iosStateFile is the simulator helper's state file, which the VM's daemon
// reads at the same path in the shared home: "" when there's no helper, or
// it's somewhere the VM can't see.
func (v *VM) iosStateFile() string {
	if !iosHost(v) || v.Home == "" || v.Paths.Config == "" {
		return ""
	}
	file := iossim.StateFile(v.Paths.Config)
	if rel, err := filepath.Rel(v.Home, file); err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return file
}

// ensureIOS starts the simulator helper unless it runs, as this version: on
// every command the front end forwards, and on the vm commands the app runs
// to start the VM and to watch it, so it is up whenever the app is. It never
// stops a command: an agent that can't reach it is told why.
func (v *VM) ensureIOS(ctx context.Context) {
	if v.iosStateFile() == "" {
		return
	}
	exe, err := os.Executable()
	// A test binary would run its tests again as the helper.
	if err != nil || strings.HasSuffix(exe, ".test") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = iossim.Ensure(ctx, iossim.EnsureOptions{
		ConfigDir: v.Paths.Config,
		Roots:     []string{v.Paths.Worktrees()},
		Version:   frontVersion,
		Exe:       exe,
	})
	if err != nil && v.Log != nil {
		_, _ = fmt.Fprintln(v.Log, "note: the iOS Simulator won't be available:", err)
	}
}

// ensuresIOS reports whether a vm command starts the helper: vm start, and
// vm power, which the app asks every few seconds. (Every command the front
// end forwards starts it too.)
func ensuresIOS(args []string) bool {
	return len(args) > 1 && args[0] == "vm" && (args[1] == "start" || args[1] == "power")
}
