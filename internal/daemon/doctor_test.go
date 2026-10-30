package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

func doctorCheck(t *testing.T, r api.DoctorReport, id string) api.DoctorCheck {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", id, r.Checks)
	return api.DoctorCheck{}
}

// TestDoctorReusesSetupsChecksAndSaysWhatFixesThem. The doctor is Setup's
// checks said as ok, warn or fail, plus its own; an image that isn't there
// fails and fixes itself, while a login, which only the user can give, has a
// command for them instead.
func TestDoctorReusesSetupsChecksAndSaysWhatFixesThem(t *testing.T) {
	t.Parallel()
	offline := errors.New("dial tcp: lookup api.github.com: no such host")
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) {
		s.probe = func(context.Context, string) error { return offline }
	}})
	report, err := d.client.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != Version {
		t.Errorf("version = %q, want %q", report.Version, Version)
	}
	var ids []string
	for _, c := range report.Checks {
		ids = append(ids, c.ID)
		if c.Status != api.DoctorOK && c.Fix == "" {
			t.Errorf("%s is %s with no fix: %+v", c.ID, c.Status, c)
		}
	}
	// In order: the daemon, Incus, its user mapping and pool, the disks, the
	// network (no image downloads in tests: AGENTBOX_IMAGES_URL=off), the
	// image, then the logins and tools.
	want := "daemon incus host storage disk network-github image ai-login ai-tool gh github"
	if got := strings.Join(ids, " "); got != want {
		t.Errorf("checks = %s\nwant      %s", got, want)
	}
	if c := doctorCheck(t, report, "incus"); c.Status != api.DoctorOK {
		t.Errorf("incus = %+v, want ok, as Setup says", c)
	}
	if c := doctorCheck(t, report, "network-github"); c.Status != api.DoctorFail || !strings.Contains(c.Message, "no such host") {
		t.Errorf("GitHub, offline = %+v; want a failure naming why", c)
	}
	if c := doctorCheck(t, report, "image"); c.Status != api.DoctorFail || !c.AutoFix {
		t.Errorf("no image = %+v; want a failure that fixes itself", c)
	}
	if c := doctorCheck(t, report, api.DoctorAILogin); c.Status != api.DoctorFail || c.AutoFix || c.Fix != "agentbox auth claude" {
		t.Errorf("no Claude login = %+v; want a failure with agentbox auth claude to run", c)
	}
	if c := doctorCheck(t, report, "github"); c.Status != api.DoctorWarn || c.Fix != "agentbox auth github" {
		t.Errorf("no GitHub account = %+v; want a warning with agentbox auth github", c)
	}

	// Online, the network passes.
	d.srv.probe = func(context.Context, string) error { return nil }
	report, err = d.client.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c := doctorCheck(t, report, "network-github"); c.Status != api.DoctorOK {
		t.Errorf("GitHub, online = %+v; want ok", c)
	}
}

// TestDoctorFixRefusesWhatHasNoFixOfItsOwn: --fix runs only what is safe to
// retry, and says so of a check it was asked for and can't.
func TestDoctorFixRefusesWhatHasNoFixOfItsOwn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) {
		s.probe = func(context.Context, string) error { return nil }
	}})
	res, err := d.client.DoctorFix(context.Background(), api.DoctorFixRequest{Checks: []string{api.DoctorAILogin}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fixes) != 1 || res.Fixes[0].ID != api.DoctorAILogin || res.Fixes[0].Job != "" || res.Fixes[0].Error == "" {
		t.Errorf("fixing the login = %+v; want it refused, with no job", res.Fixes)
	}
}

// TestDoctorHomeRefusesWhatTheHomeChatCantFix. The Home chat runs on the AI
// tool's login, so a failing login is never handed to it; nor is a warning,
// or what fixes itself.
func TestDoctorHomeRefusesWhatTheHomeChatCantFix(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{setup: func(s *Server) {
		s.probe = func(context.Context, string) error { return nil }
	}})
	before, err := d.srv.store.AllLeadSessions(context.Background(), state.HomeProject)
	if err != nil {
		t.Fatal(err)
	}
	for check, why := range map[string]string{api.DoctorAILogin: "can't fix it", "github": "didn't fail", "image": "fixes itself", "nope": "no check"} {
		_, err := d.client.DoctorHome(context.Background(), api.DoctorHomeRequest{Check: check})
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("handing %s to the Home chat: %v; want it refused (%s)", check, err, why)
		}
	}
	sessions, err := d.srv.store.AllLeadSessions(context.Background(), state.HomeProject)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != len(before) {
		t.Errorf("refusals opened %d Home sessions", len(sessions)-len(before))
	}
}

// TestDoctorHandOffTellsTheHomeChatWhatFailed: the check, what it found, its
// fix and the end of the log, with secrets out of it, and the skill to use.
func TestDoctorHandOffTellsTheHomeChatWhatFailed(t *testing.T) {
	c := api.DoctorCheck{ID: "network-github", Title: "GitHub", Status: api.DoctorFail,
		Message: "can't reach api.github.com: i/o timeout", Fix: "check this machine's internet connection"}
	var log strings.Builder
	for i := range 100 {
		fmt.Fprintf(&log, "line %d\n", i)
	}
	log.WriteString("pushing with GH_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789\n")
	msg := doctorHandOff(c, log.String())
	for _, want := range []string{"troubleshooting-setup", "GitHub (network-github)", "can't reach api.github.com: i/o timeout",
		"Suggested fix: check this machine's internet connection", "line 99", "agentbox doctor"} {
		if !strings.Contains(msg, want) {
			t.Errorf("hand-off lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Errorf("hand-off carries a token:\n%s", msg)
	}
	if strings.Contains(msg, "line 60\n") {
		t.Errorf("hand-off has more than the log's last %d lines:\n%s", doctorLogLines, msg)
	}
	if !strings.Contains(doctorHandOff(c, ""), "Check: GitHub") || strings.Contains(doctorHandOff(c, ""), "daemon's log") {
		t.Errorf("with no log, the hand-off should leave the log out")
	}
}

func TestHandOffRefusal(t *testing.T) {
	fail := api.DoctorCheck{ID: "incus", Status: api.DoctorFail}
	if err := handOffRefusal(fail); err != nil {
		t.Errorf("a failure with no fix of its own: %v; want it handed over", err)
	}
	for _, c := range []api.DoctorCheck{
		{ID: api.DoctorAILogin, Status: api.DoctorFail, Fix: "agentbox auth claude"},
		{ID: "disk", Status: api.DoctorWarn},
		{ID: "image", Status: api.DoctorFail, AutoFix: true},
	} {
		if handOffRefusal(c) == nil {
			t.Errorf("%+v was handed over", c)
		}
	}
}

func TestDiskCheck(t *testing.T) {
	const gib = 1 << 30
	if c := diskCheck(agent.DiskStatus{}); c.Status != api.DoctorOK || c.Message != "not measured yet" {
		t.Errorf("unmeasured = %+v", c)
	}
	ok := agent.DiskCheck{DiskSpace: agent.DiskSpace{Label: "Worktrees", Free: 40 * gib}, Floor: 5 * gib, Level: agent.DiskOK}
	if c := diskCheck(agent.DiskStatus{Disks: []agent.DiskCheck{ok}}); c.Status != api.DoctorOK || !strings.Contains(c.Message, "40.0G free") {
		t.Errorf("room = %+v", c)
	}
	low := agent.DiskCheck{DiskSpace: agent.DiskSpace{Label: "Storage pool", Free: 2 * gib, Advice: []string{"Retire agents you're done with."}}, Floor: 5 * gib, Level: agent.DiskLow}
	c := diskCheck(agent.DiskStatus{Disks: []agent.DiskCheck{ok, low}})
	if c.Status != api.DoctorWarn || !strings.Contains(c.Message, "Storage pool has 2.0G free") || c.Fix != "Retire agents you're done with." {
		t.Errorf("low = %+v", c)
	}
	low.Level = agent.DiskFull
	if c := diskCheck(agent.DiskStatus{Disks: []agent.DiskCheck{low}}); c.Status != api.DoctorFail {
		t.Errorf("full = %+v, want a failure", c)
	}
}

func TestFromSetupCheck(t *testing.T) {
	for _, tc := range []struct {
		in   api.SetupCheck
		want string
	}{
		{api.SetupCheck{Status: api.SetupOK}, api.DoctorOK},
		{api.SetupCheck{Status: api.SetupMissing, Required: true}, api.DoctorFail},
		{api.SetupCheck{Status: api.SetupOutdated, Required: true}, api.DoctorFail},
		{api.SetupCheck{Status: api.SetupBuilding, Required: true}, api.DoctorWarn},
		{api.SetupCheck{Status: api.SetupWarn}, api.DoctorWarn},
	} {
		if got := fromSetupCheck(tc.in).Status; got != tc.want {
			t.Errorf("%+v = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestDoctorHomeOpensASessionToldWhatFailed: Incus not answering has no fix of
// the doctor's own, so it goes to a Home session of its own, whose chat holds
// what failed.
func TestDoctorHomeOpensASessionToldWhatFailed(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), unusableIncus, testConfig{setup: func(s *Server) {
		s.probe = func(context.Context, string) error { return nil }
	}})
	ctx := context.Background()
	before, err := d.srv.store.AllLeadSessions(ctx, state.HomeProject)
	if err != nil {
		t.Fatal(err)
	}
	// Not logged in, the Home chat couldn't run: nothing is opened.
	if _, err := d.client.DoctorHome(ctx, api.DoctorHomeRequest{Check: "incus"}); err == nil || !strings.Contains(err.Error(), "agentbox auth claude") {
		t.Errorf("handing over with no login: %v; want it refused, saying to log in", err)
	}
	if sessions, _ := d.srv.store.AllLeadSessions(ctx, state.HomeProject); len(sessions) != len(before) {
		t.Errorf("a refusal opened %d Home sessions", len(sessions)-len(before))
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-default"); err != nil {
		t.Fatal(err)
	}
	res, err := d.client.DoctorHome(ctx, api.DoctorHomeRequest{Check: "incus"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := d.srv.store.LeadSession(ctx, state.HomeProject, res.Session)
	if err != nil {
		t.Fatal(err)
	}
	if ls.Topic != "Fix: Incus" {
		t.Errorf("session topic = %q, want Fix: Incus", ls.Topic)
	}
	items, err := d.srv.store.ChatItems(ctx, state.HomeProject, state.LeadSessionName(res.Session))
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	for _, it := range items {
		notices = append(notices, string(it.Data))
	}
	if all := strings.Join(notices, "\n"); !strings.Contains(all, "troubleshooting-setup") || !strings.Contains(all, "permission denied") {
		t.Errorf("the session's chat = %s; want the hand-off, with Incus' own error", all)
	}
}
