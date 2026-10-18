// Package schedule works out when a scheduled task runs: once at a time, every
// so often, or on a cron expression, in the timezone of the person who asked.
// It keeps no state and knows nothing of what a run does; the daemon stores
// schedules (state.Schedule) and runs them (internal/daemon/schedules.go).
package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kinds of schedule.
const (
	// Once runs at At, and then never again.
	Once = "once"
	// Every runs every Every, counted from At, so the times it runs don't
	// drift with how long each run took or how late the daemon got to it.
	Every = "every"
	// Cron runs on a five-field cron expression, read in Location.
	Cron = "cron"
)

// MinEvery is the shortest interval a schedule may repeat on: anything
// shorter is a loop, not a schedule, and each run starts a turn of a model.
const MinEvery = 5 * time.Minute

// Spec is when a schedule runs.
type Spec struct {
	Kind string
	// At is when a Once schedule runs, and where an Every one counts from.
	At time.Time
	// Every is an Every schedule's interval.
	Every time.Duration
	// Cron is a Cron schedule's expression: five fields (minute, hour, day of
	// the month, month, day of the week) or one of @hourly, @daily, @weekly,
	// @monthly, @yearly.
	Cron string
	// Location is the timezone a Cron expression's hours and days are in. A
	// Once or Every schedule is an instant and needs none, but keeps it to be
	// shown in.
	Location *time.Location
}

// Check reports what is wrong with a spec, if anything.
func (s Spec) Check() error {
	switch s.Kind {
	case Once:
		if s.At.IsZero() {
			return errors.New("a one-off schedule needs a time to run at")
		}
	case Every:
		if s.Every < MinEvery {
			return fmt.Errorf("a schedule can repeat every %s at most often, not every %s", MinEvery, s.Every)
		}
		if s.At.IsZero() {
			return errors.New("a repeating schedule needs a time to count from")
		}
	case Cron:
		if _, err := ParseCron(s.Cron); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown kind of schedule %q: once, every or cron", s.Kind)
	}
	return nil
}

func (s Spec) loc() *time.Location {
	if s.Location == nil {
		return time.Local
	}
	return s.Location
}

// Next is the first time the schedule runs after after, and false when it
// never runs again: a Once whose time has passed, or a cron expression no
// date matches (30 February).
func (s Spec) Next(after time.Time) (time.Time, bool) {
	switch s.Kind {
	case Once:
		return s.At, s.At.After(after)
	case Every:
		if s.Every <= 0 {
			return time.Time{}, false
		}
		if s.At.After(after) {
			return s.At, true
		}
		n := after.Sub(s.At)/s.Every + 1
		return s.At.Add(n * s.Every), true
	case Cron:
		c, err := ParseCron(s.Cron)
		if err != nil {
			return time.Time{}, false
		}
		return c.Next(after, s.loc())
	}
	return time.Time{}, false
}

// Describe says when a schedule runs, in English, for the command line and
// the lead: the app writes its own, translated.
func (s Spec) Describe() string {
	loc := s.loc()
	switch s.Kind {
	case Once:
		return "once, at " + s.At.In(loc).Format("Mon 2 Jan 2006 15:04 MST")
	case Every:
		return "every " + FormatEvery(s.Every)
	case Cron:
		return fmt.Sprintf("cron %q (%s)", s.Cron, loc)
	}
	return s.Kind
}

// FormatEvery writes an interval the way people say it: 90m is 1h30m, 48h is
// 2d.
func FormatEvery(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	out := strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
	if strings.HasSuffix(out, "h0m") {
		out = strings.TrimSuffix(out, "0m")
	}
	return out
}

// ParseEvery reads an interval: a Go duration (90m, 2h30m) or a number of
// days (1d, 7d).
func ParseEvery(text string) (time.Duration, error) {
	text = strings.TrimSpace(strings.ToLower(text))
	if days, ok := strings.CutSuffix(text, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q isn't a number of days", text)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%q isn't an interval like 30m, 2h or 1d", text)
	}
	return d, nil
}

// ParseAt reads a time to run at: RFC 3339 with an offset
// (2026-10-19T09:00:00+01:00), or a date and time in loc (2026-10-19 09:00),
// or a time of day in loc (09:00), which is the next time the clock shows it
// after now.
func ParseAt(text string, loc *time.Location, now time.Time) (time.Time, error) {
	text = strings.TrimSpace(text)
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, text, loc); err == nil {
			return t, nil
		}
	}
	if clock, err := time.ParseInLocation("15:04", text, loc); err == nil {
		local := now.In(loc)
		t := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		if !t.After(now) {
			t = time.Date(local.Year(), local.Month(), local.Day()+1, clock.Hour(), clock.Minute(), 0, 0, loc)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q isn't a time: give one like 2026-10-19T09:00:00+01:00, 2026-10-19 09:00 or 09:00", text)
}

// LoadLocation is time.LoadLocation, with "" meaning fallback rather than
// UTC.
func LoadLocation(name string, fallback *time.Location) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return fallback, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%q isn't a timezone: give an IANA name like Europe/Lisbon or America/Sao_Paulo", name)
	}
	return loc, nil
}
