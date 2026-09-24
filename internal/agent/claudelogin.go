package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/creack/pty"
)

// Logging in to Claude Code is `claude setup-token`, and AgentBox runs it
// itself rather than sending you to a terminal to paste what it prints (D59).
// What that takes was found by running Claude Code 2.1.273 on a real machine:
//
//   - It needs a pty. With pipes for stdin, stdout and stderr it prints
//     nothing at all and waits forever — it draws its screens with ink, which
//     gives up when stdout isn't a terminal. There is no non-interactive mode.
//   - It needs a wide one. ink wraps at the terminal's width, and both the
//     sign-in URL and the token are longer than 80 columns, so a narrow pty
//     breaks them across lines and there is no reliable way to join them back
//     up. setupTokenCols is wider than either.
//   - It opens the browser itself, through $BROWSER, with or without a
//     DISPLAY. That URL comes back to a callback server the CLI listens on, on
//     a localhost port, so approving in the browser finishes the login with
//     nothing to paste. Pointing $BROWSER at a script of our own is how
//     AgentBox learns the URL to open.
//   - The URL it *prints* is a second, parallel login, whose callback is on
//     claude.com and which ends in a code to hand back. It is the fallback for
//     when the browser can't reach this machine, and the only thing the CLI
//     ever asks for.
//   - It needs a HOME of its own, which is the point: the user's ~/.claude is
//     never touched (D6).

const (
	// setupTokenCols is wide enough that Claude Code wraps neither the sign-in
	// URL nor the token it prints.
	setupTokenCols = 400
	// setupTokenTimeout is how long AgentBox waits for the login to be
	// approved before giving up, so a browser tab closed and forgotten doesn't
	// leave a Claude Code running for the rest of the session.
	setupTokenTimeout = 10 * time.Minute
)

// SetupTokenEvents reports what a login needs while it runs. Every field is
// optional, and each is called from the goroutine running the login.
type SetupTokenEvents struct {
	// Status is progress worth showing: installing Claude Code, waiting.
	Status func(detail string)
	// Browser is the page to approve, as soon as Claude Code asks for it to be
	// opened. Approving there finishes the login on its own.
	Browser func(url string)
	// Paste is the fallback page, whose approval ends in a code that has to go
	// back to Claude Code through the codes channel.
	Paste func(url string)
}

// SetupToken runs `claude setup-token` and returns the long-lived token it
// prints, for the caller to store. It installs Claude Code first if this
// machine has none, reports the page to approve through ev, and reads codes
// for the fallback flow, where the user copies a code out of the browser.
//
// It never returns before the user has approved the login, or before
// setupTokenTimeout, so callers run it as a job rather than in a request.
func (m *Manager) SetupToken(ctx context.Context, ev SetupTokenEvents, codes <-chan string) (string, error) {
	status := ev.Status
	if status == nil {
		status = func(string) {}
	}
	claude, err := m.ensureClaude(ctx, status)
	if err != nil {
		return "", err
	}

	home, cleanup, err := m.loginHome()
	if err != nil {
		return "", err
	}
	defer cleanup()
	browser, urls, err := browserHook(home)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, setupTokenTimeout)
	defer cancel()
	m.logf("Logging in to Claude Code with %s, under %s", claude, home)
	cmd := exec.CommandContext(ctx, claude, "setup-token")
	cmd.Env = append(m.loginEnv(home), "BROWSER="+browser)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: setupTokenCols, Rows: 60})
	if err != nil {
		return "", fmt.Errorf("running %s setup-token: %w", claude, err)
	}
	defer ptmx.Close()
	// Claude Code has no more to say once it has printed the token, and waits
	// for a keypress on some paths, so the login ends when this returns.
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	done := make(chan struct{})
	defer close(done)
	go watchBrowserURL(done, urls, ev.Browser)
	go typeCodes(done, ptmx, codes)

	token, out, err := readSetupToken(ptmx, ev.Paste)
	if token != "" {
		return token, nil
	}
	return "", setupTokenError(ctx, err, out)
}

// loginHome is a HOME for one login, thrown away afterwards, so nothing Claude
// Code writes while logging in outlives it and none of it lands in the user's
// own ~/.claude (D6).
func (m *Manager) loginHome() (string, func(), error) {
	if err := os.MkdirAll(m.toolsHome(), 0o700); err != nil {
		return "", nil, err
	}
	home, err := os.MkdirTemp(m.toolsHome(), "login-")
	if err != nil {
		return "", nil, err
	}
	return home, func() { os.RemoveAll(home) }, nil
}

// browserHook is a $BROWSER that writes down the URL instead of opening it:
// the app opens it in the system browser itself, and a terminal prints it.
func browserHook(home string) (script, urls string, err error) {
	script, urls = filepath.Join(home, "open-browser"), filepath.Join(home, "browser-url")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> %s\n", shellQuote(urls))
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return "", "", err
	}
	return script, urls, nil
}

// watchBrowserURL reports the first URL the browser hook was given. Claude Code
// writes it once, a moment after it starts, so this polls rather than waiting
// on the pty, where the same URL only appears wrapped in escape sequences.
func watchBrowserURL(done <-chan struct{}, path string, report func(string)) {
	if report == nil {
		return
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
			if line != "" {
				report(line)
				return
			}
		}
	}
}

// typeCodes hands Claude Code the codes copied out of the browser, as if they
// had been typed at its prompt.
func typeCodes(done <-chan struct{}, ptmx io.Writer, codes <-chan string) {
	for {
		select {
		case <-done:
			return
		case code, ok := <-codes:
			if !ok {
				return
			}
			io.WriteString(ptmx, code+"\r")
		}
	}
}

// readSetupToken reads Claude Code's screens until it prints the token, and
// reports the fallback URL on the way. It keeps the whole output, rather than
// parsing what each read happens to hold, because an escape sequence — and the
// URL inside it — is split across reads as often as not.
func readSetupToken(ptmx io.Reader, paste func(string)) (token, out string, err error) {
	var raw []byte
	buf := make([]byte, 4096)
	var reported string
	for {
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			raw = append(raw, buf[:n]...)
			text := string(raw)
			if token := setupTokenIn(text); token != "" {
				return token, cleanTerminal(text), nil
			}
			if u := loginURL(text); paste != nil && u != "" && u != reported {
				reported = u
				paste(u)
			}
		}
		if readErr != nil {
			return "", cleanTerminal(string(raw)), readErr
		}
	}
}

// setupTokenError says what went wrong in the words of the thing that did:
// a login nobody approved, a login cancelled, or Claude Code giving up. The pty
// closes with EIO when the command exits, which says nothing on its own, so
// what it last drew goes in the message.
func setupTokenError(ctx context.Context, err error, out string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("the Claude Code login wasn't approved within %s: start it again", setupTokenTimeout)
	case ctx.Err() != nil:
		return errors.New("the Claude Code login was cancelled")
	}
	if last := lastLines(out, 3); last != "" {
		return fmt.Errorf("claude setup-token ended without a token: %s", last)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("claude setup-token ended without a token: %w", err)
	}
	return errors.New("claude setup-token ended without a token")
}

var (
	// setupTokenPattern is the long-lived token Claude Code prints. Anything
	// shorter than this is a mention of the prefix rather than a token.
	setupTokenPattern = regexp.MustCompile(`sk-ant-oat[0-9A-Za-z_-]{20,}`)
	// osc8Pattern is a terminal hyperlink, "\e]8;<params>;<url>\a": the whole
	// URL, before the terminal wrapped the text under it.
	osc8Pattern = regexp.MustCompile("\x1b]8;[^;\x07\x1b]*;([^\x07\x1b]+)")
	// urlPattern is a URL in plain text, ending at whitespace, a quote or an
	// escape.
	urlPattern = regexp.MustCompile(`https://[^\s\x00-\x20"'<>]+`)
	// ansiPattern is what a terminal program draws with: CSI sequences (cursor
	// moves, colours), OSC sequences ending in BEL or ST, and the one- and
	// two-character escapes in between.
	ansiPattern = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]|\x1b.")
)

// setupTokenIn finds the long-lived token in what Claude Code printed. A token
// that runs to the very end isn't one yet: Claude Code always has more to say
// after it, so that is a token still arriving, and half of one saved as if it
// were whole is a login that fails much later, inside an agent.
func setupTokenIn(out string) string {
	text := cleanTerminal(out)
	loc := setupTokenPattern.FindStringIndex(text)
	if loc == nil || loc[1] == len(text) {
		return ""
	}
	return text[loc[0]:loc[1]]
}

// loginURL finds the sign-in page Claude Code printed. It draws it as a
// terminal hyperlink, so the escape sequence holds the URL whole even where
// the text under it was wrapped; the visible text is the fallback, for a
// terminal that doesn't get one.
func loginURL(out string) string {
	for _, m := range osc8Pattern.FindAllStringSubmatch(out, -1) {
		if isClaudeLoginURL(m[1]) {
			return m[1]
		}
	}
	for _, u := range urlPattern.FindAllString(cleanTerminal(out), -1) {
		if isClaudeLoginURL(u) {
			return u
		}
	}
	return ""
}

// isClaudeLoginURL keeps the sign-in page apart from the documentation links
// and status pages Claude Code also prints.
func isClaudeLoginURL(raw string) bool {
	u, err := url.Parse(strings.TrimRight(raw, ".,)"))
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	known := host == "claude.com" || host == "claude.ai" || host == "anthropic.com" ||
		strings.HasSuffix(host, ".claude.com") || strings.HasSuffix(host, ".anthropic.com")
	return known && strings.Contains(u.Path, "oauth")
}

// cleanTerminal turns what a terminal program drew into the text it meant: the
// escape sequences go, and so do the carriage returns ink ends its lines with.
func cleanTerminal(out string) string {
	return strings.ReplaceAll(ansiPattern.ReplaceAllString(out, ""), "\r", "\n")
}

// lastLines is the tail of what Claude Code drew, for an error message. Its
// screens are redrawn over and over, so blank and repeated lines are dropped
// rather than shown.
func lastLines(out string, n int) string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || (len(lines) > 0 && lines[len(lines)-1] == line) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines[max(len(lines)-n, 0):], " ")
}

// loginEnv is the environment `claude setup-token` runs in: the installers'
// one, with a HOME of its own so it can neither read nor write the user's
// ~/.claude (D6), a terminal it knows how to draw on, and any Anthropic
// credentials the daemon itself was started with taken out — with one of those
// set, Claude Code offers to keep using it instead of minting a token.
func (m *Manager) loginEnv(home string) []string {
	drop := map[string]bool{
		"HOME": true, "BROWSER": true, "TERM": true, "CLAUDE_CONFIG_DIR": true,
		"CLAUDE_CODE_OAUTH_TOKEN": true, "ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true,
		"ANTHROPIC_BASE_URL": true, "ANTHROPIC_MODEL": true,
	}
	var env []string
	for _, kv := range m.installEnv() {
		if name, _, _ := strings.Cut(kv, "="); !drop[name] {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+home, "TERM=xterm-256color")
}
