// Package opencode asks the OpenCode command line what it can run.
//
// OpenCode's models are named "provider/model", and which ones exist depends
// on which providers the login has keys for — so, like Claude Code's menu,
// the only honest list is one OpenCode itself named. `opencode models` prints
// it, and the daemon remembers it under state.SettingOpenCodeModelChoices so
// that the app and a project's lead have something true to choose from before
// any OpenCode chat has ever started.
//
// It runs with AgentBox's own login directory as XDG_DATA_HOME (see
// credentials.Store.OpenCodeDataHome), never the host's ~/.local/share/opencode:
// the answer has to be about the login agents get, not about the user's own.
package opencode

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Command is the OpenCode command line, as it is found on the host's PATH.
const Command = "opencode"

// modelsTimeout bounds the call. `opencode models` starts OpenCode's own
// runtime and asks each configured provider what it offers, so it is slower
// than a version check and still nothing like a turn.
const modelsTimeout = 45 * time.Second

// Models lists the "provider/model" ids OpenCode says the stored login can
// run, using dataHome as its XDG data directory. An OpenCode that isn't
// installed on this machine is an ordinary error: there is then no list, which
// is the same position as an installation that has never logged in.
func Models(ctx context.Context, dataHome string) ([]string, error) {
	path, err := exec.LookPath(Command)
	if err != nil {
		return nil, fmt.Errorf("opencode isn't installed on this machine: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "models")
	// A fresh environment but for what a command needs to run at all: HOME
	// stays the user's (OpenCode writes caches there), while XDG_DATA_HOME is
	// AgentBox's, which is where its auth.json is.
	cmd.Env = append(cmd.Environ(), "XDG_DATA_HOME="+dataHome)
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

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
