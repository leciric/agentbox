package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"agentbox/internal/cursor"
)

// The daemon does three Cursor jobs on the host rather than in an agent:
// listing the models the sign-in can run (for Settings and the lead, before
// any Cursor chat has started), checking an API key someone typed in, and
// Cursor's browser sign-in. All three are the adapter script's own commands,
// so they need what it needs: Node and @cursor/sdk, installed the first time
// into AgentBox's tools directory beside the lead's Claude Code adapter
// (hosttools.go), never on the user's PATH.

func (m *Manager) cursorHome() string { return filepath.Join(m.toolsHome(), "cursor") }

// CursorHelper returns the script's host commands, installing Node and the
// pinned SDK first when this machine doesn't have them. status reports what
// it installs.
func (m *Manager) CursorHelper(ctx context.Context, status func(string)) (cursor.Helper, error) {
	ctx, cancel := context.WithTimeout(ctx, hostInstallTimeout)
	defer cancel()
	nodeBin, err := m.ensureNode(ctx, status)
	if err != nil {
		return cursor.Helper{}, err
	}
	home := m.cursorHome()
	if m.cursorSDKVersion() != cursor.SDKVersion() {
		status("Installing Cursor's SDK, once for this machine")
		if err := os.MkdirAll(home, 0o700); err != nil {
			return cursor.Helper{}, err
		}
		cmd := exec.CommandContext(ctx, filepath.Join(nodeBin, "npm"), "install", "--silent", "--no-fund", "--no-audit", "--prefix", home, "@cursor/sdk@"+cursor.SDKVersion())
		cmd.Env = append(m.installEnv(), "PATH="+nodeBin+string(os.PathListSeparator)+os.Getenv("PATH")+string(os.PathListSeparator)+systemPath)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return cursor.Helper{}, installError("@cursor/sdk", err, out.String(), ctx)
		}
		if got := m.cursorSDKVersion(); got != cursor.SDKVersion() {
			return cursor.Helper{}, fmt.Errorf("installing @cursor/sdk: it reported success but %s has %q: %s", home, got, tail(out.String()))
		}
	}
	// Written every time, so the script always matches this binary.
	script := filepath.Join(home, cursor.ScriptName)
	if err := os.WriteFile(script, cursor.Script, 0o600); err != nil {
		return cursor.Helper{}, err
	}
	return cursor.Helper{Node: filepath.Join(nodeBin, "node"), Script: script, SDK: home}, nil
}

// cursorSDKVersion is the version of the SDK in AgentBox's tools directory,
// or "" when there is none.
func (m *Manager) cursorSDKVersion() string {
	raw, err := os.ReadFile(filepath.Join(m.cursorHome(), "node_modules", "@cursor", "sdk", "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return ""
	}
	return manifest.Version
}
