package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDeviceGitHub is github.com's two device-flow endpoints, answering each
// poll with the next of polls.
type fakeDeviceGitHub struct {
	mu     sync.Mutex
	polls  []map[string]any
	scopes string
	asked  int
}

func (f *fakeDeviceGitHub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("%s asked for %q, want JSON", r.URL.Path, r.Header.Get("Accept"))
		}
		if r.Form.Get("client_secret") != "" {
			t.Errorf("%s sent a client secret", r.URL.Path)
		}
		if r.Form.Get("client_id") != "Iv1.test" {
			t.Errorf("%s client_id = %q", r.URL.Path, r.Form.Get("client_id"))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/login/device/code":
			f.scopes = r.Form.Get("scope")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-123", "user_code": "WDJB-MJHT",
				"verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5,
			})
		case "/login/oauth/access_token":
			if r.Form.Get("device_code") != "dev-123" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("poll form = %v", r.Form)
			}
			if f.asked >= len(f.polls) {
				t.Errorf("polled %d times, past the script", f.asked+1)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(f.polls[f.asked])
			f.asked++
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pending() map[string]any { return map[string]any{"error": "authorization_pending"} }

func TestDeviceFlowOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		polls  []map[string]any
		token  string
		err    error
		waited []time.Duration
	}{
		{
			name:   "approved after a wait",
			polls:  []map[string]any{pending(), pending(), {"access_token": "gho_abc", "token_type": "bearer"}},
			token:  "gho_abc",
			waited: []time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second},
		},
		{
			name:   "slow_down with an interval moves to it",
			polls:  []map[string]any{{"error": "slow_down", "interval": 10}, {"access_token": "gho_abc"}},
			token:  "gho_abc",
			waited: []time.Duration{5 * time.Second, 10 * time.Second},
		},
		{
			name:   "slow_down with none adds five seconds, each time",
			polls:  []map[string]any{{"error": "slow_down"}, {"error": "slow_down"}, {"access_token": "gho_abc"}},
			token:  "gho_abc",
			waited: []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second},
		},
		{
			name:   "expired",
			polls:  []map[string]any{pending(), {"error": "expired_token"}},
			err:    ErrDeviceExpired,
			waited: []time.Duration{5 * time.Second, 5 * time.Second},
		},
		{
			name:   "denied",
			polls:  []map[string]any{{"error": "access_denied"}},
			err:    ErrDeviceDenied,
			waited: []time.Duration{5 * time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDeviceGitHub{polls: tc.polls}
			srv := fake.serve(t)
			var waited []time.Duration
			d := DeviceFlow{ClientID: "Iv1.test", BaseURL: srv.URL, Wait: func(_ context.Context, d time.Duration) error {
				waited = append(waited, d)
				return nil
			}}
			code, err := d.Start(context.Background(), DeviceScopes)
			if err != nil {
				t.Fatal(err)
			}
			if code.UserCode != "WDJB-MJHT" || code.VerificationURI != "https://github.com/login/device" {
				t.Errorf("code = %+v", code)
			}
			if fake.scopes != "repo read:org workflow gist" {
				t.Errorf("scopes = %q", fake.scopes)
			}
			token, err := d.Poll(context.Background(), code)
			if !errors.Is(err, tc.err) || token != tc.token {
				t.Fatalf("Poll = %q, %v; want %q, %v", token, err, tc.token, tc.err)
			}
			if !slices.Equal(waited, tc.waited) {
				t.Errorf("waited %v, want %v", waited, tc.waited)
			}
		})
	}
}

func TestDeviceFlowStopsWhenTheCodeRunsOut(t *testing.T) {
	fake := &fakeDeviceGitHub{}
	srv := fake.serve(t)
	d := DeviceFlow{ClientID: "Iv1.test", BaseURL: srv.URL, Wait: func(context.Context, time.Duration) error { return nil }}
	_, err := d.Poll(context.Background(), DeviceCode{DeviceCode: "dev-123", ExpiresAt: time.Now().Add(-time.Second)})
	if !errors.Is(err, ErrDeviceExpired) || fake.asked != 0 {
		t.Errorf("Poll past expiry = %v after %d polls", err, fake.asked)
	}
}

func TestDeviceFlowCancel(t *testing.T) {
	fake := &fakeDeviceGitHub{polls: []map[string]any{pending()}}
	srv := fake.serve(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := DeviceFlow{ClientID: "Iv1.test", BaseURL: srv.URL}
	go cancel()
	_, err := d.Poll(ctx, DeviceCode{DeviceCode: "dev-123", Interval: 60})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Poll cancelled = %v", err)
	}
}

func TestDeviceFlowRefusals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "device_flow_disabled", "error_description": "Device Flow must be explicitly enabled"})
	}))
	t.Cleanup(srv.Close)
	d := DeviceFlow{ClientID: "Iv1.test", BaseURL: srv.URL}
	if _, err := d.Start(context.Background(), DeviceScopes); err == nil || !strings.Contains(err.Error(), "Enable Device Flow") {
		t.Errorf("Start with device flow off = %v", err)
	}
	if _, err := (DeviceFlow{BaseURL: srv.URL}).Start(context.Background(), DeviceScopes); !errors.Is(err, ErrNoClientID) {
		t.Errorf("Start with no client ID = %v", err)
	}
}

func TestClientIDFromEnv(t *testing.T) {
	t.Setenv("AGENTBOX_GITHUB_CLIENT_ID", "")
	want := clientID
	if clientID == ClientIDPlaceholder {
		want = ""
	}
	if got := ClientID(); got != want {
		t.Errorf("ClientID() = %q, want %q", got, want)
	}
	t.Setenv("AGENTBOX_GITHUB_CLIENT_ID", " Ov23liOverride ")
	if got := ClientID(); got != "Ov23liOverride" {
		t.Errorf("ClientID() = %q", got)
	}
}
