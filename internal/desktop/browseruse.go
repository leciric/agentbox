package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"agentbox/internal/mcp"
)

// The machine's Chromium closes once nothing has used it for a few minutes
// (browser.sh's idle watcher), and opens again on the next use. What uses it
// marks the time on a file the watcher reads: every desktop tool call, every
// Playwright call (whose guard also opens the browser first when it is
// closed), and browser.sh start. Playwright's MCP server connects to the new
// browser by itself.

// browserScript is browser.sh in the agent (agent.browserScriptPath).
const browserScript = "/usr/local/bin/agentbox-browser"

// browserUsedFile is the file whose time says when the browser was last
// used, under the agent user's home; browser.sh names it too.
const browserUsedFile = ".local/state/agentbox/browser-used"

// MarkBrowserUsed tells the idle watcher the browser is in use now.
func MarkBrowserUsed() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	markUsed(filepath.Join(home, browserUsedFile), time.Now())
}

func markUsed(path string, now time.Time) {
	if os.Chtimes(path, now, now) == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if f, err := os.Create(path); err == nil {
		_ = f.Close()
	}
}

// EnsureBrowser marks the browser used and starts it when its DevTools
// endpoint doesn't answer, which is a browser the idle watcher closed (or one
// that never started). A browser that won't start is said on log; the call
// that needed it then fails with Playwright's own message.
func EnsureBrowser(ctx context.Context, log io.Writer) {
	MarkBrowserUsed()
	if devtoolsUp(ctx, DevTools) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, browserScript, "start").CombinedOutput()
	if err != nil {
		_, _ = fmt.Fprintf(log, "agentbox: starting the browser: %v: %s\n", err, out)
	}
}

func devtoolsUp(ctx context.Context, devtools string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+devtools+"/json/version", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// MarkingUse has each of tools mark the browser used as it is called: the
// desktop tools drive the display the browser is on.
func MarkingUse(tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		if run := t.Run; run != nil {
			t.Run = func(args json.RawMessage) (string, error) { MarkBrowserUsed(); return run(args) }
		}
		if run := t.RunContent; run != nil {
			t.RunContent = func(args json.RawMessage) ([]mcp.Content, error) { MarkBrowserUsed(); return run(args) }
		}
		if wait := t.Wait; wait != nil {
			t.Wait = func(ctx context.Context, args json.RawMessage) (string, error) {
				MarkBrowserUsed()
				return wait(ctx, args)
			}
		}
		out[i] = t
	}
	return out
}
