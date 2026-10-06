package cookieimport

import (
	"fmt"
	"time"
)

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
	if len(cookies) == 0 {
		return nil, skipped, fmt.Errorf("the profile has no cookies that haven't expired")
	}
	return cookies, skipped, nil
}
