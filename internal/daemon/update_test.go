package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sync"
	"testing"
	"time"

	"agentbox/internal/state"
)

// fakeLatest stands in for agentbox.linting.dev, and keeps every query it got.
type fakeLatest struct {
	mu      sync.Mutex
	queries []url.Values
}

func (f *fakeLatest) start(t *testing.T, version string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.Query())
		f.mu.Unlock()
		w.Write([]byte(`{"version":"` + version + `","url":"https://github.com/leciric/agentbox/releases/tag/v` + version + `"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AGENTBOX_UPDATE_URL", srv.URL)
}

func (f *fakeLatest) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

// asVersion makes the daemon a release build for one test. Registered before
// startTestDaemon, so it is put back after the daemon has stopped.
func asVersion(t *testing.T, v string) {
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func TestUpdateCheckFindsANewerRelease(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	var fake fakeLatest
	fake.start(t, "0.17.0")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()

	waitFor(t, "the check as the daemon starts", func() bool {
		status, err := d.client.Update(ctx)
		return err == nil && status.Available != nil
	})
	status, _ := d.client.Update(ctx)
	if !status.Enabled || status.Blocked != "" || status.Current != "0.16.0" || status.CheckedAt == nil ||
		status.Available.Version != "0.17.0" || status.Available.URL != "https://github.com/leciric/agentbox/releases/tag/v0.17.0" {
		t.Errorf("Update() = %+v", status)
	}

	id, err := d.srv.store.Setting(ctx, state.SettingInstallID)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("install ID = %q, %v; want a random UUID", id, err)
	}
	q := fake.queries[0]
	if len(q) != 4 || q.Get("install") != id || q.Get("version") != "0.16.0" || q.Get("os") == "" || q.Get("arch") == "" {
		t.Errorf("query = %v", q)
	}

	// Turning it off forgets the answer, and stops asking.
	settings, err := patchSettings(t, d, `{"updateCheck": false}`)
	if err != nil || settings.UpdateCheck {
		t.Fatalf("turning the check off = %+v, %v", settings.UpdateCheck, err)
	}
	if status, _ := d.client.Update(ctx); status.Enabled || status.Available != nil {
		t.Errorf("after turning it off, Update() = %+v", status)
	}
	before := fake.count()
	d.srv.checkForUpdate(ctx)
	if fake.count() != before {
		t.Error("a check went out with the setting off")
	}

	// Turning it back on asks at once, with the same install ID.
	if _, err := patchSettings(t, d, `{"updateCheck": true}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the check after turning it on", func() bool {
		status, err := d.client.Update(ctx)
		return err == nil && status.Available != nil
	})
	fake.mu.Lock()
	last := fake.queries[len(fake.queries)-1]
	fake.mu.Unlock()
	if last.Get("install") != id {
		t.Errorf("install ID changed: %q, then %q", id, last.Get("install"))
	}
}

func TestUpdateCheckIsQuietWhenCurrent(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.17.0")
	var fake fakeLatest
	fake.start(t, "0.17.0")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	waitFor(t, "the check as the daemon starts", func() bool {
		status, err := d.client.Update(ctx)
		return err == nil && status.CheckedAt != nil
	})
	if status, _ := d.client.Update(ctx); status.Available != nil {
		t.Errorf("Update() = %+v on the latest release", status)
	}
}

// TestUpdateCheckBlocked: a development build, AGENTBOX_NO_UPDATE_CHECK=1 and
// DO_NOT_TRACK=1 each keep the check from ever going out. (The names are short
// because the daemon's sockets live under t.TempDir, and a unix socket's path
// can't be long.)
func TestUpdateCheckBlocked(t *testing.T) {
	for name, env := range map[string][2]string{
		"dev":    {"", ""},
		"opt":    {"1", ""}, // AGENTBOX_NO_UPDATE_CHECK=1
		"no_dnt": {"", "1"}, // DO_NOT_TRACK=1
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AGENTBOX_NO_UPDATE_CHECK", env[0])
			t.Setenv("DO_NOT_TRACK", env[1])
			if name != "dev" {
				asVersion(t, "0.16.0")
			}
			var fake fakeLatest
			fake.start(t, "0.17.0")
			d := startTestDaemon(t, t.TempDir(), fakeIncus)
			ctx := context.Background()
			d.srv.checkForUpdate(ctx)
			time.Sleep(100 * time.Millisecond) // and the one Run started
			if fake.count() != 0 {
				t.Errorf("%d check(s) went out", fake.count())
			}
			status, err := d.client.Update(ctx)
			if err != nil || status.Blocked == "" || status.Available != nil {
				t.Errorf("Update() = %+v, %v", status, err)
			}
			if id, _ := d.srv.store.Setting(ctx, state.SettingInstallID); id != "" {
				t.Errorf("an install ID %q was made for a check that never runs", id)
			}
		})
	}
}
