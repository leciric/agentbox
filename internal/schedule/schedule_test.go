package schedule

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestCronNext(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.ParseInLocation("2006-01-02 15:04", s, lisbon)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		expr, after, want string
	}{
		{"0 9 * * *", "2026-10-18 08:00", "2026-10-18 09:00"},
		{"0 9 * * *", "2026-10-18 09:00", "2026-10-19 09:00"},
		{"0 8 * * 1", "2026-10-18 10:00", "2026-10-19 08:00"},  // Sunday → Monday
		{"0 8 * * mon", "2026-10-19 08:00", "2026-10-26 08:00"}, // a week on
		{"30 8 * * 1-5", "2026-10-23 09:00", "2026-10-26 08:30"}, // Friday → Monday
		{"*/15 * * * *", "2026-10-18 10:07", "2026-10-18 10:15"},
		{"0 0 1 * *", "2026-10-18 10:00", "2026-11-01 00:00"},
		{"@weekly", "2026-10-18 10:00", "2026-10-25 00:00"},
		{"0 12 29 2 *", "2026-10-18 10:00", "2028-02-29 12:00"},
		{"0 9 1 * 1", "2026-10-18 10:00", "2026-10-19 09:00"}, // day of month or of week
		{"0 9 * * 7", "2026-10-18 10:00", "2026-10-25 09:00"}, // 7 is Sunday
		// Lisbon's clocks go back at 02:00 on 25 October 2026: 09:00 is still 09:00 local.
		{"0 9 * * *", "2026-10-24 10:00", "2026-10-25 09:00"},
	} {
		c, err := ParseCron(tc.expr)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		got, ok := c.Next(at(tc.after), lisbon)
		if !ok || !got.Equal(at(tc.want)) {
			t.Errorf("%s after %s = %s, %v; want %s", tc.expr, tc.after, got.In(lisbon), ok, tc.want)
		}
	}
}

func TestCronInTimezone(t *testing.T) {
	saoPaulo := mustLoad(t, "America/Sao_Paulo")
	after := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) // 09:00 in São Paulo
	got, ok := Spec{Kind: Cron, Cron: "0 9 * * *", Location: saoPaulo}.Next(after)
	if want := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Fatalf("got %s, want %s", got.UTC(), want)
	}
}

func TestCronRejects(t *testing.T) {
	for _, expr := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "0 0 0 * *", "0 0 * 13 *", "5-1 * * * *", "*/0 * * * *", "0 0 * * funday"} {
		if _, err := ParseCron(expr); err == nil {
			t.Errorf("%q parsed", expr)
		}
	}
	if _, ok := (Spec{Kind: Cron, Cron: "0 0 30 2 *"}).Next(time.Now()); ok {
		t.Error("30 February matched")
	}
}

func TestOnceAndEvery(t *testing.T) {
	start := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	once := Spec{Kind: Once, At: start}
	if got, ok := once.Next(start.Add(-time.Minute)); !ok || !got.Equal(start) {
		t.Errorf("once before: %s %v", got, ok)
	}
	if _, ok := once.Next(start); ok {
		t.Error("once ran again")
	}
	every := Spec{Kind: Every, At: start, Every: 2 * time.Hour}
	for after, want := range map[time.Time]time.Time{
		start.Add(-time.Hour):          start,
		start:                          start.Add(2 * time.Hour),
		start.Add(5 * time.Hour):       start.Add(6 * time.Hour),
		start.Add(100*time.Hour + 1e9): start.Add(102 * time.Hour),
	} {
		if got, ok := every.Next(after); !ok || !got.Equal(want) {
			t.Errorf("every after %s = %s, want %s", after, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	now := time.Now()
	for _, s := range []Spec{{Kind: Once}, {Kind: Every, At: now, Every: time.Minute}, {Kind: Cron, Cron: "x"}, {Kind: "weekly"}} {
		if s.Check() == nil {
			t.Errorf("%+v passed", s)
		}
	}
	for _, s := range []Spec{{Kind: Once, At: now}, {Kind: Every, At: now, Every: time.Hour}, {Kind: Cron, Cron: "@daily"}} {
		if err := s.Check(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestParseAt(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, lisbon)
	for text, want := range map[string]time.Time{
		"2026-10-19T09:00:00+01:00": time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC),
		"2026-10-19 09:00":          time.Date(2026, 10, 19, 9, 0, 0, 0, lisbon),
		"2026-10-19T09:00":          time.Date(2026, 10, 19, 9, 0, 0, 0, lisbon),
		"11:30":                     time.Date(2026, 10, 18, 11, 30, 0, 0, lisbon),
		"09:00":                     time.Date(2026, 10, 19, 9, 0, 0, 0, lisbon),
	} {
		got, err := ParseAt(text, lisbon, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%s = %s, %v; want %s", text, got, err, want)
		}
	}
	if _, err := ParseAt("tomorrow", lisbon, now); err == nil {
		t.Error("tomorrow parsed")
	}
}

func TestEvery(t *testing.T) {
	for text, want := range map[string]time.Duration{"30m": 30 * time.Minute, "2h": 2 * time.Hour, "1h30m": 90 * time.Minute, "7d": 7 * 24 * time.Hour} {
		got, err := ParseEvery(text)
		if err != nil || got != want {
			t.Errorf("%s = %s, %v", text, got, err)
		}
		if back := FormatEvery(got); back != text {
			t.Errorf("FormatEvery(%s) = %s, want %s", got, back, text)
		}
	}
	if _, err := ParseEvery("0d"); err == nil {
		t.Error("0d parsed")
	}
}

func TestLoadLocation(t *testing.T) {
	if loc, err := LoadLocation("", time.UTC); err != nil || loc != time.UTC {
		t.Errorf("empty: %v %v", loc, err)
	}
	if _, err := LoadLocation("Mars/Olympus", time.UTC); err == nil {
		t.Error("Mars loaded")
	}
}
