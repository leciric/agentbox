// Package opencode asks the OpenCode command line what it can run.
//
// OpenCode's models are named "provider/model", and which ones exist depends
// on which providers the login has keys for — so, like Claude Code's menu,
// the only honest list is one OpenCode itself named. `opencode models` prints
// it, and the daemon remembers it under state.SettingOpenCodeModelChoices so
// that the app and a project's lead have something true to choose from before
// any OpenCode chat has ever started.
//
// It runs with AgentBox's own login directory as XDG_DATA_HOME and an empty
// config directory as XDG_CONFIG_HOME (see credentials.Store.OpenCodeDataHome
// and HostEnv), never the host's ~/.local/share/opencode or opencode.json: the
// answer has to be about the login agents get, not about the user's own. The
// host's config may also be written for the other release, which the one being
// asked refuses to read at all.
package opencode

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Command is the OpenCode command line's name, on PATH and in the release.
const Command = "opencode"

// modelsTimeout bounds the call. `opencode models` starts OpenCode's own
// runtime and asks each configured provider what it offers, so it is slower
// than a version check and still nothing like a turn.
const modelsTimeout = 45 * time.Second

// Models lists the "provider/model" ids OpenCode says the stored login can
// run, running command (Installer.Ensure's, or EnsureOpenCode2's) with dataHome
// as its XDG data directory. OpenCode 2 is asked in standalone mode: its other
// commands talk to a background service, which AgentBox runs for no chat.
func Models(ctx context.Context, command, dataHome string, standalone bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	args := []string{"models"}
	if standalone {
		args = append(args, "--standalone")
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = HostEnv(dataHome, cmd.Environ())
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		if line := lastLine(errs.String()); line != "" {
			return nil, fmt.Errorf("opencode models: %w: %s", err, line)
		}
		return nil, fmt.Errorf("opencode models: %w", err)
	}
	return ParseModels(out.String()), nil
}

// ParseModels reads the ids out of what `opencode models` printed: one
// "provider/model" per line. Anything else on a line — a heading, a warning,
// an empty line — is not an id and is dropped, so a future release that prints
// more than the list costs a shorter menu rather than a menu of nonsense.
func ParseModels(out string) []string {
	var models []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		id := strings.TrimSpace(line)
		provider, model, ok := strings.Cut(id, "/")
		if !ok || provider == "" || model == "" || strings.ContainsAny(id, " \t") {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	return models
}

// Name is how a "provider/model" id reads in a menu: "github-copilot/gpt-5.4"
// becomes "github-copilot / gpt-5.4". OpenCode's own ACP session sends proper
// display names, and they replace these as soon as a chat has run; until then
// the id is all there is, and nothing here invents a friendlier one.
func Name(id string) string {
	provider, model, ok := strings.Cut(id, "/")
	if !ok {
		return id
	}
	return provider + " / " + model
}

// Import gives OpenCode 2 the credentials AgentBox holds for it, in the form
// `opencode auth export` prints. It is OpenCode 2's replacement for writing an
// auth.json: its credentials live in its own database.
func Import(ctx context.Context, command, dataHome string, export []byte) error {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "auth", "import", "--standalone")
	cmd.Env = HostEnv(dataHome, cmd.Environ())
	cmd.Stdin = bytes.NewReader(export)
	var errs bytes.Buffer
	cmd.Stderr = &errs
	if err := cmd.Run(); err != nil {
		if line := lastLine(errs.String()); line != "" {
			return fmt.Errorf("opencode auth import: %w: %s", err, line)
		}
		return fmt.Errorf("opencode auth import: %w", err)
	}
	return nil
}

// Export reads what OpenCode 2 stored in dataHome, in the form `auth import`
// takes. It is how a login made with OpenCode 2 comes back to AgentBox.
func Export(ctx context.Context, command, dataHome string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "auth", "export", "--standalone")
	cmd.Env = HostEnv(dataHome, cmd.Environ())
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		if line := lastLine(errs.String()); line != "" {
			return nil, fmt.Errorf("opencode auth export: %w: %s", err, line)
		}
		return nil, fmt.Errorf("opencode auth export: %w", err)
	}
	return out.Bytes(), nil
}

// hostConfig names the environment variables OpenCode reads that would point
// it at the host's configuration, database or credentials instead of
// AgentBox's own. They are dropped from HostEnv's answer.
var hostConfig = map[string]bool{
	"XDG_DATA_HOME":           true,
	"XDG_CONFIG_HOME":         true,
	"OPENCODE_CONFIG":         true,
	"OPENCODE_CONFIG_CONTENT": true,
	"OPENCODE_CONFIG_DIR":     true,
	"OPENCODE_AUTH_CONTENT":   true,
	"OPENCODE_DB":             true,
}

// HostEnv is the environment for running OpenCode against AgentBox's own
// login rather than the host's. Its data home is dataHome, where its auth.json
// or database is, and its config home is an empty directory under it, so the
// host's ~/.config/opencode/opencode.json is never read: that file may hold
// OpenCode 2's permissions and servers, which OpenCode 1 refuses, and even a
// valid one would have the host's plugins and providers answering instead of
// AgentBox's own login. The variables that point OpenCode at a config,
// database or credentials elsewhere are dropped for the same reason. HOME is
// left alone: caches and logs may still be the user's.
func HostEnv(dataHome string, environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if hostConfig[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "XDG_DATA_HOME="+dataHome, "XDG_CONFIG_HOME="+filepath.Join(dataHome, "config"))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
