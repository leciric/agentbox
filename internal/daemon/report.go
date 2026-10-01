package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/report"
	"agentbox/internal/state"
	"agentbox/internal/update"
)

// Problem reports (internal/report). The daemon is where every report is put
// together and sent, the app's as much as the CLI's: it collects what it can
// see itself (the version, the mode and setup state, its own log and, in a
// VM, the supervisor's), takes what only the app can see as sections of the
// request, and redacts all of it with the one Redactor. A draft is what the
// user is shown; a send redacts again, so nothing reaches the server that
// didn't go through it.

// redactor knows the daemon's own home, and the host's when it runs in a VM.
func (s *Server) redactor() report.Redactor {
	home, _ := os.UserHomeDir()
	return report.NewRedactor(home, os.Getenv("HOME"), hostos.Home())
}

// reportDraft is POST /v1/reports/draft: the report the user would send,
// redacted, with the client's own sections after the daemon's.
func (s *Server) reportDraft(w http.ResponseWriter, r *http.Request) error {
	var req api.ReportDraftRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	install, err := s.installID(ctx)
	if err != nil {
		return err
	}
	sections, err := report.Clean(s.redactor(), append(s.reportSections(ctx), req.Sections...))
	if err != nil {
		return err
	}
	endpoint, err := report.URL(s.cfg.UpdateURL)
	if err != nil {
		return err
	}
	u := update.NewRequest(install, Version)
	return writeJSON(w, http.StatusOK, api.ReportDraft{
		Install: u.Install, Version: u.Version, OS: u.OS, Arch: u.Arch, Sections: sections, Endpoint: endpoint,
	})
}

// sendReport is POST /v1/reports: send what the user saw in the draft and
// kept. An error report is only sent while the user has them on.
func (s *Server) sendReport(w http.ResponseWriter, r *http.Request) error {
	var req api.ReportRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Kind == api.ReportKindError {
		if on, err := s.store.Flag(ctx, state.SettingErrorReports); err != nil || !on {
			return cmp.Or(err, errors.New("automatic error reports are off: turn them on in Settings, General"))
		}
		if os.Getenv("DO_NOT_TRACK") == "1" {
			return errors.New("automatic error reports aren't sent while DO_NOT_TRACK=1 is set")
		}
	}
	red := s.redactor()
	message, err := report.CleanMessage(red, req.Kind, req.Message)
	if err != nil {
		return err
	}
	if len(req.Sections) > report.MaxSections {
		return fmt.Errorf("a report has at most %d sections", report.MaxSections)
	}
	sections, err := report.Clean(red, req.Sections)
	if err != nil {
		return err
	}
	install, err := s.installID(ctx)
	if err != nil {
		return err
	}
	u := update.NewRequest(install, Version)
	id, err := report.Send(ctx, s.cfg.UpdateURL, report.Payload{
		Install: u.Install, Version: u.Version, OS: u.OS, Arch: u.Arch, Kind: req.Kind, Message: message, Sections: sections,
	})
	if err != nil {
		return err
	}
	s.logf("sent a %s report: %s", req.Kind, id)
	return writeJSON(w, http.StatusOK, api.ReportSent{ID: id})
}

// reportSections are the daemon's own sections: the system, its log, and the
// VM supervisor's when it runs in a VM whose log it can read.
func (s *Server) reportSections(ctx context.Context) []api.ReportSection {
	out := []api.ReportSection{
		{ID: api.ReportSectionSystem, Title: "AgentBox and this machine", Content: s.systemReport(ctx)},
		{ID: api.ReportSectionDaemonLog, Title: "The daemon's log (the end of daemon.log)", Content: report.TailFile(s.cfg.Paths.DaemonLog(), report.DefaultLogTail)},
	}
	if path := os.Getenv(report.VMLogEnv); path != "" {
		out = append(out, api.ReportSection{ID: api.ReportSectionVMLog, Title: "The VM supervisor's log (the end of vm.log)", Content: report.TailFile(path, report.DefaultLogTail/2)})
	}
	return out
}

// systemReport is the system section: what this AgentBox is and runs on,
// and how its setup stands.
func (s *Server) systemReport(ctx context.Context) string {
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "%-10s %s\n", label+":", value) }
	line("AgentBox", Version)
	line("Mode", reportMode())
	line("Daemon", fmt.Sprintf("%s/%s, %s", runtime.GOOS, runtime.GOARCH, runtime.Version()))
	if k, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		line("Kernel", strings.TrimSpace(string(k)))
	}
	if d := osPrettyName(); d != "" {
		line("Distro", d)
	}
	if projects, err := s.store.Projects(ctx); err == nil {
		line("Projects", fmt.Sprint(len(projects)))
	}
	if agents, err := s.store.Agents(ctx, ""); err == nil {
		byStatus := map[string]int{}
		for _, a := range agents {
			byStatus[a.Status]++
		}
		var parts []string
		for _, st := range slices.Sorted(maps.Keys(byStatus)) {
			parts = append(parts, fmt.Sprintf("%d %s", byStatus[st], st))
		}
		line("Agents", cmp.Or(strings.Join(parts, ", "), "none"))
	}
	setup, err := s.setupStatus(ctx)
	if err != nil {
		line("Setup", "couldn't be checked: "+err.Error())
		return b.String()
	}
	line("Setup", map[bool]string{true: "ready", false: "not ready"}[setup.Ready])
	for _, c := range setup.Checks {
		switch {
		case c.Status == api.SetupOK || c.Detail == "":
			fmt.Fprintf(&b, "  %-14s %s", c.ID, c.Status)
		case strings.HasPrefix(c.Detail, c.Status+":"):
			fmt.Fprintf(&b, "  %-14s %s", c.ID, c.Detail) // it says the status itself
		default:
			fmt.Fprintf(&b, "  %-14s %s: %s", c.ID, c.Status, c.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// reportMode says how this AgentBox runs: on the machine itself, or in which
// VM of which computer.
func reportMode() string {
	switch hostos.OS() {
	case "":
		return "on this Linux machine itself (host mode, Incus on the host)"
	case hostos.Linux:
		return "in its Cloud Hypervisor VM on a Linux host (VM mode)"
	case "darwin":
		return "in its VM on a Mac"
	case hostos.Windows:
		return "in its WSL distro on Windows"
	default:
		return "in a VM on " + hostos.OS()
	}
}

// osPrettyName is the distribution's name from os-release.
func osPrettyName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}
