package update

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// UsageDay is one UTC day's feature counts: api.Feature keys, and how many
// times each was used. Nothing else is in it.
type UsageDay struct {
	Day      string           `json:"day"` // YYYY-MM-DD
	Features map[string]int64 `json:"features"`
}

// usageBody is everything a usage report sends: the same four things as the
// update check, and the days.
type usageBody struct {
	Install string     `json:"install"`
	Version string     `json:"version"`
	OS      string     `json:"os"`
	Arch    string     `json:"arch"`
	Days    []UsageDay `json:"days"`
}

// MaxUsageDays is the most days one report carries, and how far back unsent
// counts are kept: the server takes no more.
const MaxUsageDays = 31

// UsageURL is where usage goes: "usage" beside the update check's URL (base,
// or DefaultURL when empty), so AGENTBOX_UPDATE_URL moves both.
func UsageURL(base string) (string, error) {
	u, err := url.Parse(cmp.Or(base, DefaultURL))
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	return u.ResolveReference(&url.URL{Path: "usage"}).String(), nil
}

// SendUsage posts days to UsageURL(base). A nil error means the server kept
// them, and they can be forgotten; it replaces a day's counts rather than
// adding to them, so sending a day again is harmless.
func SendUsage(ctx context.Context, base string, req Request, days []UsageDay) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	target, err := UsageURL(base)
	if err != nil {
		return err
	}
	body, err := json.Marshal(usageBody{Install: req.Install, Version: req.Version, OS: req.OS, Arch: req.Arch, Days: days})
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("usage stats: %s", resp.Status)
	}
	return nil
}
