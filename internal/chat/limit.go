package chat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A spent usage limit is the one way a turn fails that waiting puts right. This
// file recognises one in what Claude Code said, works out when the limit
// resets, and schedules the nudge that carries the turn on once it has.
//
// Claude Code only. Codex and OpenCode refuse a turn in their own words, none
// of which have been checked against anything here, and a wrong guess would
// have AgentBox typing into a conversation on its own for a failure that
// waiting can't fix.

const (
	// resumeWait is how long a limited turn waits when the tool named no reset
	// time, and the step the backoff doubles from.
	resumeWait = 5 * time.Minute
	// resumeMaxWait caps that backoff.
	resumeMaxWait = time.Hour
	// resumeSlack is added to a named reset time. The tool names it as a clock
	// time rounded to the minute, and two machines' clocks don't agree to the
	// second, so knocking at exactly the stated moment is the one time it is
	// most likely to still be refused.
	resumeSlack = 30 * time.Second
	// resumeAttempts is how many times a turn is carried on before AgentBox
	// leaves it alone. A limit still refusing after this many waits isn't the
	// rolling window everybody hits; it's an account that needs a person.
	resumeAttempts = 5
	// resumeNudge is what the resumed turn says. Short on purpose: it is the
	// same session, with the whole conversation still in it, so this only has
	// to set the model going again — not describe the work back to it.
	resumeNudge = "Continue where you left off."
)

// usageLimitSigns are what a spent usage limit looks like on the error a turn
// failed with. The ACP adapter rejects the prompt with `Internal error: <what
// the tool said>`, and adds the CLI's own machine-readable error kind as the
// error's data, so both the prose and `"errorKind":"rate_limit"` land in the
// text finishTurn hands over.
//
// Like authSigns above them these are deliberately narrow, and for the same
// reason turned around: resuming a turn that failed for some other reason
// would have AgentBox quietly asking an agent to carry on with work the tool
// never refused.
var usageLimitSigns = []string{
	"usage limit reached",
	`"errorkind":"rate_limit"`,
	"rate_limit_error",
}

// usageLimitPrefixes are how Claude Code's own "you are out of usage" message
// begins. They are the list the Claude Agent SDK exports as
// USAGE_LIMIT_ERROR_PREFIXES, which its ACP adapter matches with startsWith to
// tell that synthetic message from the model's own prose
// (isSyntheticUsageLimitMessage in the adapter's session-failure-extension).
//
// AgentBox sees the message at all because it doesn't advertise the adapter's
// typed session failures: a client that doesn't gets the synthetic message
// forwarded as an ordinary assistant message, which is where the reset time is
// written. Matched the same way the adapter matches it — at the start of what
// was said — because "you've hit your limit" in the middle of a paragraph is
// the model talking about limits, not the provider refusing a turn.
var usageLimitPrefixes = []string{
	"you've hit your",
	"you've reached your",
	"you're out of usage credits",
	"you're out of extra usage",
	"your org is out of usage",
	"your seat type doesn't include usage",
	"your usage allocation has been disabled",
	"your group's usage limit is set to",
}

// UsageLimit reports whether a turn that failed with failure, having last heard
// the tool say said, failed because the account's Claude usage is spent — and
// when the tool said that limit resets, zero when it named no time.
//
// now is the clock the reset is worked out against: the tool writes a bare
// clock time ("resets at 3pm"), so which 3pm it means depends on when it said
// it.
func UsageLimit(failure, said string, now time.Time) (until time.Time, limited bool) {
	failure, said = tidy(failure), tidy(said)
	if !limitedBy(failure) && !limitSaid(said) {
		return time.Time{}, false
	}
	// The reset time can be in either: the error carries the result text, the
	// message carries the sentence the user is shown.
	if until := resetTime(failure, now); !until.IsZero() {
		return until, true
	}
	return resetTime(said, now), true
}

// limitedBy reports a usage limit named by the error a turn failed with.
func limitedBy(failure string) bool {
	for _, sign := range usageLimitSigns {
		if strings.Contains(failure, sign) {
			return true
		}
	}
	// The tool's own sentence reaches here too, wrapped in the adapter's
	// "Internal error: " preamble, so it is matched after that rather than
	// only at the very start.
	for _, prefix := range usageLimitPrefixes {
		if strings.Contains(failure, prefix) {
			return true
		}
	}
	return false
}

// limitSaid reports a usage limit in the last thing the tool said.
func limitSaid(said string) bool {
	for _, line := range strings.Split(said, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range usageLimitPrefixes {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

// tidy puts text into the shape the patterns below expect: lower case, with
// the typographic apostrophe folded onto the plain one. Claude Code writes
// "You've" with a plain apostrophe, but the same sentence reaches AgentBox
// through several hands and any of them may have prettified it.
func tidy(text string) string {
	return strings.ToLower(strings.NewReplacer("\u2019", "'", "\u00a0", " ").Replace(text))
}

var (
	// epochPattern is the reset as seconds since the epoch: the "<message>|
	// <unix seconds>" form Claude Code has used, and the resetsAt field a
	// JSON error body carries.
	epochPattern = regexp.MustCompile(`(?:\||"resets_?at"\s*:\s*"?)(\d{10,13})`)
	// isoPattern is a full timestamp, wherever in the text it appears.
	isoPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[t ]\d{2}:\d{2}(?::\d{2})?(?:\.\d+)?(?:z|[+-]\d{2}:?\d{2})`)
	// datePattern is "resets Nov 3, 9am": how the tool writes a limit more
	// than a day off.
	datePattern = regexp.MustCompile(`reset[a-z]*(?:\s+at)?\s+(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\s+(\d{1,2}),?\s*(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?`)
	// clockPattern is "resets at 3pm", "resets 10:30am (utc)": the common one,
	// since most limits reset within the day.
	clockPattern = regexp.MustCompile(`reset[a-z]*(?:\s+at)?\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*(?:\(([a-z]+)\))?`)
	// waitPattern is a wait rather than a time: "try again in 5 minutes",
	// "retry after 3600 seconds", "retry-after: 60".
	waitPattern      = regexp.MustCompile(`(?:in|after)\s+(\d+)\s*(second|minute|hour)s?`)
	retryAfterHeader = regexp.MustCompile(`retry[- ]after["']?\s*[:=]\s*["']?(\d+)`)
)

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

// resetTime is when text says the limit resets, or the zero time when it
// doesn't say. text is already tidied.
func resetTime(text string, now time.Time) time.Time {
	if m := epochPattern.FindStringSubmatch(text); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		switch {
		case err != nil:
		case len(m[1]) >= 13: // milliseconds
			return time.UnixMilli(n).In(now.Location())
		default:
			return time.Unix(n, 0).In(now.Location())
		}
	}
	if m := isoPattern.FindString(text); m != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02t15:04z07:00", "2006-01-02 15:04:05z07:00", "2006-01-02 15:04z07:00"} {
			if at, err := time.Parse(layout, m); err == nil {
				return at
			}
		}
	}
	if m := datePattern.FindStringSubmatch(text); m != nil {
		hour, min := clock(m[3], m[4], m[5])
		at := time.Date(now.Year(), months[m[1]], atoi(m[2]), hour, min, 0, 0, now.Location())
		// A date the tool names without a year is the next one to come: "Jan
		// 2" said in December is next year's.
		if at.Before(now.Add(-24 * time.Hour)) {
			at = at.AddDate(1, 0, 0)
		}
		return at
	}
	if m := clockPattern.FindStringSubmatch(text); m != nil {
		loc := now.Location()
		if m[4] == "utc" || m[4] == "gmt" {
			// The only zone worth honouring: a name like "pst" can't be
			// resolved to a real zone, and the tool writes the user's own
			// local time anyway, which is the daemon's too.
			loc = time.UTC
		}
		hour, min := clock(m[1], m[2], m[3])
		at := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, loc)
		if !at.After(now) {
			// A clock time is a time of day, so the one it means is the next
			// one — unless it has only just gone by, which means the limit
			// reset while the turn was still failing.
			if now.Sub(at) < time.Hour {
				return now
			}
			at = at.Add(24 * time.Hour)
		}
		return at
	}
	if m := retryAfterHeader.FindStringSubmatch(text); m != nil {
		return now.Add(time.Duration(atoi(m[1])) * time.Second)
	}
	if m := waitPattern.FindStringSubmatch(text); m != nil {
		unit := map[string]time.Duration{"second": time.Second, "minute": time.Minute, "hour": time.Hour}[m[2]]
		return now.Add(time.Duration(atoi(m[1])) * unit)
	}
	return time.Time{}
}

// clock reads an "3", "30", "pm" triple as hours and minutes of a 24-hour
// clock. Without am or pm the hour is already one: nothing writes "resets at
// 15pm".
func clock(hour, min, meridiem string) (int, int) {
	h, m := atoi(hour), atoi(min)
	switch {
	case meridiem == "pm" && h < 12:
		h += 12
	case meridiem == "am" && h == 12:
		h = 0
	}
	return h, m
}

func atoi(text string) int {
	n, _ := strconv.Atoi(text)
	return n
}

// resumeDelay is how long to wait before carrying a limited turn on: until the
// limit resets when the tool said when that is, and a doubling fallback when it
// didn't — or when it said a time, that time came, and the limit was still
// spent. try counts from 1.
func resumeDelay(until, now time.Time, try int) time.Duration {
	backoff := resumeMaxWait
	if try >= 1 && try < 8 {
		backoff = min(resumeWait<<(try-1), resumeMaxWait)
	}
	if until.IsZero() {
		return backoff
	}
	wait := until.Sub(now) + resumeSlack
	switch {
	case wait < resumeSlack:
		return backoff // a reset time already behind us
	case try > 1 && wait < backoff:
		return backoff // it named the same time again, and was wrong before
	}
	return wait
}

// limited records that the turn that just ended hit Claude Code's usage limit,
// and schedules the resume that carries it on. It reports whether it was a
// usage limit at all, so the caller can leave the record alone for any other
// ending. Caller holds c.mu.
func (c *conversation) limited(t *turn, failure string) bool {
	if c.agent.AI != "claude" {
		return false
	}
	now := c.m.now()
	until, limited := UsageLimit(failure, c.lastSaid(t.id), now)
	if !limited {
		return false
	}
	c.session.Limited, c.session.LimitedUntil = true, nil
	if !until.IsZero() {
		at := until
		c.session.LimitedUntil = &at
	}
	c.markSession()
	c.planResume(until)
	return true
}

// lastSaid is the last thing the tool said in a turn. Claude Code's own "you
// are out of usage" message lands there, as an ordinary assistant message, and
// it is the half of the failure that carries the reset time. Caller holds c.mu.
func (c *conversation) lastSaid(turnID string) string {
	for i := len(c.items) - 1; i >= 0; i-- {
		it := c.items[i]
		if it.ID == turnID {
			return "" // back at the top of the turn, with nothing said in it
		}
		if it.Turn == turnID && it.Kind == "assistant" && it.Parent == "" && strings.TrimSpace(it.Text) != "" {
			return it.Text
		}
	}
	return ""
}

// planResume schedules the nudge that carries a limited turn on, unless the
// setting is off or this conversation has already waited enough times.
//
// The wake-up is a timer in memory and nothing else. A daemon that restarts
// forgets it, and the conversation is left as any other failed one: the turn
// is failed, the chat says why, and the next message starts a turn as usual.
// Storing the schedule and re-arming it on load would buy little — the daemon
// normally outlives the limit it is waiting on — and would cost something real:
// an AgentBox that came up hours later would start typing into conversations
// nobody is watching.
//
// Caller holds c.mu.
func (c *conversation) planResume(until time.Time) {
	c.cancelResume()
	if !c.m.resumeAfterLimit() {
		return
	}
	c.resumeTry++
	if c.resumeTry > resumeAttempts {
		c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
			"%s is still over its usage limit after %d tries, so AgentBox has stopped waiting. Send a message to carry on.",
			ToolNames[c.agent.AI], resumeAttempts)
		return
	}
	now := c.m.now()
	wait := resumeDelay(until, now, c.resumeTry)
	at := now.Add(wait)
	c.session.ResumeAt = &at
	c.markSession()
	c.resumeGen++
	gen := c.resumeGen
	c.resumeTimer = c.m.after(wait, func() { c.resume(gen) })
}

// endLimit takes the limit off the session and calls off the pending resume:
// the chat isn't waiting any more. The count of waits stays, because the turn
// that carries a limited chat on comes through here — handing itself a fresh
// set of attempts is how a limit that never resets becomes a loop. Caller
// holds c.mu.
func (c *conversation) endLimit() {
	c.cancelResume()
	if c.session.Limited || c.session.LimitedUntil != nil {
		c.session.Limited, c.session.LimitedUntil = false, nil
		c.markSession()
	}
}

// clearLimit is endLimit for a chat that has genuinely moved on — a turn that
// ended some other way, a session started again, a cleared conversation — so
// the next limit starts its waiting from the beginning. Caller holds c.mu.
func (c *conversation) clearLimit() {
	c.endLimit()
	c.resumeTry = 0
}

// cancelResume drops a pending resume: the chat has moved on, or it is ending.
// Caller holds c.mu.
func (c *conversation) cancelResume() {
	if c.resumeTimer != nil {
		c.resumeTimer.Stop()
		c.resumeTimer = nil
	}
	if c.session.ResumeAt != nil {
		c.session.ResumeAt = nil
		c.markSession()
	}
}

// resume carries the limited turn on, now that the limit should have reset. gen
// is the wake-up it was scheduled as: a timer that fired while the chat was
// being sent somewhere else does nothing.
func (c *conversation) resume(gen int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gone || c.resumeTimer == nil || c.resumeGen != gen {
		return
	}
	c.resumeTimer = nil
	if c.turn != nil {
		// Something is already running — a message arrived as the timer fired.
		// The work is moving again, which is all this was for.
		c.cancelResume()
		c.flush(true)
		return
	}
	if !c.m.resumeAfterLimit() {
		// Turned off while it waited. Read here as well as at scheduling time,
		// so turning it off stops the waits already running.
		c.cancelResume()
		c.flush(true)
		return
	}
	c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
		"The usage limit should have reset, so AgentBox asked %s to carry on.", ToolNames[c.agent.AI])
	it := c.add("user", "")
	it.Turn, it.Text = it.ID, resumeNudge
	c.beginTurn(it, resumeNudge, nil)
}
