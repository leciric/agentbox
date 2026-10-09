package hostos

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// WindowsHomeEnv is the Windows user's home (C:\Users\ana) at its path in
// WSL (/mnt/c/Users/ana), which the Windows front end gives the daemon it
// starts (package hostwsl). It isn't HomeEnv: Windows's home isn't shared at
// the same path, and the Linux side mostly has no business there.
const WindowsHomeEnv = "AGENTBOX_WINDOWS_HOME"

var (
	// C:\x and C:/x; \\?\C:\x and \??\C:\x are how a junction's or a long
	// path's target can read.
	drivePath = regexp.MustCompile(`^(?:[\\/]{2}\?[\\/]|\\\?\?\\)?([A-Za-z]):(?:[\\/](.*))?$`)
	// \\wsl.localhost\<distro>\... and the older \\wsl$\<distro>\...
	wslPath = regexp.MustCompile(`(?i)^[\\/]{2}(?:wsl\.localhost|wsl\$)[\\/]([^\\/]+)(?:[\\/](.*))?$`)
)

// IsWindowsPath reports whether p is, whole, a path on a Windows drive or in a
// WSL distro, as Windows writes them.
func IsWindowsPath(p string) bool {
	return drivePath.MatchString(p) || wslPath.MatchString(p)
}

// LinuxPath is where a Windows path is in WSL: C:\Users\ana is
// /mnt/c/Users/ana, and \\wsl.localhost\AgentBox\home\ana is /home/ana. A Linux
// path is itself. ok is false for a path WSL can't see: another distro's
// files (distro "" takes any), or a network share.
func LinuxPath(p, distro string) (string, bool) {
	if strings.HasPrefix(p, "/") {
		return p, true
	}
	if m := drivePath.FindStringSubmatch(p); m != nil {
		rest := strings.TrimRight(strings.ReplaceAll(m[2], `\`, "/"), "/")
		out := "/mnt/" + strings.ToLower(m[1])
		if rest != "" {
			out += "/" + rest
		}
		return out, true
	}
	if m := wslPath.FindStringSubmatch(p); m != nil {
		if distro != "" && !strings.EqualFold(m[1], distro) {
			return "", false
		}
		return "/" + strings.TrimRight(strings.ReplaceAll(m[2], `\`, "/"), "/"), true
	}
	return "", false
}

// windowsUsers is where Windows keeps its users' homes, in WSL. A variable
// for tests.
var windowsUsers = "/mnt/c/Users"

// WindowsHome is the Windows user's home at its path in WSL: what the front
// end said (WindowsHomeEnv), else, for a daemon started some other way,
// /mnt/c/Users/<the Linux user> when there is one, since AgentBox's distro
// names its user after the Windows one. "" outside WSL.
func WindowsHome() string {
	if !WSL() {
		return ""
	}
	if h := os.Getenv(WindowsHomeEnv); h != "" {
		return h
	}
	name := os.Getenv("USER")
	if u, err := user.Current(); name == "" && err == nil {
		name = u.Username
	}
	if name == "" {
		return ""
	}
	home := filepath.Join(windowsUsers, name)
	if st, err := os.Stat(home); err == nil && st.IsDir() {
		return home
	}
	return ""
}
