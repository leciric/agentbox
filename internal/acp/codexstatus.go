package acp

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CodexWindow is one of a Codex account's limits as codex-acp's /status says
// it. codex-acp keeps the rate limits Codex reports to itself and only prints
// them there, so the text is the one place AgentBox can read them from.
type CodexWindow struct {
	// Name is the window: five_hour or seven_day when it is that long (the
	// names Claude's use, so the app treats them alike), else the label's
	// slug ("3h-limit"), prefixed by the limit's own name when it isn't the
	// plain "codex" one.
	Name  string
	Label string
	// Used is the share used, 0 to 1. ResetsAt is zero when /status gave none.
	Used     float64
	ResetsAt time.Time
}

var (
	codexLimitLine  = regexp.MustCompile(`^\*\*(.*?)(\d+m limit|\d+h limit|\d+d limit|Weekly limit|Limit):\*\* (\d+)% left(?: \(resets (\d{1,2}):(\d{2})(?: on ([^)]+))?\))?\s*$`)
	codexDayFirst   = regexp.MustCompile(`^(\d{1,2}) ([A-Za-z]{3})`)
	codexMonthFirst = regexp.MustCompile(`^([A-Za-z]{3}) (\d{1,2})`)
)

// ParseCodexStatus reads the limit lines of codex-acp's /status answer
// ("**5h limit:** 80% left (resets 14:30)"). Reset times carry no zone and
// no year: they are read in loc, the zone the adapter runs in, relative to
// now. A status without limits ("data not available yet") gives none.
func ParseCodexStatus(text string, now time.Time, loc *time.Location) []CodexWindow {
	var out []CodexWindow
	for line := range strings.SplitSeq(text, "\n") {
		m := codexLimitLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		prefix, window := strings.TrimSpace(m[1]), m[2]
		left, _ := strconv.Atoi(m[3])
		w := CodexWindow{Used: float64(100-min(max(left, 0), 100)) / 100}
		switch window {
		case "5h limit":
			w.Name, w.Label = "five_hour", "5-hour"
		case "Weekly limit":
			w.Name, w.Label = "seven_day", "Weekly"
		default:
			w.Name, w.Label = strings.ReplaceAll(window, " ", "-"), strings.TrimSuffix(window, " limit")
		}
		if prefix != "" && prefix != "codex" {
			w.Name, w.Label = prefix+" "+w.Name, prefix+" "+w.Label
		}
		if m[4] != "" {
			w.ResetsAt = codexReset(m[4], m[5], m[6], now, loc)
		}
		out = append(out, w)
	}
	return out
}

// codexReset turns "14:30" or "14:30 on 3 Oct" (or "Oct 3": the adapter's
// locale decides) into the next such moment.
func codexReset(hour, minute, date string, now time.Time, loc *time.Location) time.Time {
	h, _ := strconv.Atoi(hour)
	mi, _ := strconv.Atoi(minute)
	now = now.In(loc)
	if date == "" {
		return time.Date(now.Year(), now.Month(), now.Day(), h, mi, 0, 0, loc)
	}
	var day int
	var month string
	if m := codexDayFirst.FindStringSubmatch(date); m != nil {
		day, month = atoi(m[1]), m[2]
	} else if m := codexMonthFirst.FindStringSubmatch(date); m != nil {
		day, month = atoi(m[2]), m[1]
	} else {
		return time.Time{}
	}
	mon, err := time.Parse("Jan", month)
	if err != nil {
		return time.Time{}
	}
	t := time.Date(now.Year(), mon.Month(), day, h, mi, 0, 0, loc)
	if t.Before(now.AddDate(0, -2, 0)) {
		t = t.AddDate(1, 0, 0)
	}
	return t
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
