package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// fakeDaemon answers the doctor's two routes on a unix socket, as the daemon
// does, and counts the fixes asked of it.
func fakeDaemon(t *testing.T, checks []api.DoctorCheck) (*api.Client, *[]api.DoctorFixRequest) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var fixes []api.DoctorFixRequest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/doctor", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DoctorReport{Version: "1.2.3", Checks: checks})
	})
	mux.HandleFunc("POST /v1/doctor/fix", func(w http.ResponseWriter, r *http.Request) {
		var req api.DoctorFixRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		fixes = append(fixes, req)
		_ = json.NewEncoder(w).Encode(api.DoctorFixResult{Fixes: []api.DoctorFix{{ID: "image", Job: "job-7"}}})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return api.NewClient(socket), &fixes
}

func TestRunPrintsEveryCheckAndFails(t *testing.T) {
	c, fixes := fakeDaemon(t, []api.DoctorCheck{
		{ID: "image", Title: "Base image", Status: api.DoctorFail, Message: "there's none yet", Fix: "agentbox image build", AutoFix: true},
		{ID: "github", Title: "GitHub account", Status: api.DoctorWarn, Message: "none stored", Fix: "agentbox auth github"},
	})
	local := []Local{{Check: api.DoctorCheck{ID: "socket", Title: "Daemon socket", Status: api.DoctorOK, Message: "answers"}}}
	var out bytes.Buffer
	failed, err := Run(context.Background(), &out, &out, func(context.Context) ([]Local, *api.Client) { return local, c }, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !failed {
		t.Error("a failed check didn't fail the run")
	}
	text := out.String()
	for _, want := range []string{"ok    Daemon socket", "FAIL  Base image", "fix: agentbox image build", "warn  GitHub account",
		"fix: agentbox auth github", "1 check failed, 1 warning.", "--fix can fix 1 of them"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if len(*fixes) != 0 {
		t.Errorf("without --fix, it asked the daemon to fix %v", *fixes)
	}
}

func TestRunFixesLocalChecksThenTheDaemons(t *testing.T) {
	c, fixes := fakeDaemon(t, []api.DoctorCheck{{ID: "image", Title: "Base image", Status: api.DoctorOK, Message: "current"}})
	started := false
	check := func(context.Context) ([]Local, *api.Client) {
		if started {
			return []Local{{Check: api.DoctorCheck{ID: "socket", Title: "Daemon socket", Status: api.DoctorOK, Message: "answers"}}}, c
		}
		// Down, until its fix starts it: then there's a daemon to ask.
		return []Local{{
			Check: api.DoctorCheck{ID: "socket", Title: "Daemon socket", Status: api.DoctorFail, Message: "nothing answers", Fix: "agentbox daemon start", AutoFix: true},
			Fix:   func(context.Context) error { started = true; return nil },
		}}, nil
	}
	var out bytes.Buffer
	failed, err := Run(context.Background(), &out, &out, check, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !started || failed {
		t.Errorf("started %v, failed %v; want the daemon started, and nothing failing after\n%s", started, failed, out.String())
	}
	if len(*fixes) != 1 {
		t.Errorf("asked the daemon for %d fixes, want 1", len(*fixes))
	}
	if !strings.Contains(out.String(), "agentbox jobs job-7") || !strings.Contains(out.String(), "Everything AgentBox needs is in place.") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestRunWithNoDaemonAndAFixThatFails(t *testing.T) {
	check := func(context.Context) ([]Local, *api.Client) {
		return []Local{{
			Check: api.DoctorCheck{ID: "vm", Title: "AgentBox's VM", Status: api.DoctorFail, Message: "doesn't answer", Fix: "agentbox vm start", AutoFix: true},
			Fix:   func(context.Context) error { return errors.New("no VM") },
		}}, nil
	}
	var out, log bytes.Buffer
	failed, err := Run(context.Background(), &out, &log, check, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// --json's output is the JSON alone: what fixing said is on log.
	var report api.DoctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out.String())
	}
	if !failed || len(report.Checks) != 1 || report.Checks[0].ID != "vm" {
		t.Errorf("failed %v, report %+v", failed, report)
	}
	if !strings.Contains(log.String(), "couldn't: no VM") {
		t.Errorf("a fix that failed wasn't said:\n%s", log.String())
	}
}

func TestVersionCheck(t *testing.T) {
	if c := VersionCheck("1.0.0", "1.0.0", "x"); c.Status != api.DoctorOK || c.AutoFix {
		t.Errorf("same = %+v", c)
	}
	if c := VersionCheck("0.9.0", "1.0.0", "agentbox daemon stop"); c.Status != api.DoctorWarn || !c.AutoFix || c.Fix != "agentbox daemon stop" || !strings.Contains(c.Message, "0.9.0") {
		t.Errorf("older daemon = %+v", c)
	}
}
