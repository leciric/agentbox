// Package cursor is AgentBox's side of Cursor, the fourth AI tool: the ACP
// adapter it runs agents' chats through, and the one-off jobs the daemon has
// that same script do on the host (list models, sign in, check an API key).
//
// Cursor is driven through its TypeScript SDK (@cursor/sdk), not its command
// line, so there is no published ACP adapter to pin the way there is for
// Claude Code and Codex: the adapter is adapter.mjs here, embedded in the
// binary and written next to the SDK wherever it runs. The SDK itself is pinned
// in internal/image/tools.txt, which puts it in every base image and, through
// image.UpdateTools, into bases built before it was there.
package cursor

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"agentbox/internal/image"
)

// Script is the adapter: run with no arguments it speaks ACP on stdin and
// stdout; with "models", "login" or "check-key" it does one job and exits.
//
//go:embed adapter.mjs
var Script []byte

// ScriptName is the file the script is written to, in an agent's
// ~/.local/share/agentbox and in AgentBox's own tools directory on the host.
const ScriptName = "agentbox-cursor-acp.mjs"

// SDKEnv names the directory the SDK was installed into, which the script
// imports it from: mise's install directory in an agent, an npm prefix on the
// host.
const SDKEnv = "AGENTBOX_CURSOR_SDK"

// SDKPackage is mise's name for the SDK with the version AgentBox pins.
var SDKPackage = image.Pin("npm:@cursor/sdk")

// SDKVersion is the pinned version alone, as npm names it.
func SDKVersion() string { return SDKPackage[strings.LastIndex(SDKPackage, "@")+1:] }

// Timeouts for the host jobs. Listing models and checking a key are one
// request each; a sign-in waits for someone to finish it in a browser, and
// Cursor's own poll gives up after about twenty minutes.
const (
	requestTimeout = 45 * time.Second
	LoginTimeout   = 15 * time.Minute
)

// Helper runs the script's one-off commands with a node and an SDK the host
// has (agent.Manager.CursorHelper installs them).
type Helper struct {
	Node   string // the node binary
	Script string // the script's path
	SDK    string // the npm prefix the SDK is installed under
}

func (h Helper) command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, h.Node, append([]string{h.Script}, args...)...)
	cmd.Env = append(os.Environ(), SDKEnv+"="+h.SDK)
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

// Model is one entry of Cursor's model menu: its id, the name Cursor shows,
// and the effort levels it takes (none for most).
type Model struct {
	Value       string   `json:"value"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Efforts     []string `json:"efforts"`
}

// Models asks Cursor which models apiKey can run.
func (h Helper) Models(ctx context.Context, apiKey string) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var out, errs bytes.Buffer
	cmd := h.command(ctx, []string{"CURSOR_API_KEY=" + apiKey}, "models")
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return nil, failed("listing Cursor's models", err, errs.String())
	}
	var models []Model
	if err := json.Unmarshal(lastLine(out.String()), &models); err != nil {
		return nil, fmt.Errorf("listing Cursor's models: %w", err)
	}
	return models, nil
}

// CheckKey asks Cursor whose apiKey is and, when Cursor knows it, saves it to
// storePath in the SDK's own credentials format. It returns the account's email.
func (h Helper) CheckKey(ctx context.Context, apiKey, storePath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var out, errs bytes.Buffer
	cmd := h.command(ctx, nil, "check-key", storePath)
	cmd.Stdin = strings.NewReader(apiKey)
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return "", failed("checking the Cursor API key", err, errs.String())
	}
	var r struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(lastLine(out.String()), &r)
	return r.Email, nil
}

// Login runs Cursor's browser sign-in, which mints an API key for AgentBox
// and saves it to storePath. url is called with the page to open as soon as
// Cursor names it; Login returns once the sign-in is finished, with the
// account's email, or when ctx ends.
func (h Helper) Login(ctx context.Context, storePath string, url func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, LoginTimeout)
	defer cancel()
	var errs bytes.Buffer
	cmd := h.command(ctx, nil, "login", storePath)
	cmd.Stderr = &errs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var email string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var line struct {
			LoginURL *string `json:"loginUrl"`
			Email    *string `json:"email"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		if line.LoginURL != nil {
			url(*line.LoginURL)
		}
		if line.Email != nil {
			email = *line.Email
		}
	}
	if err := cmd.Wait(); err != nil {
		return "", failed("signing in to Cursor", err, errs.String())
	}
	return email, nil
}

// failed says why a job failed in the script's own words: its last line on
// stderr, which is the error it exits with (the SDK's log lines come before it).
func failed(what string, err error, stderr string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: it took too long", what)
	}
	if line := strings.TrimSpace(string(lastLine(stderr))); line != "" {
		return fmt.Errorf("%s: %s", what, line)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func lastLine(s string) []byte {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return []byte(strings.TrimSpace(lines[len(lines)-1]))
}
