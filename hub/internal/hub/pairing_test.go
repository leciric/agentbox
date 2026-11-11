package hub

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/hubapi"
)

// claim makes the test hub's owner with a setup link, as the first person to
// open it would, and returns their device token.
func (h *testHub) claim() string {
	h.t.Helper()
	link, claim, err := NewSetupLink(context.Background(), h.store, h.http.URL)
	if err != nil || !claim {
		h.t.Fatalf("NewSetupLink: %v, claim %v", err, claim)
	}
	_, secret, _ := strings.Cut(link, "#")
	return decode[hubapi.HubSession](h.t, h.do("POST", hubapi.PathSetup, "", hubapi.HubSetupRequest{Secret: secret, Label: "first browser"}), 200).Token
}

// pair asks for a pairing, as a device or an environment would.
func (h *testHub) pair(req hubapi.HubPairRequest) hubapi.HubPairing {
	h.t.Helper()
	return decode[hubapi.HubPairing](h.t, h.do("POST", hubapi.PathPair, "", req), 200)
}

func (h *testHub) poll(p hubapi.HubPairing) hubapi.HubPairResult {
	h.t.Helper()
	return decode[hubapi.HubPairResult](h.t, h.do("POST", hubapi.PathPairToken, "", hubapi.HubPairTokenRequest{Secret: p.Secret}), 200)
}

func TestClaimingTheHub(t *testing.T) {
	h := newTestHub(t)
	info := decode[hubapi.HubInfo](t, h.do("GET", hubapi.PathInfo, "", nil), 200)
	if info.Claimed || info.Passwords {
		t.Fatalf("a new hub: %+v", info)
	}
	// Nothing pairs before there's an owner to approve it.
	if resp := h.do("POST", hubapi.PathPair, "", hubapi.HubPairRequest{Kind: hubapi.PairDevice, Label: "app"}); resp.StatusCode != http.StatusConflict {
		t.Errorf("pairing on a hub with no owner: %d", resp.StatusCode)
	}

	// Only the newest link works, and only once, and only for one of two
	// people opening it at the same moment.
	old, _, _ := NewSetupLink(context.Background(), h.store, h.http.URL)
	link, _, _ := NewSetupLink(context.Background(), h.store, h.http.URL)
	if !strings.HasPrefix(link, h.http.URL+hubapi.PathSetupPage+"#"+hubapi.SetupSecretPrefix) {
		t.Fatalf("link = %s", link)
	}
	_, oldSecret, _ := strings.Cut(old, "#")
	if resp := h.do("POST", hubapi.PathSetup, "", hubapi.HubSetupRequest{Secret: oldSecret}); resp.StatusCode != 401 {
		t.Errorf("a replaced link: %d", resp.StatusCode)
	}
	_, secret, _ := strings.Cut(link, "#")
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	tokens := make([]string, 4)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := h.do("POST", hubapi.PathSetup, "", hubapi.HubSetupRequest{Secret: secret, Label: fmt.Sprintf("browser %d", i)})
			statuses[i] = resp.StatusCode
			if resp.StatusCode == 200 {
				tokens[i] = decode[hubapi.HubSession](t, resp, 200).Token
				// A browser keeps it in the cookie.
				if !strings.Contains(resp.Header.Get("Set-Cookie"), hubapi.SessionCookie+"=") {
					t.Error("claiming set no cookie")
				}
			}
		}()
	}
	wg.Wait()
	owner := ""
	for i, status := range statuses {
		if status == 200 {
			if owner != "" {
				t.Fatal("two people claimed the hub")
			}
			owner = tokens[i]
		} else if status != 401 {
			t.Errorf("claim %d: %d", i, status)
		}
	}
	if owner == "" {
		t.Fatalf("nobody claimed the hub: %v", statuses)
	}
	me := decode[hubapi.HubUser](t, h.do("GET", hubapi.PathMe, owner, nil), 200)
	if me.Email != "" || me.ID == "" {
		t.Errorf("the owner: %+v", me)
	}
	if info := decode[hubapi.HubInfo](t, h.do("GET", hubapi.PathInfo, "", nil), 200); !info.Claimed {
		t.Error("the hub isn't claimed after its link was used")
	}

	// Once claimed, a link signs the owner in, as a new device of theirs.
	link, claim, err := NewSetupLink(context.Background(), h.store, h.http.URL)
	if err != nil || claim {
		t.Fatalf("a link on a claimed hub: %v, claim %v", err, claim)
	}
	_, secret, _ = strings.Cut(link, "#")
	again := decode[hubapi.HubSession](t, h.do("POST", hubapi.PathSetup, "", hubapi.HubSetupRequest{Secret: secret, Label: "lost everything"}), 200)
	if again.User.ID != me.ID {
		t.Errorf("the sign-in link signed in %+v, not the owner", again.User)
	}
	if devices := decode[[]hubapi.HubDevice](t, h.do("GET", hubapi.PathDevices, owner, nil), 200); len(devices) != 2 {
		t.Errorf("devices = %+v", devices)
	}
}

func TestPairingADevice(t *testing.T) {
	h := newTestHub(t)
	owner := h.claim()
	p := h.pair(hubapi.HubPairRequest{Kind: hubapi.PairDevice, Label: "AgentBox app on laptop\x07"})
	if p.Secret == "" || len(p.Code) != 9 || p.Interval < 1 || len(p.QR) == 0 || !strings.HasPrefix(p.ApproveURL, h.http.URL+hubapi.PathApprovePage+"?code=") {
		t.Fatalf("pairing = %+v", p)
	}
	if r := h.poll(p); r.Status != hubapi.PairPending || r.Session != nil {
		t.Fatalf("before approval: %+v", r)
	}

	// Approving needs a device of the hub's.
	path := hubapi.PathPairings + "/" + p.Code
	if resp := h.do("GET", path, "", nil); resp.StatusCode != 401 {
		t.Errorf("looking at a pairing signed out: %d", resp.StatusCode)
	}
	if resp := h.do("POST", path+"/approve", "", nil); resp.StatusCode != 401 {
		t.Errorf("approving signed out: %d", resp.StatusCode)
	}
	// However the code is typed.
	typed := strings.ToLower(strings.ReplaceAll(p.Code, "-", " "))
	info := decode[hubapi.HubPairingInfo](t, h.do("GET", hubapi.PathPairings+"/"+strings.ReplaceAll(typed, " ", "%20"), owner, nil), 200)
	if info.Kind != hubapi.PairDevice || info.Label != "AgentBox app on laptop" || info.Address == "" {
		t.Errorf("info = %+v", info)
	}
	decode[hubapi.HubPairingInfo](t, h.do("POST", path+"/approve", owner, nil), 200)
	if resp := h.do("POST", path+"/deny", owner, nil); resp.StatusCode != 404 && resp.StatusCode != 409 {
		t.Errorf("denying it after approving it: %d", resp.StatusCode)
	}

	r := h.poll(p)
	if r.Status != hubapi.PairApproved || r.Session == nil || !strings.HasPrefix(r.Session.Token, hubapi.SessionTokenPrefix) {
		t.Fatalf("after approval: %+v", r)
	}
	// The token answers once.
	if resp := h.do("POST", hubapi.PathPairToken, "", hubapi.HubPairTokenRequest{Secret: p.Secret}); resp.StatusCode != 404 {
		t.Errorf("polling again: %d", resp.StatusCode)
	}
	device := r.Session.Token
	if me := decode[hubapi.HubUser](t, h.do("GET", hubapi.PathMe, device, nil), 200); me.ID != r.Session.User.ID {
		t.Errorf("the device is %+v", me)
	}

	// The device is listed, and revoking it signs it out.
	devices := decode[[]hubapi.HubDevice](t, h.do("GET", hubapi.PathDevices, owner, nil), 200)
	var id string
	for _, d := range devices {
		if d.Label == "AgentBox app on laptop" {
			id = d.ID
			if d.Current {
				t.Error("the laptop is the owner's current device")
			}
		} else if !d.Current {
			t.Errorf("the owner's browser isn't current: %+v", d)
		}
	}
	if len(devices) != 2 || id == "" {
		t.Fatalf("devices = %+v", devices)
	}
	// Another account can't revoke it.
	other := h.signIn("someone@example.com")
	if resp := h.do("DELETE", hubapi.PathDevices+"/"+id, other, nil); resp.StatusCode != 404 {
		t.Errorf("revoking someone else's device: %d", resp.StatusCode)
	}
	if resp := h.do("DELETE", hubapi.PathDevices+"/"+id, owner, nil); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp := h.do("GET", hubapi.PathMe, device, nil); resp.StatusCode != 401 {
		t.Errorf("a revoked device: %d", resp.StatusCode)
	}
	if resp := h.do("DELETE", hubapi.PathDevices+"/"+id, owner, nil); resp.StatusCode != 404 {
		t.Errorf("revoking it twice: %d", resp.StatusCode)
	}
}

func TestPairingAnEnvironment(t *testing.T) {
	h := newTestHub(t)
	owner := h.claim()
	decode[hubapi.HubEnvironmentToken](t, h.do("POST", hubapi.PathEnvironments, owner, hubapi.HubCreateEnvironmentRequest{Name: "lint-pc"}), 201)

	p := h.pair(hubapi.HubPairRequest{Kind: hubapi.PairEnvironment, Label: "AgentBox on Lint-PC.local", Hostname: "Lint-PC.local", Version: "0.17.0"})
	info := decode[hubapi.HubPairingInfo](t, h.do("POST", hubapi.PathPairings+"/"+p.Code+"/approve", owner, nil), 200)
	if info.Name != "lint-pc" {
		t.Errorf("name = %q, from its hostname", info.Name)
	}
	r := h.poll(p)
	if r.Status != hubapi.PairApproved || r.Environment == nil || r.Session != nil {
		t.Fatalf("result = %+v", r)
	}
	// lint-pc was taken, so it's lint-pc-2.
	if r.Environment.Environment.Name != "lint-pc-2" || !strings.HasPrefix(r.Environment.Token, hubapi.EnvironmentTokenPrefix) {
		t.Errorf("environment = %+v", r.Environment)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.connectEnvironment(ctx, r.Environment.Token)
	envs := decode[[]hubapi.HubEnvironment](t, h.do("GET", hubapi.PathEnvironments, owner, nil), 200)
	if len(envs) != 2 || envs[1].Name != "lint-pc-2" || !envs[1].Online {
		t.Errorf("environments = %+v", envs)
	}
}

func TestPairingDeniedOrExpired(t *testing.T) {
	h := newTestHub(t)
	owner := h.claim()
	denied := h.pair(hubapi.HubPairRequest{Kind: hubapi.PairDevice, Label: "phishing"})
	decode[hubapi.HubPairingInfo](t, h.do("POST", hubapi.PathPairings+"/"+denied.Code+"/deny", owner, nil), 200)
	if r := h.poll(denied); r.Status != hubapi.PairDenied || r.Session != nil {
		t.Errorf("denied: %+v", r)
	}

	late := h.pair(hubapi.HubPairRequest{Kind: hubapi.PairDevice, Label: "slow"})
	h.server.mu.Lock()
	h.server.pairings[late.Code].expires = time.Now().Add(-time.Second)
	h.server.mu.Unlock()
	if resp := h.do("POST", hubapi.PathPairings+"/"+late.Code+"/approve", owner, nil); resp.StatusCode != 404 {
		t.Errorf("approving an expired pairing: %d", resp.StatusCode)
	}
	if r := h.poll(late); r.Status != hubapi.PairExpired {
		t.Errorf("expired: %+v", r)
	}

	for _, req := range []hubapi.HubPairRequest{{Kind: "admin"}, {}} {
		if resp := h.do("POST", hubapi.PathPair, "", req); resp.StatusCode != 400 {
			t.Errorf("kind %q: %d", req.Kind, resp.StatusCode)
		}
	}
	if resp := h.do("POST", hubapi.PathPairToken, "", hubapi.HubPairTokenRequest{Secret: hubapi.PairingSecretPrefix + "guess"}); resp.StatusCode != 404 {
		t.Errorf("a made-up secret: %d", resp.StatusCode)
	}
	// One address can't pile pairings up.
	status := 0
	for range maxPairingsPerAddress + 1 {
		status = h.do("POST", hubapi.PathPair, "", hubapi.HubPairRequest{Kind: hubapi.PairDevice}).StatusCode
	}
	if status != http.StatusTooManyRequests {
		t.Errorf("after %d pairings from one address: %d", maxPairingsPerAddress+1, status)
	}
}

func TestCodes(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		c := newCode()
		if normalizeCode(c) != c || strings.ContainsAny(c, "AEIOUY0123456789") {
			t.Fatalf("code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 990 {
		t.Errorf("only %d different codes in 1000", len(seen))
	}
	for in, want := range map[string]string{"bcdf ghjk": "BCDF-GHJK", "BCDFGHJK": "BCDF-GHJK", "bcd": "", "BCDF-GHJKL": ""} {
		if got := normalizeCode(in); got != want {
			t.Errorf("normalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[[2]string]string{{"", "Lint-PC.local"}: "lint-pc", {"My VPS!", ""}: "my-vps", {"", ""}: "environment", {"", "a.b"}: "a"} {
		if got := environmentName(in[0], in[1]); got != want {
			t.Errorf("environmentName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A hub from before pairing keeps its accounts: their sessions become devices
// with IDs, they still sign in with their password, and the oldest account
// is the owner, which approves pairings.
func TestAccountsFromBeforePairing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := HashPassword("correct horse battery")
	stmts := []string{`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`}
	for _, m := range migrations[:2] {
		stmts = append(stmts, strings.Split(m, ";")...)
	}
	stmts = append(stmts,
		`INSERT INTO schema_migrations (version) VALUES (1), (2)`,
		fmt.Sprintf(`INSERT INTO users VALUES ('usr_old', 'old@example.com', 'Old', '%s', 1)`, hash),
		fmt.Sprintf(`INSERT INTO sessions VALUES ('%s', 'usr_old', 'desktop app', 1, %d)`, hashToken(hubapi.SessionTokenPrefix+"old"), time.Now().Add(time.Hour).UnixMilli()),
	)
	for _, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = db.Close()

	store, err := OpenStore(context.Background(), "sqlite:"+path)
	if err != nil {
		t.Fatal(err)
	}
	h := &testHub{t: t, server: &Server{Store: store}, store: store}
	h.http = httptest.NewServer(h.server.Handler())
	t.Cleanup(func() {
		h.http.Close()
		_ = store.Close()
	})

	old := hubapi.SessionTokenPrefix + "old"
	devices := decode[[]hubapi.HubDevice](t, h.do("GET", hubapi.PathDevices, old, nil), 200)
	if len(devices) != 1 || !strings.HasPrefix(devices[0].ID, "dev_") || devices[0].Label != "desktop app" || !devices[0].Current {
		t.Errorf("the old session as a device: %+v", devices)
	}
	info := decode[hubapi.HubInfo](t, h.do("GET", hubapi.PathInfo, "", nil), 200)
	if !info.Claimed || !info.Passwords {
		t.Errorf("info = %+v", info)
	}
	decode[hubapi.HubSession](t, h.do("POST", hubapi.PathLogin, "", hubapi.HubLoginRequest{Email: "old@example.com", Password: "correct horse battery"}), 200)
	p := h.pair(hubapi.HubPairRequest{Kind: hubapi.PairDevice, Label: "new app"})
	decode[hubapi.HubPairingInfo](t, h.do("POST", hubapi.PathPairings+"/"+p.Code+"/approve", old, nil), 200)
	if r := h.poll(p); r.Session == nil || r.Session.User.Email != "old@example.com" {
		t.Errorf("a device the old account approved: %+v", r)
	}
	if _, claim, _ := NewSetupLink(context.Background(), store, "http://hub"); claim {
		t.Error("a hub with accounts made a claim link")
	}
}

func TestPages(t *testing.T) {
	h := newTestHub(t)
	get := func(path, token string, header ...string) string {
		t.Helper()
		resp := h.do("GET", path, token, nil, header...)
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
		return string(body)
	}
	if body := get("/hub/", ""); !strings.Contains(body, "no owner yet") {
		t.Error("an unclaimed hub's page doesn't say it has no owner")
	}
	if body := get(hubapi.PathSetupPage, ""); !strings.Contains(body, "Make this hub mine") {
		t.Error("the setup page doesn't offer to claim the hub")
	}
	owner := h.claim()
	if body := get("/hub/", owner); !strings.Contains(body, "Approve a device") {
		t.Error("the owner's page has no way to approve a device")
	}
	if body := get("/hub/connect", ""); !strings.Contains(body, "Connect this browser") {
		t.Error("the connect page")
	}
	// The code from the link is shown, read however it was typed.
	if body := get(hubapi.PathApprovePage+"?code=bcdf-ghjk", ""); !strings.Contains(body, "BCDF-GHJK") || !strings.Contains(body, "isn't signed in") {
		t.Error("the approval page for a browser that isn't signed in")
	}
	if body := get("/hub/connect?next=//evil.example/", ""); strings.Contains(body, "evil.example") {
		t.Error("the connect page goes on to another site")
	}

	// A page that got a device token keeps it in the cookie, from the hub's
	// own pages only.
	p := h.pair(hubapi.HubPairRequest{Kind: hubapi.PairDevice, Label: "Firefox on Linux"})
	if resp := h.do("GET", "/hub/qr.svg?code="+p.Code, "", nil); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/svg+xml" {
		t.Errorf("the QR code: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	h.do("POST", hubapi.PathPairings+"/"+p.Code+"/approve", owner, nil)
	token := h.poll(p).Session.Token
	if resp := h.do("POST", "/hub/session", "", map[string]string{"token": token}, "Origin", "https://evil.example"); resp.StatusCode != 403 {
		t.Errorf("setting the cookie from another site: %d", resp.StatusCode)
	}
	resp := h.do("POST", "/hub/session", "", map[string]string{"token": token}, "Origin", h.http.URL)
	if resp.StatusCode != 204 || !strings.Contains(resp.Header.Get("Set-Cookie"), token) {
		t.Errorf("setting the cookie: %d %q", resp.StatusCode, resp.Header.Get("Set-Cookie"))
	}
	if resp := h.do("POST", "/hub/session", "", map[string]string{"token": hubapi.SessionTokenPrefix + "made-up"}, "Origin", h.http.URL); resp.StatusCode != 401 {
		t.Errorf("a made-up token: %d", resp.StatusCode)
	}
}
