package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr is a parsed five-field cron expression. Each field is a bit set of
// the values it allows. Like Vixie cron, when both the day of the month and
// the day of the week are restricted a day matching either runs.
type CronExpr struct {
	minute, hour, dom, month, dow uint64
	domAny, dowAny                bool
}

var descriptors = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseCron reads a cron expression.
func ParseCron(expr string) (CronExpr, error) {
	text := strings.ToLower(strings.TrimSpace(expr))
	if d, ok := descriptors[text]; ok {
		text = d
	}
	fields := strings.Fields(text)
	if len(fields) != 5 {
		return CronExpr{}, fmt.Errorf("cron expression %q: want five fields (minute hour day-of-month month day-of-week), like \"0 9 * * 1-5\"", expr)
	}
	var c CronExpr
	var err error
	parts := []struct {
		name    string
		into    *uint64
		lo, hi  int
		names   []string
		namesLo int
	}{
		{"minute", &c.minute, 0, 59, nil, 0},
		{"hour", &c.hour, 0, 23, nil, 0},
		{"day of the month", &c.dom, 1, 31, nil, 0},
		{"month", &c.month, 1, 12, monthNames, 1},
		{"day of the week", &c.dow, 0, 7, dayNames, 0},
	}
	for i, p := range parts {
		if *p.into, err = parseField(fields[i], p.lo, p.hi, p.names, p.namesLo); err != nil {
			return CronExpr{}, fmt.Errorf("cron expression %q, %s: %w", expr, p.name, err)
		}
	}
	// 7 is Sunday too.
	if c.dow&(1<<7) != 0 {
		c.dow = c.dow&^(1<<7) | 1
	}
	c.domAny = strings.HasPrefix(fields[2], "*")
	c.dowAny = strings.HasPrefix(fields[4], "*")
	return c, nil
}

func parseField(field string, lo, hi int, names []string, namesLo int) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(field, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("%q isn't a step", stepText)
			}
			step = n
		}
		from, to := lo, hi
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if from, err = value(a, lo, hi, names, namesLo); err != nil {
				return 0, err
			}
			if to, err = value(b, lo, hi, names, namesLo); err != nil {
				return 0, err
			}
			if from > to {
				return 0, fmt.Errorf("the range %q runs backwards", rng)
			}
		default:
			n, err := value(rng, lo, hi, names, namesLo)
			if err != nil {
				return 0, err
			}
			from = n
			if !hasStep {
				to = n
			}
		}
		for v := from; v <= to; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func value(text string, lo, hi int, names []string, namesLo int) (int, error) {
	for i, name := range names {
		if text == name {
			return i + namesLo, nil
		}
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%q isn't between %d and %d", text, lo, hi)
	}
	return n, nil
}

// maxCronYears is how far ahead Next looks before deciding an expression
// never matches: far enough for 29 February.
const maxCronYears = 5

// Next is the first time after after that c matches, read in loc. A time the
// clock skips when summer time starts runs at the time it is moved to, once.
func (c CronExpr) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	local := after.In(loc)
	for i := 0; i <= maxCronYears*366; i++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+i, 0, 0, 0, 0, loc)
		if !c.matchesDay(day) {
			continue
		}
		for h := range 24 {
			if c.hour&(1<<h) == 0 {
				continue
			}
			for m := range 60 {
				if c.minute&(1<<m) == 0 {
					continue
				}
				t := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
				if t.After(after) {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

func (c CronExpr) matchesDay(day time.Time) bool {
	if c.month&(1<<int(day.Month())) == 0 {
		return false
	}
	dom := c.dom&(1<<day.Day()) != 0
	dow := c.dow&(1<<int(day.Weekday())) != 0
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	default:
		return dom || dow
	}
}
