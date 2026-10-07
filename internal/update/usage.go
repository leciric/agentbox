package update

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
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

// UsageBody is everything a usage report sends: the same four things as the
// update check, and the days.
type UsageBody struct {
	Install string     `json:"install"`
	Version string     `json:"version"`
	OS      string     `json:"os"`
	Arch    string     `json:"arch"`
	Days    []UsageDay `json:"days"`
}

// NewUsageBody is the report req sends with days.
func NewUsageBody(req Request, days []UsageDay) UsageBody {
	return UsageBody{Install: req.Install, Version: req.Version, OS: req.OS, Arch: req.Arch, Days: days}
}

// UsageEvent is one anonymous usage event: a random id, so a retry is told
// from a new one, its UTC day, its name, and its fixed fields.
type UsageEvent struct {
	ID    string          `json:"id"`
	Day   string          `json:"day"`
	Name  string          `json:"name"`
	Props json.RawMessage `json:"props"`
}

// EventsBody is everything an events report sends: the same four things as
// the update check, and the events.
type EventsBody struct {
	Install string       `json:"install"`
	Version string       `json:"version"`
	OS      string       `json:"os"`
	Arch    string       `json:"arch"`
	Events  []UsageEvent `json:"events"`
}

// NewEventsBody is the report req sends with events.
func NewEventsBody(req Request, events []UsageEvent) EventsBody {
	return EventsBody{Install: req.Install, Version: req.Version, OS: req.OS, Arch: req.Arch, Events: events}
}

// MaxEventsPerReport is the most events one report carries: the server
// takes no more.
const MaxEventsPerReport = 500

// MaxUsageDays is the most days one report carries, and how far back unsent
// counts are kept: the server takes no more.
const MaxUsageDays = 31

// UsageURL is where usage goes: "usage" beside the update check's URL (base,
// or DefaultURL when empty), so AGENTBOX_UPDATE_URL moves both.
func UsageURL(base string) (string, error) { return besideCheck(base, "usage") }

// EventsURL is where usage events go, "events" beside the update check's URL.
func EventsURL(base string) (string, error) { return besideCheck(base, "events") }

func besideCheck(base, name string) (string, error) {
	u, err := url.Parse(cmp.Or(base, DefaultURL))
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	return u.ResolveReference(&url.URL{Path: name}).String(), nil
}

// SendUsage posts days to UsageURL(base). A nil error means the server kept
// them, and they can be forgotten; it replaces a day's counts rather than
// adding to them, so sending a day again is harmless.
func SendUsage(ctx context.Context, base string, req Request, days []UsageDay) error {
	target, err := UsageURL(base)
	if err != nil {
		return err
	}
	return post(ctx, target, NewUsageBody(req, days))
}

// ErrRefused is a report the server answered with a 4xx: sending it again
// would only be refused again.
var ErrRefused = errors.New("refused")

// SendEvents posts events to EventsURL(base). A nil error means the server
// kept them; one it has already kept is ignored, so a retry is harmless. An
// error wrapping ErrRefused means it never will, as an older server refusing
// an event a newer AgentBox knows.
func SendEvents(ctx context.Context, base string, req Request, events []UsageEvent) error {
	target, err := EventsURL(base)
	if err != nil {
		return err
	}
	return post(ctx, target, NewEventsBody(req, events))
}

func post(ctx context.Context, target string, payload any) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	body, err := json.Marshal(payload)
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
	switch {
	case resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests:
		return fmt.Errorf("usage stats: %s: %w", resp.Status, ErrRefused)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("usage stats: %s", resp.Status)
	}
	return nil
}
