package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
)

// fakeUsage stands in for agentbox.linting.dev: it answers the update check,
// and keeps every usage report it is sent.
type fakeUsage struct {
	mu      sync.Mutex
	reports []map[string]any
}

func (f *fakeUsage) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/usage" {
			body, _ := io.ReadAll(r.Body)
			var report map[string]any
			if err := json.Unmarshal(body, &report); err != nil {
				t.Error(err)
			}
			f.mu.Lock()
			f.reports = append(f.reports, report)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"version":"0.16.0","url":"https://github.com/leciric/agentbox/releases/tag/v0.16.0"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/v1/latest"
}

func (f *fakeUsage) sent() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reports...)
}

func postFeature(d testDaemon, feature string) error {
	r := httptest.NewRequest(http.MethodPost, "/v1/usage-stats/"+feature, nil)
	r.SetPathValue("feature", feature)
	return d.srv.countAppFeature(httptest.NewRecorder(), r)
}

func TestUsageStatsAreCountedAndSentWithTheCheck(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	var fake fakeUsage
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t)})
	ctx := context.Background()

	settings, err := patchSettings(t, d, `{}`)
	if err != nil || !settings.UsageStats {
		t.Fatalf("usage stats are on by default: %+v, %v", settings.UsageStats, err)
	}
	// The app counts only the keys it is given.
	if err := postFeature(d, api.FeaturePullList); err != nil {
		t.Fatal(err)
	}
	if err := postFeature(d, "my-secret-repo"); err == nil {
		t.Error("an unknown feature was counted")
	}
	yesterday := usageDay(time.Now().AddDate(0, 0, -1))
	for range 3 {
		_ = d.srv.store.CountFeature(ctx, yesterday, api.FeatureAgentCreateClaude)
	}
	_ = d.srv.store.CountFeature(ctx, usageDay(time.Now().AddDate(0, 0, -40)), api.FeatureAgentDestroy)

	d.srv.checkForUpdate(ctx)
	reports := fake.sent()
	if len(reports) != 1 {
		t.Fatalf("sent %d reports, want 1", len(reports))
	}
	got := reports[0]
	days, _ := json.Marshal(got["days"])
	// Only the days that are over, and none the server wouldn't take.
	if want := `[{"day":"` + yesterday + `","features":{"agent.create.claude":3}}]`; string(days) != want {
		t.Errorf("days = %s, want %s", days, want)
	}
	if len(got) != 5 || got["version"] != "0.16.0" || got["install"] == "" || got["os"] == "" || got["arch"] == "" {
		t.Errorf("report = %v", got)
	}
	// What was sent is forgotten; today's count waits for its day to end.
	left, counts, _ := d.srv.store.FeatureUsage(ctx, "9999-12-31")
	if len(left) != 1 || left[0] != usageDay(time.Now()) || counts[left[0]][api.FeaturePullList] != 1 {
		t.Errorf("after sending, kept %v %v", left, counts)
	}
	// Nothing new to send, nothing sent.
	d.srv.checkForUpdate(ctx)
	if n := len(fake.sent()); n != 1 {
		t.Errorf("sent %d reports with nothing to send", n)
	}

	// Off forgets what wasn't sent, and counts nothing more.
	if settings, err := patchSettings(t, d, `{"usageStats": false}`); err != nil || settings.UsageStats {
		t.Fatalf("turning the stats off = %+v, %v", settings.UsageStats, err)
	}
	_ = postFeature(d, api.FeaturePullList)
	if left, _, _ := d.srv.store.FeatureUsage(ctx, "9999-12-31"); len(left) != 0 {
		t.Errorf("with the stats off, kept %v", left)
	}
}

func TestUsageStatsFollowTheUpdateCheck(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	var fake fakeUsage
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t)})
	ctx := context.Background()

	_ = postFeature(d, api.FeatureMemoryView)
	if _, err := patchSettings(t, d, `{"updateCheck": false}`); err != nil {
		t.Fatal(err)
	}
	_ = postFeature(d, api.FeatureMemoryView)
	if left, _, _ := d.srv.store.FeatureUsage(ctx, "9999-12-31"); len(left) != 0 {
		t.Errorf("with the update check off, kept %v", left)
	}

	// And whatever blocks the check blocks them too.
	if _, err := patchSettings(t, d, `{"updateCheck": true}`); err != nil {
		t.Fatal(err)
	}
	_ = d.srv.store.ForgetFeatureUsage(ctx, "") // the settings change was counted
	t.Setenv("DO_NOT_TRACK", "1")
	_ = postFeature(d, api.FeatureMemoryView)
	if left, _, _ := d.srv.store.FeatureUsage(ctx, "9999-12-31"); len(left) != 0 {
		t.Errorf("with DO_NOT_TRACK=1, kept %v", left)
	}
}
