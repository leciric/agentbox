package credentials

// Whether Anthropic still accepts a stored Claude Code token, and when it was
// saved.
//
// A token from `claude setup-token` is long-lived, not eternal: it can be
// revoked, and the account behind it can change. Nothing used to notice, so a
// dead token surfaced as an unexplained 401 inside an agent, hours after it
// died. Two things fix that: each token records the day it was stored, and a
// check asks Anthropic whether it is still taken.
//
// The check is the call Claude Code itself makes to read the profile behind an
// OAuth token: GET /api/oauth/profile with the token as a bearer. It runs no
// model and costs nothing, needs no beta header, and answers 401
// "OAuth access token is invalid" for a token Anthropic no longer accepts (D62).
//
// A token from `claude setup-token` is only good for inference, and the
// profile needs a scope it doesn't have: Anthropic answers 403 with
// error_code oauth_scope_insufficient. That is an answer about the scope, not
// the token — the token was recognised, or its scopes couldn't have been read
// — so it counts as valid (D83). Probed live: a made-up token gets 401
// authentication_error, a working setup-token login gets that 403.
//
// Both live in a sidecar next to the token, `<account>.json`, so the CLI and
// the daemon — separate processes — share one answer, and neither asks more
// than once an hour.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TokenState is what Anthropic last said about a stored token.
type TokenState string

const (
	// TokenUnknown means nobody has asked yet, or the check couldn't reach
	// Anthropic. It is never a reason to stop using a token: an offline
	// machine must not look like a revoked login.
	TokenUnknown TokenState = ""
	// TokenValid means Anthropic answered for this token.
	TokenValid TokenState = "valid"
	// TokenRejected means Anthropic refused it: revoked, or expired.
	TokenRejected TokenState = "rejected"
)

const (
	// CheckTTL is how long a real answer stands. `agentbox auth status` and
	// the app's Setup page, which polls every few seconds, share it.
	CheckTTL = time.Hour
	// retryTTL is how long a check that learned nothing stands. It is short
	// because the usual cause is a machine that was briefly offline, and
	// waiting an hour to notice it came back would be its own surprise.
	retryTTL = 5 * time.Minute
	// checkTimeout bounds one call to Anthropic, matching Claude Code's own.
	checkTimeout = 10 * time.Second

	metaSuffix = ".json" // the sidecar beside <account>.token
)

// Validity is what is known about one stored token.
type Validity struct {
	State     TokenState
	CheckedAt time.Time
	// Detail is Anthropic's own wording when it refused, or why the check
	// couldn't run.
	Detail string
}

// Stale reports whether the answer is old enough to ask again.
func (v Validity) Stale() bool {
	ttl := CheckTTL
	if v.State == TokenUnknown {
		ttl = retryTTL
	}
	return time.Since(v.CheckedAt) > ttl
}

// claudeMeta is the sidecar kept beside a token: `<account>.json`.
//
// Issued is nil for a token stored before AgentBox recorded the date — the
// honest answer is that it doesn't know, and the token file's own timestamp
// isn't one: copying a credentials directory or restoring a backup rewrites
// it, which would make a years-old token look like today's.
type claudeMeta struct {
	Issued    *time.Time `json:"issued"`              // when the token was saved; null when unknown
	State     TokenState `json:"state,omitempty"`     // what the last check found
	CheckedAt *time.Time `json:"checkedAt,omitempty"` // when that check ran
	Detail    string     `json:"detail,omitempty"`
}

// claudeMetaPath is where an account's sidecar lives.
func (s Store) claudeMetaPath(account string) string {
	return filepath.Join(s.ClaudeDir(), account+metaSuffix)
}

// readClaudeMeta reads an account's sidecar. A missing one means the same as
// an empty one: the token was stored before any of this existed.
func (s Store) readClaudeMeta(account string) claudeMeta {
	b, err := os.ReadFile(s.claudeMetaPath(account))
	if err != nil {
		return claudeMeta{}
	}
	var m claudeMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return claudeMeta{}
	}
	return m
}

// writeClaudeMeta replaces an account's sidecar atomically. It is as private as
// the token it describes.
func writeClaudeMeta(path string, m claudeMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// markClaudeIssued records the day an account's token was stored, and forgets
// whatever the last check said: this is a different token now. It waits for a
// check in flight, which would otherwise write the old token's answer over the
// new token's sidecar.
func (s Store) markClaudeIssued(account string, at time.Time) error {
	lock := checkLock(s.claudeMetaPath(account))
	lock.Lock()
	defer lock.Unlock()
	return writeClaudeMeta(s.claudeMetaPath(account), claudeMeta{Issued: &at})
}

// markClaudeIssuedUnknown gives every token stored before the sidecar existed
// one that says "issued: unknown", so a stored token always has a sidecar and
// an unknown date is a recorded fact rather than a missing file.
//
// Best effort: a missing sidecar already means the same thing, so a
// credentials directory that can't be written to loses nothing. It runs from
// migrateClaude, which runs before every read and write, and does nothing once
// each token has one.
func (s Store) markClaudeIssuedUnknown() {
	names, err := s.accountNames()
	if err != nil {
		return
	}
	for _, name := range names {
		path := s.claudeMetaPath(name)
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		_ = writeClaudeMeta(path, claudeMeta{})
	}
}

// ClaudeValidity is what the last check said about an account's token, read
// from its sidecar. It never calls Anthropic: the app's Setup page polls every
// few seconds and can't wait for the network, so it reads this and refreshes
// in the background when Stale says the answer is old.
func (s Store) ClaudeValidity(account string) Validity {
	name, err := s.ClaudeAccountOf(account)
	if err != nil || name == "" {
		return Validity{}
	}
	m := s.readClaudeMeta(name)
	v := Validity{State: m.State, Detail: m.Detail}
	if m.CheckedAt != nil {
		v.CheckedAt = *m.CheckedAt
	}
	return v
}

// CheckClaudeAccount says whether Anthropic still accepts an account's token,
// reusing the stored answer while it is fresh. An empty account means the
// default one. Callers asking at the same moment share one call, so the CLI
// and the Setup poll can both ask freely.
func (s Store) CheckClaudeAccount(ctx context.Context, account string) (Validity, error) {
	name, err := s.ClaudeAccountOf(account)
	if err != nil || name == "" {
		return Validity{}, err
	}
	token, err := s.ClaudeToken(name)
	if err != nil || token == "" {
		return Validity{}, err
	}

	lock := checkLock(s.claudeMetaPath(name))
	lock.Lock()
	defer lock.Unlock()
	// Re-read inside the lock: whoever we waited for has just answered.
	if v := s.ClaudeValidity(name); !v.Stale() {
		return v, nil
	}
	v := CheckClaudeToken(ctx, token)
	return v, s.recordClaudeValidity(name, v)
}

// RejectClaudeAccount records that Anthropic refused an account's token where
// it was really used — an agent's chat — so the account is marked rejected at
// once, rather than at the next hourly check. An empty account means the
// default one.
func (s Store) RejectClaudeAccount(account, detail string) error {
	name, err := s.ClaudeAccountOf(account)
	if err != nil || name == "" {
		return err
	}
	// Behind the same lock a check takes, so a refusal seen for real wins over
	// an answer a check that started earlier is still waiting for.
	lock := checkLock(s.claudeMetaPath(name))
	lock.Lock()
	defer lock.Unlock()
	return s.recordClaudeValidity(name, Validity{State: TokenRejected, CheckedAt: time.Now(), Detail: detail})
}

// recordClaudeValidity stores an answer beside the token, keeping the date the
// token was saved.
func (s Store) recordClaudeValidity(account string, v Validity) error {
	m := s.readClaudeMeta(account)
	m.State, m.Detail = v.State, v.Detail
	at := v.CheckedAt
	m.CheckedAt = &at
	return writeClaudeMeta(s.claudeMetaPath(account), m)
}

// ClaudeAccountOf is the account a name refers to: the one named, or the
// default account when the name is empty, which is what an agent that never
// picked one runs on. It is "" when no account is stored at all.
func (s Store) ClaudeAccountOf(account string) (string, error) {
	if err := s.migrateClaude(); err != nil {
		return "", err
	}
	if account == "" {
		return s.DefaultClaudeAccount()
	}
	if err := ValidateAccount(account); err != nil {
		return "", err
	}
	return account, nil
}

// checkLocks keeps one check per account in flight. A Store is a value copied
// wherever it's needed, so the locks live with the package, keyed by the
// sidecar's path.
var (
	checkLocksMu sync.Mutex
	checkLocks   = map[string]*sync.Mutex{}
)

func checkLock(path string) *sync.Mutex {
	checkLocksMu.Lock()
	defer checkLocksMu.Unlock()
	l := checkLocks[path]
	if l == nil {
		l = &sync.Mutex{}
		checkLocks[path] = l
	}
	return l
}

// anthropicAPI is where the check goes. ANTHROPIC_BASE_URL moves it, the same
// variable Claude Code reads, for a machine that reaches Anthropic through a
// gateway.
func anthropicAPI() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")), "/")
	if base == "" {
		return "https://api.anthropic.com"
	}
	return base
}

// CheckClaudeToken asks Anthropic whether it still accepts a token, without
// storing anything. It never returns an error: not being able to ask is an
// answer of its own (TokenUnknown), and it must not read as a dead login.
func CheckClaudeToken(ctx context.Context, token string) Validity {
	now := time.Now()
	token = strings.TrimSpace(token)
	if token == "" {
		return Validity{State: TokenUnknown, CheckedAt: now, Detail: "no token is stored"}
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicAPI()+"/api/oauth/profile", nil)
	if err != nil {
		return Validity{State: TokenUnknown, CheckedAt: now, Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Validity{State: TokenUnknown, CheckedAt: now, Detail: "couldn't reach Anthropic: " + err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return Validity{State: TokenValid, CheckedAt: now}
	case resp.StatusCode == http.StatusForbidden && scopeOnly(body):
		return Validity{State: TokenValid, CheckedAt: now}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return Validity{State: TokenRejected, CheckedAt: now, Detail: apiMessage(body, resp.Status)}
	default:
		// A rate limit or an outage says nothing about the token.
		return Validity{State: TokenUnknown, CheckedAt: now, Detail: fmt.Sprintf("Anthropic answered %s: %s", resp.Status, apiMessage(body, resp.Status))}
	}
}

// scopeOnly reports whether a refusal was about the token's scopes and nothing
// else: a token Anthropic recognised, asked for something it may not read.
func scopeOnly(body []byte) bool {
	var out struct {
		Error struct {
			Type    string `json:"type"`
			Details struct {
				ErrorCode string `json:"error_code"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &out) != nil {
		return false
	}
	return out.Error.Type == "permission_error" && out.Error.Details.ErrorCode == "oauth_scope_insufficient"
}

// apiMessage pulls the sentence out of an Anthropic error body, falling back
// to the status line for anything that isn't one.
func apiMessage(body []byte, status string) string {
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err == nil && out.Error.Message != "" {
		return out.Error.Message
	}
	return status
}
