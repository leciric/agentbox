package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/cookieimport"
	"agentbox/internal/state"
)

// browserCookiesName is where a project's imported browser cookies are kept:
// a project secret like the others, sealed the same way and removed with the
// project, under a name no environment variable can have. ForAgent, List,
// NamesForAgent and Resolve only deal in environment variable names, so the
// cookies never reach an env file, a list, the brief or a connector; they go
// one way only, into the agents' Chromium (agent.Manager.applyBrowserCookies).
const browserCookiesName = "browser-cookies"

// MaxBrowserCookiesBytes bounds the sealed import, above what MaxValueLen
// allows an environment variable: a few signed-in sites' cookies run to tens
// of kilobytes.
const MaxBrowserCookiesBytes = 2 << 20

// BrowserCookies is a project's import: the domains the user picked, the
// cookies of those domains, and when.
type BrowserCookies struct {
	Domains    []string              `json:"domains"`
	Format     string                `json:"format"`
	Cookies    []cookieimport.Cookie `json:"cookies"`
	ImportedAt time.Time             `json:"importedAt"`
}

// SetBrowserCookies stores a project's import, replacing the one before.
func (s Store) SetBrowserCookies(ctx context.Context, project string, bc BrowserCookies) error {
	if err := s.ready(); err != nil {
		return err
	}
	if len(bc.Cookies) == 0 {
		return errors.New("no cookies to import: pick domains the export has cookies for")
	}
	data, err := json.Marshal(bc)
	if err != nil {
		return err
	}
	if len(data) > MaxBrowserCookiesBytes {
		return fmt.Errorf("the cookies of these domains are %d bytes, more than the %d an import may hold: pick fewer domains", len(data), MaxBrowserCookiesBytes)
	}
	sealed, err := s.seal(string(data))
	if err != nil {
		return err
	}
	return s.State.SetSecret(ctx, state.Secret{Project: project, Name: browserCookiesName, Value: sealed, UpdatedAt: bc.ImportedAt.Truncate(time.Second)})
}

// BrowserCookies opens a project's import; ok is false when it has none.
func (s Store) BrowserCookies(ctx context.Context, project string) (bc BrowserCookies, ok bool, err error) {
	if err := s.ready(); err != nil {
		return bc, false, err
	}
	sec, err := s.State.Secret(ctx, project, "", browserCookiesName)
	if errors.Is(err, state.ErrNotFound) {
		return bc, false, nil
	}
	if err != nil {
		return bc, false, err
	}
	plain, err := s.open(sec.Value)
	if err != nil {
		return bc, false, err
	}
	if err := json.Unmarshal([]byte(plain), &bc); err != nil {
		return bc, false, errors.New("the stored browser cookies can't be read: import them again")
	}
	return bc, true, nil
}

// RemoveBrowserCookies forgets a project's import. Agents that already have
// the cookies keep them in their browser profile.
func (s Store) RemoveBrowserCookies(ctx context.Context, project string) error {
	if err := s.ready(); err != nil {
		return err
	}
	err := s.State.RemoveSecret(ctx, project, "", browserCookiesName)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	return err
}
