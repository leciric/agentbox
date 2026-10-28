package cookieimport

import (
	"errors"
	"fmt"
	"time"
)

// ErrAppBound is returned when ReadProfile is asked for a Windows Chromium
// profile: its cookies are sealed with app-bound encryption the daemon can't
// open. The desktop app reads these on the host and seals them through the
// decrypted-import endpoint instead (see Accept).
var ErrAppBound = errors.New("Chrome, Edge and Brave on Windows seal their cookies so only the browser can open them: import them from the AgentBox desktop app, or export a cookies file")

// ErrKeychainDenied is a macOS Chromium profile read without the browser's
// Keychain passphrase, which the user refused to share when macOS asked.
var ErrKeychainDenied = errors.New("macOS didn't give AgentBox the browser's Keychain key, so its cookies can't be decrypted: import again and click Allow (or Always Allow) when macOS asks")

// ReadProfile reads every cookie of one profile, picked out of FindProfiles
// by ID, dropping expired ones. keyringSecret is the Chromium browser's
// keyring passphrase, fetched on the host by the desktop app (empty for
// Firefox, or when it couldn't be fetched). It returns the cookies and, for
// a Chromium profile, how many values it couldn't decrypt with the key it
// had. Nothing here logs or returns a cookie value in an error.
func ReadProfile(home, profileID, keyringSecret string, now time.Time) (cookies []Cookie, skipped int, err error) {
	var p *Profile
	for _, cand := range FindProfiles(home) {
		if cand.ID == profileID {
			cand := cand
			p = &cand
			break
		}
	}
	if p == nil {
		return nil, 0, fmt.Errorf("no such browser profile")
	}
	if p.AppBound() {
		return nil, 0, ErrAppBound
	}
	switch p.Engine {
	case EngineFirefox:
		cookies, err = readFirefox(p.db, now)
	case EngineChromium:
		cookies, skipped, err = readChromium(p.db, keyringSecret, p.goos, now)
	default:
		return nil, 0, fmt.Errorf("unknown cookie store %q", p.Engine)
	}
	if err != nil {
		return nil, 0, err
	}
	if len(cookies) == 0 && skipped > 0 {
		if p.goos == "darwin" && keyringSecret == "" {
			return nil, skipped, ErrKeychainDenied
		}
		return nil, skipped, fmt.Errorf("none of the profile's %d cookies could be decrypted with the browser's keyring passphrase", skipped)
	}
	if len(cookies) == 0 {
		return nil, skipped, fmt.Errorf("the profile has no cookies that haven't expired")
	}
	return cookies, skipped, nil
}
