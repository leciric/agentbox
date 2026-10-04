package machines

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ServerName is the MCP server's name in the AI tools' configuration, which
// names its tools there too: mcp__machine__screenshot.
const ServerName = "machine"

// Target is one AI tool's user-scope configuration.
type Target struct {
	Tool string // claude or codex
	Path string
}

// Targets are the AI tools installed for this user: Claude Code's
// ~/.claude.json and Codex's ~/.codex/config.toml, each when the tool's
// command or its configuration is there.
func Targets(home string) []Target {
	var out []Target
	claudeDir := home
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		claudeDir = d
	}
	if present("claude", filepath.Join(claudeDir, ".claude.json")) {
		out = append(out, Target{"claude", filepath.Join(claudeDir, ".claude.json")})
	}
	codexDir := filepath.Join(home, ".codex")
	if d := os.Getenv("CODEX_HOME"); d != "" {
		codexDir = d
	}
	if present("codex", codexDir) {
		out = append(out, Target{"codex", filepath.Join(codexDir, "config.toml")})
	}
	return out
}

func present(command, path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	_, err := exec.LookPath(command)
	return err == nil
}

// Install adds the server to t, running bin, or puts the one there right.
// changed says whether the file changed.
func Install(t Target, bin string) (changed bool, err error) {
	args := []string{"machines", "mcp", "--tool", t.Tool}
	switch t.Tool {
	case "claude":
		return editClaude(t.Path, map[string]any{"type": "stdio", "command": bin, "args": args, "env": map[string]any{}})
	case "codex":
		return editCodex(t.Path, codexTable(bin, args))
	}
	return false, fmt.Errorf("no AI tool %q", t.Tool)
}

// Uninstall removes the server from t.
func Uninstall(t Target) (changed bool, err error) {
	switch t.Tool {
	case "claude":
		return editClaude(t.Path, nil)
	case "codex":
		return editCodex(t.Path, "")
	}
	return false, fmt.Errorf("no AI tool %q", t.Tool)
}

// editClaude sets mcpServers.machine in Claude Code's ~/.claude.json, or
// removes it when server is nil, leaving the rest of the file as it was.
func editClaude(path string, server map[string]any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	doc := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := doc["mcpServers"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return false, fmt.Errorf("%s: mcpServers: %w", path, err)
		}
	}
	old, had := servers[ServerName]
	if server == nil {
		if !had {
			return false, nil
		}
		delete(servers, ServerName)
	} else {
		raw, err := json.Marshal(server)
		if err != nil {
			return false, err
		}
		if had && jsonEqual(old, raw) {
			return false, nil
		}
		servers[ServerName] = raw
	}
	raw, err := json.Marshal(servers)
	if err != nil {
		return false, err
	}
	doc["mcpServers"] = raw
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	return true, writeFile(path, append(out, '\n'))
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(ax, by)
}

// codexTable is the server as Codex's TOML. machine_start may build the
// image, which takes minutes, so a call may take long.
func codexTable(bin string, args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [%s]\nstartup_timeout_sec = 30\ntool_timeout_sec = 1800\n",
		ServerName, bin, strings.Join(quoted, ", "))
}

// editCodex replaces the server's table in Codex's config.toml with table,
// or removes it when table is "". Every other line stays as it was.
func editCodex(path, table string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var kept []string
	ours := false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" {
			continue
		}
		if h := strings.TrimSpace(line); strings.HasPrefix(h, "[") {
			ours = isOurTable(h)
		}
		if !ours {
			kept = append(kept, line)
		}
	}
	rest := strings.TrimRight(strings.Join(kept, ""), "\n")
	out := rest
	if table != "" {
		if out != "" {
			out += "\n\n"
		}
		out += table
	} else if out != "" {
		out += "\n"
	}
	if out == string(data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, writeFile(path, []byte(out))
}

// isOurTable reports whether a TOML header is the server's table or one of
// its subtables ([mcp_servers.machine.env]).
func isOurTable(header string) bool {
	h := strings.Trim(header, "[] \t")
	if i := strings.Index(h, "#"); i >= 0 {
		h = strings.TrimSpace(strings.TrimRight(h[:i], "] \t"))
	}
	for _, name := range []string{ServerName, `"` + ServerName + `"`} {
		p := "mcp_servers." + name
		if h == p || strings.HasPrefix(h, p+".") {
			return true
		}
	}
	return false
}

// writeFile replaces path at once, keeping its mode.
func writeFile(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
