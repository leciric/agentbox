// Package doctor is `agentbox doctor` on the command line: the checks the
// command makes where it runs, then the daemon's (GET /v1/doctor), printed in
// order, and with --fix whatever fixes itself fixed first. Two commands run
// it: the one in the VM or on a host-mode machine (internal/cli), which
// checks the daemon's socket and version, and the front end on the user's
// machine (internal/hostvm), which checks the VM and KVM instead.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"agentbox/internal/api"
)

// Local is a check the command makes itself, with the fix it runs for --fix
// when it has one (Check.AutoFix).
type Local struct {
	Check api.DoctorCheck
	Fix   func(ctx context.Context) error
}

// Checker is what one run checks: the command's own checks, and the daemon to
// ask for the rest, nil when the command's checks found none answering.
type Checker func(ctx context.Context) ([]Local, *api.Client)

// Run is `agentbox doctor`: it reports whether any check failed, which the
// command exits 1 for. With fix it first fixes what fixes itself, the
// command's own and then the daemon's, saying so on log, and checks again.
func Run(ctx context.Context, out, log io.Writer, check Checker, fix, asJSON bool) (failed bool, err error) {
	local, c := check(ctx)
	if fix {
		var fixed []string
		for _, l := range local {
			if l.Check.Status == api.DoctorOK || !l.Check.AutoFix || l.Fix == nil {
				continue
			}
			_, _ = fmt.Fprintf(log, "Fixing %s…\n", l.Check.Title)
			if err := l.Fix(ctx); err != nil {
				_, _ = fmt.Fprintf(log, "  couldn't: %v\n", err)
			}
			fixed = append(fixed, l.Check.ID)
		}
		if len(fixed) > 0 {
			local, c = check(ctx)
		}
		if c != nil {
			res, err := c.DoctorFix(ctx, api.DoctorFixRequest{})
			if err != nil {
				return true, err
			}
			for _, f := range res.Fixes {
				switch {
				case f.Error != "":
					_, _ = fmt.Fprintf(log, "Couldn't fix %s: %s\n", f.ID, f.Error)
				case f.Job != "":
					_, _ = fmt.Fprintf(log, "Fixing %s in the background: follow it with agentbox jobs %s\n", f.ID, f.Job)
				}
			}
		}
	}
	report := api.DoctorReport{}
	for _, l := range local {
		report.Checks = append(report.Checks, l.Check)
	}
	if c != nil {
		daemon, err := c.Doctor(ctx)
		if err != nil {
			report.Checks = append(report.Checks, api.DoctorCheck{ID: "daemon", Title: "Daemon", Status: api.DoctorFail,
				Message: "it answers, but not with its checks: " + err.Error(), Fix: "agentbox daemon stop, then run this again"})
		} else {
			report.Version = daemon.Version
			report.Checks = append(report.Checks, daemon.Checks...)
		}
	}
	failed = slices.ContainsFunc(report.Checks, func(c api.DoctorCheck) bool { return c.Status == api.DoctorFail })
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return failed, enc.Encode(report)
	}
	Print(out, report.Checks)
	return failed, nil
}

// marks are how Print shows each status.
var marks = map[string]string{api.DoctorOK: "ok  ", api.DoctorWarn: "warn", api.DoctorFail: "FAIL"}

// Print writes checks one a line, each not ok followed by its fix, and a last
// line of what's left.
func Print(out io.Writer, checks []api.DoctorCheck) {
	width := 0
	for _, c := range checks {
		width = max(width, len(c.Title))
	}
	var warn, fail, auto int
	for _, c := range checks {
		_, _ = fmt.Fprintf(out, "%s  %-*s  %s\n", marks[c.Status], width, c.Title, c.Message)
		indent := strings.Repeat(" ", width+8)
		if c.Job != "" {
			_, _ = fmt.Fprintf(out, "%sbeing fixed: agentbox jobs %s\n", indent, c.Job)
		} else if c.Status != api.DoctorOK && c.Fix != "" {
			_, _ = fmt.Fprintf(out, "%sfix: %s\n", indent, c.Fix)
		}
		switch c.Status {
		case api.DoctorWarn:
			warn++
		case api.DoctorFail:
			fail++
		}
		if c.AutoFix && c.Status != api.DoctorOK && c.Job == "" {
			auto++
		}
	}
	switch {
	case fail == 0 && warn == 0:
		_, _ = fmt.Fprintln(out, "\nEverything AgentBox needs is in place.")
		return
	case fail == 0:
		_, _ = fmt.Fprintf(out, "\n%s, nothing failed.\n", plural(warn, "warning"))
	default:
		_, _ = fmt.Fprintf(out, "\n%s failed, %s.\n", plural(fail, "check"), plural(warn, "warning"))
	}
	if auto > 0 {
		_, _ = fmt.Fprintf(out, "agentbox doctor --fix can fix %d of them by itself.\n", auto)
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// VersionCheck is the check of the daemon's version against the command's,
// which fix brings back in step.
func VersionCheck(daemon, command, fix string) api.DoctorCheck {
	c := api.DoctorCheck{ID: "version", Title: "Daemon version", Status: api.DoctorOK, Message: "the daemon and this command are both " + command}
	if daemon != command {
		c.Status = api.DoctorWarn
		c.Message = fmt.Sprintf("the daemon running is %s, and this command is %s", daemon, command)
		c.Fix, c.AutoFix = fix, true
	}
	return c
}
