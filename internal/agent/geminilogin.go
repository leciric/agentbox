package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"agentbox/internal/credentials"
)

// Signing in to Gemini CLI with a Google account has no command of its own:
// Gemini CLI signs in as it starts, when its settings name the Google sign-in
// and it has none yet. What that takes was found by running Gemini CLI 0.64:
//
//   - With NO_BROWSER set it doesn't open a browser or listen on a localhost
//     port: it prints a Google page whose approval ends on
//     codeassist.google.com with a code, and asks for the code on its
//     terminal. That is the only flow that can finish when the browser is
//     outside AgentBox's VM.
//   - It needs a terminal (a pty) for that prompt, and a wide one so the page's
//     URL isn't wrapped.
//   - Once the code is in, it writes ~/.gemini/oauth_creds.json and goes on to
//     its own interactive screen, which waits for a prompt: nothing is sent to
//     a model, and the login is done once the file is there.
//   - It runs in a HOME of its own, so the user's ~/.gemini is never touched
//     (D6).

// geminiLoginURLPattern is the Google sign-in page Gemini CLI prints.
var geminiLoginURLPattern = regexp.MustCompile(`https://accounts\.google\.com/o/oauth2/[^\s\x00-\x20"'<>]+`)

// GeminiSignIn runs Gemini CLI's Google sign-in and returns the
// oauth_creds.json it wrote, for the caller to store as an account. It
// installs Gemini CLI on this machine first if it has none, reports the page
// to approve through ev.Paste, and types the codes it is given at Gemini CLI's
// prompt.
//
// It never returns before the sign-in is done or setupTokenTimeout has
// passed, so callers run it as a job rather than in a request.
func (m *Manager) GeminiSignIn(ctx context.Context, ev SetupTokenEvents, codes <-chan string) ([]byte, error) {
	status := ev.Status
	if status == nil {
		status = func(string) {}
	}
	gemini, nodeBin, err := m.ensureNPMTool(ctx, "gemini", leadNPMTools["gemini"], status)
	if err != nil {
		return nil, err
	}
	home, cleanup, err := m.loginHome()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := writePrivate(home, geminiSettingsFile, geminiSettings(nil, "oauth-personal")); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, setupTokenTimeout)
	defer cancel()
	m.logf("Signing in to Gemini CLI with %s, under %s", gemini, home)
	cmd := exec.CommandContext(ctx, gemini)
	cmd.Dir = home
	cmd.Env = append(geminiLoginEnv(m.loginEnv(home)), "NO_BROWSER=true", "PATH="+nodeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: setupTokenCols, Rows: 60})
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", gemini, err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	done := make(chan struct{})
	defer close(done)
	go typeCodes(done, ptmx, codes)
	screen := &geminiScreen{}
	ended := make(chan error, 1)
	go func() { ended <- screen.read(ptmx, ev.Paste) }()

	creds := filepath.Join(home, geminiOAuthFile)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, geminiSignInError(ctx, nil, screen.text())
		case err := <-ended:
			// It may have written the file just before it went.
			if b := readGeminiCreds(creds); b != nil {
				return b, nil
			}
			return nil, geminiSignInError(ctx, err, screen.text())
		case <-tick.C:
			if b := readGeminiCreds(creds); b != nil {
				return b, nil
			}
		}
	}
}

// readGeminiCreds is the oauth_creds.json Gemini CLI wrote, once it holds a
// sign-in agents can use; nil before.
func readGeminiCreds(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if login, ok := credentials.ParseGeminiLogin(b); !ok || login.OAuth == nil {
		return nil
	}
	return b
}

// geminiLoginEnv is loginEnv without anything that would sign Gemini CLI in
// some other way than the Google sign-in being made.
func geminiLoginEnv(env []string) []string {
	drop := map[string]bool{
		"GEMINI_API_KEY": true, "GOOGLE_API_KEY": true, "GOOGLE_GENAI_USE_VERTEXAI": true, "GOOGLE_GENAI_USE_GCA": true,
		"GOOGLE_CLOUD_ACCESS_TOKEN": true, "GOOGLE_APPLICATION_CREDENTIALS": true, "GEMINI_CLI_HOME": true, "NO_BROWSER": true, "PATH": true,
	}
	var out []string
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}

// geminiScreen keeps what Gemini CLI drew during a sign-in, for the page to
// approve and, when it fails, for saying why.
type geminiScreen struct {
	mu  sync.Mutex
	raw []byte
}

// read reads Gemini CLI's terminal until it closes, reporting the sign-in page
// once it is there.
func (s *geminiScreen) read(ptmx io.Reader, paste func(string)) error {
	buf := make([]byte, 4096)
	var reported string
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.raw = append(s.raw, buf[:n]...)
			text := cleanTerminal(string(s.raw))
			s.mu.Unlock()
			if u := geminiLoginURLPattern.FindString(text); paste != nil && u != "" && u != reported {
				reported = u
				paste(u)
			}
		}
		if err != nil {
			return err
		}
	}
}

func (s *geminiScreen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cleanTerminal(string(s.raw))
}

// geminiSignInError says why a sign-in ended without one.
func geminiSignInError(ctx context.Context, err error, out string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("the Gemini CLI sign-in wasn't finished within %s: start it again", setupTokenTimeout)
	case ctx.Err() != nil:
		return errors.New("the Gemini CLI sign-in was cancelled")
	}
	if last := lastLines(out, 3); last != "" {
		return fmt.Errorf("gemini ended without a sign-in: %s", last)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("gemini ended without a sign-in: %w", err)
	}
	return errors.New("gemini ended without a sign-in")
}
