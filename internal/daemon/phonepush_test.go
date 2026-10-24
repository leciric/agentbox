package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/push"
	"agentbox/internal/state"
)

// fakeExpo is Expo's push service: it keeps what it was sent, and says a
// token ending in "gone]" reaches nobody.
type fakeExpo struct {
	mu   sync.Mutex
	sent []push.Message
}

func (f *fakeExpo) serve(w http.ResponseWriter, r *http.Request) {
	var msgs []push.Message
	_ = json.NewDecoder(r.Body).Decode(&msgs)
	f.mu.Lock()
	f.sent = append(f.sent, msgs...)
	f.mu.Unlock()
	var data []string
	for _, m := range msgs {
		if strings.HasSuffix(m.To, "gone]") {
			data = append(data, `{"status":"error","message":"gone","details":{"error":"DeviceNotRegistered"}}`)
		} else {
			data = append(data, `{"status":"ok","id":"t"}`)
		}
	}
	_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(data, ","))
}

func (f *fakeExpo) take() []push.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

func TestPhonePush(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	expo := &fakeExpo{}
	srv := httptest.NewServer(http.HandlerFunc(expo.serve))
	defer srv.Close()
	d.srv.phonePush.client = push.Client{URL: srv.URL}
	// 12:00 UTC: 09:00 in São Paulo.
	d.srv.phonePush.now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
	d.srv.lan.mu.Lock()
	d.srv.lan.own = true
	d.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	d.srv.lan.mu.Unlock()
	port := freePort(t)
	on := true
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	pairing, err := d.client.PairLAN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	app := &phoneClient{t: t, base: base, http: &http.Client{Timeout: 10 * time.Second}}
	body, _ := json.Marshal(api.LANPairRequest{Secret: secretOf(t, pairing), Name: "Pixel", Bearer: true})
	code, out := app.do("POST", "/lan/pair", string(body))
	var session api.LANSession
	if code != http.StatusOK || json.Unmarshal([]byte(out), &session) != nil {
		t.Fatalf("pairing = %d %s", code, out)
	}
	auth := []string{"Authorization", "Bearer " + session.Token}
	get := func() api.PhonePush {
		t.Helper()
		code, out := app.do("GET", "/lan/push", "", auth...)
		var p api.PhonePush
		if code != http.StatusOK || json.Unmarshal([]byte(out), &p) != nil {
			t.Fatalf("GET /lan/push = %d %s", code, out)
		}
		return p
	}

	// Before registering: nothing, and every kind offered.
	if p := get(); p.Registered || len(p.Kinds) != len(api.PushKinds) {
		t.Fatalf("GET /lan/push before registering = %+v", p)
	}
	if code, _ := app.do("GET", "/lan/push", ""); code != http.StatusUnauthorized {
		t.Errorf("GET /lan/push with no token = %d", code)
	}
	// Choosing needs no token yet, and pushes nothing until there is one.
	if code, out := app.do("PUT", "/lan/push", `{"kinds":["needs_you"]}`, auth...); code != http.StatusOK || !strings.Contains(out, `"registered":false`) {
		t.Errorf("PUT with no token ever registered = %d %s", code, out)
	}
	if n := d.srv.sendPushes(ctx, pushNote{kind: api.PushNeedsYou, project: "web"}); n != 0 {
		t.Errorf("sendPushes to a phone with no token = %d", n)
	}
	code, out = app.do("POST", "/lan/push/test", "", auth...)
	if code != http.StatusOK || !strings.Contains(out, `"sent":false`) || !strings.Contains(out, "no push token") {
		t.Errorf("POST /lan/push/test with no token = %d %s", code, out)
	}
	for _, bad := range []string{`{"token":"nope","kinds":[]}`, `{"token":"ExponentPushToken[a]","kinds":["spam"]}`,
		`{"token":"ExponentPushToken[a]","kinds":[],"quietFrom":1440}`, `{"token":"ExponentPushToken[a]","kinds":[],"timeZone":"Mars/Base"}`} {
		if code, _ := app.do("PUT", "/lan/push", bad, auth...); code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, code)
		}
	}
	req := `{"token":"ExponentPushToken[pixel]","kinds":["needs_you","pr_ready"],"timeZone":"America/Sao_Paulo"}`
	if code, out := app.do("PUT", "/lan/push", req, auth...); code != http.StatusOK || !strings.Contains(out, `"registered":true`) || strings.Contains(out, "pixel") {
		t.Fatalf("PUT /lan/push = %d %s; want it registered, the token never read back", code, out)
	}

	note := pushNote{kind: api.PushNeedsYou, title: "Fix login needs you", body: "Which?", project: "web", agent: "agent-3"}
	if n := d.srv.sendPushes(ctx, note); n != 1 {
		t.Fatalf("sendPushes of a kind it wants = %d, want 1", n)
	}
	sent := expo.take()
	if len(sent) != 1 || sent[0].To != "ExponentPushToken[pixel]" || sent[0].Data["ref"] != "web/agent-3" || sent[0].Data["kind"] != api.PushNeedsYou {
		t.Fatalf("sent = %+v", sent)
	}
	if n := d.srv.sendPushes(ctx, pushNote{kind: api.PushFinished, project: "web"}); n != 0 {
		t.Errorf("sendPushes of a kind it doesn't want = %d", n)
	}

	// Quiet from 08:00 to 10:00 in São Paulo, where it is 09:00; the token
	// registered stays when only the settings change.
	if code, out := app.do("PUT", "/lan/push", `{"kinds":["needs_you"],"quietFrom":480,"quietTo":600,"timeZone":"America/Sao_Paulo"}`, auth...); code != http.StatusOK {
		t.Fatalf("PUT settings only = %d %s", code, out)
	}
	if n := d.srv.sendPushes(ctx, note); n != 0 {
		t.Errorf("sendPushes in quiet hours = %d", n)
	}
	d.srv.phonePush.now = func() time.Time { return time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC) }
	if n := d.srv.sendPushes(ctx, note); n != 1 {
		t.Errorf("sendPushes after quiet hours = %d", n)
	}
	expo.take()

	// A phone that may no longer read gets nothing.
	if _, err := d.client.UpdateLANPhone(ctx, session.Phone.ID, api.UpdateLANPhoneRequest{Scopes: []string{}}); err != nil {
		t.Fatal(err)
	}
	if n := d.srv.sendPushes(ctx, note); n != 0 {
		t.Errorf("sendPushes to a phone that may not read = %d", n)
	}
	if _, err := d.client.UpdateLANPhone(ctx, session.Phone.ID, api.UpdateLANPhoneRequest{Scopes: state.DefaultPhoneScopes}); err != nil {
		t.Fatal(err)
	}

	// The test push goes whatever the kinds, and says what it was.
	code, out = app.do("POST", "/lan/push/test", "", auth...)
	var test api.PhonePushTest
	if code != http.StatusOK || json.Unmarshal([]byte(out), &test) != nil || !test.Sent || test.Title == "" {
		t.Fatalf("POST /lan/push/test = %d %s", code, out)
	}
	expo.take()

	// A token Expo says reaches nobody is forgotten.
	if code, _ := app.do("PUT", "/lan/push", `{"token":"ExponentPushToken[gone]","kinds":["needs_you"]}`, auth...); code != http.StatusOK {
		t.Fatal("PUT a new token")
	}
	if n := d.srv.sendPushes(ctx, note); n != 0 {
		t.Errorf("sendPushes to a token gone = %d", n)
	}
	if p := get(); p.Registered {
		t.Errorf("a token Expo gave up on is still registered: %+v", p)
	}

	// DELETE stops them; with phones turned off nothing is pushed at all.
	if code, _ := app.do("PUT", "/lan/push", req, auth...); code != http.StatusOK {
		t.Fatal("PUT again")
	}
	if code, _ := app.do("DELETE", "/lan/push", "", auth...); code != http.StatusNoContent {
		t.Errorf("DELETE /lan/push = %d", code)
	}
	if p := get(); p.Registered {
		t.Errorf("after DELETE: %+v", p)
	}
	if code, _ := app.do("PUT", "/lan/push", req, auth...); code != http.StatusOK {
		t.Fatal("PUT again")
	}
	off := false
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if n := d.srv.sendPushes(ctx, note); n != 0 {
		t.Errorf("sendPushes with phones off = %d", n)
	}
}

func TestQuietAt(t *testing.T) {
	t.Parallel()
	at := func(h, m int) time.Time { return time.Date(2026, 10, 24, h, m, 0, 0, time.UTC) }
	for _, c := range []struct {
		from, to int
		tz       string
		now      time.Time
		want     bool
	}{
		{0, 0, "", at(3, 0), false},                // none
		{22 * 60, 7 * 60, "UTC", at(23, 30), true}, // across midnight
		{22 * 60, 7 * 60, "UTC", at(6, 59), true},
		{22 * 60, 7 * 60, "UTC", at(7, 0), false},
		{22 * 60, 7 * 60, "UTC", at(12, 0), false},
		{13 * 60, 14 * 60, "UTC", at(13, 30), true}, // within a day
		{13 * 60, 14 * 60, "UTC", at(14, 0), false},
		{22 * 60, 7 * 60, "Asia/Tokyo", at(14, 0), true}, // 23:00 in Tokyo
	} {
		p := state.PhonePush{QuietFrom: c.from, QuietTo: c.to, TimeZone: c.tz}
		if got := quietAt(p, c.now); got != c.want {
			t.Errorf("quietAt(%d–%d %s, %s) = %v", c.from, c.to, c.tz, c.now.Format("15:04"), got)
		}
	}
}

// What the hooks push: a finish with its pull request open is one to
// review, and a permission request is pushed once however often the chat
// reports it.
func TestPushNotes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	expo := &fakeExpo{}
	srv := httptest.NewServer(http.HandlerFunc(expo.serve))
	defer srv.Close()
	s := d.srv
	s.phonePush.client = push.Client{URL: srv.URL}
	if err := s.store.SetFlag(ctx, state.SettingLAN, true); err != nil {
		t.Fatal(err)
	}
	p, _, err := s.store.AddPhone(ctx, "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetPhonePush(ctx, state.PhonePush{PhoneID: p.ID, Token: "ExponentPushToken[p]", Kinds: api.PushKinds}); err != nil {
		t.Fatal(err)
	}
	wait := func(n int) []push.Message {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var got []push.Message
		for time.Now().Before(deadline) {
			expo.mu.Lock()
			l := len(expo.sent)
			expo.mu.Unlock()
			if l >= n {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond) // and nothing more
		got = expo.take()
		if len(got) != n {
			t.Fatalf("pushed %d, want %d: %+v", len(got), n, got)
		}
		return got
	}
	a := state.Agent{Project: "web", Name: "agent-3", Title: "Fix login"}

	s.pushFinished(a, api.AgentEvent{Summary: "Done.", PR: &api.PullRequest{Number: 12, State: "open", Title: "fix: login"}})
	m := wait(1)[0]
	if m.Data["kind"] != api.PushPRReady || m.Data["pr"] != "12" || m.Title != "PR #12 is ready to review" || m.Body != "Fix login: fix: login" {
		t.Errorf("a finish with its PR open = %+v", m)
	}
	s.pushFinished(a, api.AgentEvent{Summary: "Looked into it."})
	if m := wait(1)[0]; m.Data["kind"] != api.PushFinished || m.Title != "Fix login finished" || m.Body != "Looked into it." {
		t.Errorf("a finish = %+v", m)
	}

	perm := api.ChatAgentPermission{Agent: "agent-3", Ref: "web/agent-3", Item: "i1", Permission: api.ChatPermission{Title: "Run rm -rf build"}}
	s.pushPermission(a, perm)
	s.pushPermission(a, perm)
	perm.Permission.Outcome = "allow"
	s.pushPermission(a, perm)
	if m := wait(1)[0]; m.Data["kind"] != api.PushNeedsYou || m.Data["ref"] != "web/agent-3" || m.Body != "Run rm -rf build" {
		t.Errorf("a permission request = %+v", m)
	}

	s.pushChecksFailed("web", "agent-9", "", false, 7, "test (ubuntu)")
	if m := wait(1)[0]; m.Data["kind"] != api.PushCIFailed || m.Data["ref"] != "web" || m.Body != "agent-9: test (ubuntu)" {
		t.Errorf("checks failing on a gone agent's PR = %+v", m)
	}
}
