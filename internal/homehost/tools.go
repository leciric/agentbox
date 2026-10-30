package homehost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// toolCommands are the AI tools' own command lines: what says a tool is
// installed on the host.
var toolCommands = map[string]string{"claude": "claude", "codex": "codex", "opencode": "opencode"}

// FindTools finds the AI tools on this computer, and whether each has a login
// of its own.
func FindTools() map[string]Tool {
	home, _ := os.UserHomeDir()
	out := map[string]Tool{}
	for name, command := range toolCommands {
		path, err := exec.LookPath(command)
		if err != nil {
			out[name] = Tool{}
			continue
		}
		out[name] = Tool{Path: path, Login: hasLogin(name, home, os.Getenv)}
	}
	return out
}

// hasLogin reports whether a tool has a login of its own on this computer.
// Without one, Claude Code runs on AgentBox's account (Spec.LoginEnv).
func hasLogin(tool, home string, getenv func(string) string) bool {
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
	dir := func(env, fallback string) string {
		if v := getenv(env); v != "" {
			return v
		}
		return filepath.Join(home, fallback)
	}
	switch tool {
	case "claude":
		for _, env := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"} {
			if getenv(env) != "" {
				return true
			}
		}
		if exists(filepath.Join(dir("CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json")) {
			return true
		}
		// On a Mac its login is in the keychain.
		return runtime.GOOS == "darwin" && keychainHas("Claude Code-credentials")
	case "codex":
		return getenv("OPENAI_API_KEY") != "" || getenv("CODEX_API_KEY") != "" ||
			exists(filepath.Join(dir("CODEX_HOME", ".codex"), "auth.json"))
	case "opencode":
		// It has free models, and keys for others in its own config: it
		// runs either way.
		return true
	}
	return false
}

func keychainHas(service string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "security", "find-generic-password", "-s", service).Run() == nil
}

// WidenPath puts where the AI tools are usually installed on this process's
// PATH, which the tools it starts inherit. An app opened from the Dock or a
// desktop launcher gets a bare PATH; the user's login shell has theirs.
func WidenPath() {
	home, _ := os.UserHomeDir()
	var dirs []string
	if runtime.GOOS != "windows" {
		dirs = append(dirs, loginShellPath()...)
	}
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	for _, rel := range []string{".local/bin", ".claude/local", ".npm-global/bin", ".bun/bin", ".opencode/bin", ".volta/bin", ".local/share/mise/shims"} {
		dirs = append(dirs, filepath.Join(home, filepath.FromSlash(rel)))
	}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
	default:
		dirs = append(dirs, "/usr/local/bin", "/usr/bin", "/bin")
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	_ = os.Setenv("PATH", strings.Join(out, string(os.PathListSeparator)))
}

// loginShellPath is PATH as the user's login shell sets it.
func loginShellPath() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Between markers: a profile that prints something can't mix it in.
	out, err := exec.CommandContext(ctx, shell, "-ilc", `printf '\n@@PATH@@%s@@PATH@@\n' "$PATH"`).Output()
	if err != nil {
		return nil
	}
	s := string(out)
	start := strings.Index(s, "@@PATH@@")
	if start < 0 {
		return nil
	}
	s = s[start+len("@@PATH@@"):]
	end := strings.Index(s, "@@PATH@@")
	if end < 0 {
		return nil
	}
	return filepath.SplitList(s[:end])
}
