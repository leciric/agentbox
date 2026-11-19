package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/update"
)

// insideAnAgent makes the daemon see the hostname Incus gives every agent's
// machine, the one signal that needs no file.
func insideAnAgent(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTBOX_IN_AGENT_HOSTNAME", "ab-hello-stack-agent-01")
}

// TestAgentsDaemonNeverChecksForUpdates: AgentBox's released binary is copied
// into every agent as /usr/local/bin/agentbox, so a daemon started there has a
// release version and would be counted as an install. It isn't, whatever its
// environment says: DO_NOT_TRACK and AGENTBOX_NO_UPDATE_CHECK are empty here,
// as they are when a nested daemon's start dropped them.
func TestAgentsDaemonNeverChecksForUpdates(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	insideAnAgent(t)
	asVersion(t, "0.16.0")
	var fake fakeLatest
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t, "0.17.0"), releasesURL: fakeStable(t, "0.17.0")})
	ctx := context.Background()
	d.srv.checkForUpdate(ctx)
	time.Sleep(100 * time.Millisecond) // and the one Run started
	if fake.count() != 0 {
		t.Errorf("%d check(s) went out from an agent", fake.count())
	}
	status, err := d.client.Update(ctx)
	if err != nil || status.Blocked != update.InAgentReason || status.Available != nil {
		t.Errorf("Update() = %+v, %v; want it blocked because it runs in an agent", status, err)
	}
	if on, err := d.srv.updateCheckOn(ctx); err != nil || on {
		t.Errorf("updateCheckOn() = %v, %v in an agent", on, err)
	}
}

// TestAgentsDaemonNeverCountsOrSendsUsage: usage stats are counted and sent
// only while the update check may go out, which in an agent it may not.
func TestAgentsDaemonNeverCountsOrSendsUsage(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	var fake fakeUsage
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	ctx := context.Background()
	waitFor(t, "the check as the daemon starts", func() bool {
		status, err := d.client.Update(ctx)
		return err == nil && status.CheckedAt != nil
	})
	// The same daemon and the same counts, on a machine of its own: sent.
	yesterday := usageDay(time.Now().AddDate(0, 0, -1))
	_ = d.srv.store.CountFeature(ctx, yesterday, api.FeatureAgentCreateClaude)
	if !d.srv.usageStatsOn(ctx) {
		t.Fatal("the stats are off on a machine of its own: this test proves nothing")
	}

	insideAnAgent(t)
	// What the daemon sent as it started, on a machine of its own, is no
	// business of this test: only what goes out from here on.
	eventCount := func() (n int) {
		for _, list := range fake.sentEvents() {
			n += len(list)
		}
		return n
	}
	reportsBefore, eventsBefore := len(fake.sent()), eventCount()
	if d.srv.usageStatsOn(ctx) {
		t.Error("usageStatsOn() is true in an agent")
	}
	_ = d.srv.store.ForgetFeatureUsage(ctx, "") // whatever was counted above
	_ = d.srv.store.CountFeature(ctx, yesterday, api.FeatureAgentCreateClaude)
	_ = postFeature(d, api.FeaturePullList)
	if left, _, _ := d.srv.store.FeatureUsage(ctx, "9999-12-31"); len(left) != 1 || left[0] != yesterday {
		t.Errorf("a use was counted in an agent: kept %v", left)
	}
	d.sendNow(t)
	if n := len(fake.sent()) - reportsBefore; n != 0 {
		t.Errorf("%d usage report(s) went out from an agent", n)
	}
	if n := eventCount() - eventsBefore; n != 0 {
		t.Errorf("%d event(s) went out from an agent: %v", n, fake.sentEvents())
	}
}

// TestAgentsDaemonNeverSendsErrorReports: an error report is the one thing
// that doesn't go through the update check's gate, so it has its own.
func TestAgentsDaemonNeverSendsErrorReports(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	var fake fakeReports
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	errReport := api.ReportRequest{Kind: api.ReportKindError, Message: "TypeError: x is undefined", Sections: []api.ReportSection{{ID: "error", Title: "The error", Content: "at App.tsx:1"}}}
	var sent api.ReportSent

	if err := callReport(t, d.srv.sendReport, "/v1/reports", errReport, &sent); err != nil {
		t.Fatalf("the report wasn't sent on a machine of its own: %v", err)
	}
	insideAnAgent(t)
	if err := callReport(t, d.srv.sendReport, "/v1/reports", errReport, &sent); err == nil {
		t.Error("an error report was sent from an agent")
	}
	if len(fake.sent) != 1 {
		t.Errorf("sent %d reports, want only the one from the machine of its own", len(fake.sent))
	}
}
