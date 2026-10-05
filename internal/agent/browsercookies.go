package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/cookieimport"
	"agentbox/internal/state"
)

// A project can import the user's browser cookies, from an export they made
// themselves (internal/cookieimport), kept sealed as a project secret
// (secrets.Store.BrowserCookies). Agents created after the import get them
// in their Chromium the first time it starts, through the DevTools protocol:
// they never become a file in the agent's worktree, its environment or its
// brief, and the AI tool isn't told their values. A marker file in the
// agent's home records which import it got, so a restarted browser isn't
// given them again over what the agent has done since (a sign-out, a newer
// session).

func (m *Manager) browserCookiesMarker() string {
	return "/home/" + m.User.Name + "/.config/agentbox/browser-cookies.applied"
}

// applyBrowserCookies gives a's browser its project's imported cookies, once.
// Agents created before the import don't get them: the import is for new
// agents, and one already at work keeps the browser it has.
func (m *Manager) applyBrowserCookies(ctx context.Context, a state.Agent) error {
	if a.IsLead() || m.Secrets.State == nil {
		return nil
	}
	bc, ok, err := m.Secrets.BrowserCookies(ctx, a.Project)
	if err != nil || !ok {
		return err
	}
	if a.CreatedAt.Before(bc.ImportedAt.Truncate(time.Second)) {
		return nil
	}
	stamp := strconv.FormatInt(bc.ImportedAt.Unix(), 10)
	var got bytes.Buffer
	_ = m.Incus.UserExec(ctx, a.Instance, m.User.Name, "cat "+shellQuote(m.browserCookiesMarker())+" 2>/dev/null || true", nil, &got, nil)
	if strings.TrimSpace(got.String()) == stamp {
		return nil
	}
	if err := m.setBrowserCookies(ctx, a, bc.Cookies); err != nil {
		return err
	}
	m.logf("Signed the browser in to %s with the project's imported cookies", strings.Join(bc.Domains, ", "))
	return m.Incus.WriteFile(ctx, a.Instance, m.browserCookiesMarker(), []byte(stamp+"\n"), m.User.UID, m.User.GID, 0o600)
}

// setBrowserCookies sets cookies in the agent's running Chromium, through its
// browser-wide DevTools endpoint.
func (m *Manager) setBrowserCookies(ctx context.Context, a state.Agent, cookies []cookieimport.Cookie) error {
	var version struct{ WebSocketDebuggerURL string }
	if err := m.devtoolsRequest(ctx, a, http.MethodGet, "/json/version", &version); err != nil {
		return err
	}
	if version.WebSocketDebuggerURL == "" {
		return errors.New("the browser has no DevTools endpoint to set cookies through")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocketDial(ctx, version.WebSocketDebuggerURL, m.devtools(a))
	if err != nil {
		return fmt.Errorf("connecting to the browser: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	session := devtoolsSession{conn: conn}
	return session.call(ctx, "Storage.setCookies", map[string]any{"cookies": cookieParams(cookies)}, nil)
}

// cookieParams are cookies as the DevTools protocol's CookieParam. A domain
// cookie is set by its domain; a host-only one by a URL, which is the only
// way to make one (and the only way a __Host- cookie is accepted).
func cookieParams(cookies []cookieimport.Cookie) []map[string]any {
	out := make([]map[string]any, 0, len(cookies))
	for _, c := range cookies {
		p := map[string]any{"name": c.Name, "value": c.Value, "path": c.Path, "secure": c.Secure, "httpOnly": c.HTTPOnly}
		if strings.HasPrefix(c.Domain, ".") {
			p["domain"] = c.Domain
		} else {
			scheme := "http"
			if c.Secure {
				scheme = "https"
			}
			p["url"] = scheme + "://" + c.Domain + c.Path
		}
		if c.Expires > 0 {
			p["expires"] = c.Expires
		}
		if c.SameSite != "" {
			p["sameSite"] = c.SameSite
		}
		out = append(out, p)
	}
	return out
}
