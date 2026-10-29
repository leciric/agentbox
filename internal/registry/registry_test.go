package registry_test

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentbox/internal/registry"
)

// fakeRegistry is a small distribution API: /v2/ asks for a bearer token, and
// /token hands one out to the right login. It lives on http, so the check has to
// fall back from https the way an insecure registry needs.
func fakeRegistry(t *testing.T, username, password string, public bool) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if !public {
			user, pass, ok := r.BasicAuth()
			if !ok || user != username || pass != password {
				w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if got := r.URL.Query().Get("service"); got != "fake" {
			t.Errorf("the token request said service %q, want the challenge's", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"a-token"}`))
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/token",service="fake",scope="registry:catalog:*"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func clientTo(server *httptest.Server, username, password string) registry.Client {
	return registry.Client{
		URL:      strings.TrimPrefix(server.URL, "http://") + "/someone",
		Username: username,
		Token:    password,
		Insecure: true,
	}
}

func TestCheckAcceptsTheStoredLogin(t *testing.T) {
	server := fakeRegistry(t, "someone", "a-secret", false)
	result := clientTo(server, "someone", "a-secret").Check(context.Background())
	if !result.OK {
		t.Fatalf("Check() = %+v, want a pass", result)
	}
	if !strings.Contains(result.Detail, "accepted the login") {
		t.Errorf("detail = %q, want it to say the login was accepted", result.Detail)
	}
}

func TestCheckSaysWhenTheLoginIsRefused(t *testing.T) {
	server := fakeRegistry(t, "someone", "a-secret", false)
	result := clientTo(server, "someone", "wrong").Check(context.Background())
	if result.OK {
		t.Fatal("Check() passed with the wrong password")
	}
	if !strings.Contains(result.Detail, "refused the stored login") {
		t.Errorf("detail = %q, want it to say the login was refused", result.Detail)
	}
	if strings.Contains(result.Detail, "wrong") {
		t.Errorf("detail = %q: it quotes the credential", result.Detail)
	}
}

func TestCheckSaysWhenALoginIsNeeded(t *testing.T) {
	server := fakeRegistry(t, "someone", "a-secret", false)
	result := clientTo(server, "", "").Check(context.Background())
	if result.OK {
		t.Fatal("Check() passed with no login")
	}
	if !strings.Contains(result.Detail, "needs a login") {
		t.Errorf("detail = %q, want it to say a login is needed", result.Detail)
	}
}

func TestCheckPassesARegistryThatNeedsNoLogin(t *testing.T) {
	server := fakeRegistry(t, "", "", true)
	result := clientTo(server, "", "").Check(context.Background())
	if !result.OK {
		t.Fatalf("Check() = %+v, want a pass", result)
	}
	if !strings.Contains(result.Detail, "without a login") {
		t.Errorf("detail = %q, want it to say no login was needed", result.Detail)
	}
}

func TestCheckSaysWhenNothingAnswers(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	result := clientTo(server, "", "").Check(context.Background())
	if result.OK {
		t.Fatal("Check() passed against a registry that is gone")
	}
	if !strings.Contains(result.Detail, "connect") && !strings.Contains(result.Detail, "refused") {
		t.Errorf("detail = %q, want the connection error", result.Detail)
	}
}

// A registry that answers with a plain Basic challenge, which is what a small
// self-hosted one without a token service does.
func TestCheckFollowsABasicChallenge(t *testing.T) {
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("someone:a-secret"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != auth {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	result := clientTo(server, "someone", "a-secret").Check(context.Background())
	if !result.OK {
		t.Fatalf("Check() = %+v, want a pass", result)
	}
}

func TestCheckRefusesWhatIsNotARegistry(t *testing.T) {
	c := registry.Client{URL: ""}
	result := c.Check(context.Background())
	if result.OK || !strings.Contains(result.Detail, "no url") {
		t.Errorf("Check() = %+v, want it to say the url is missing", result)
	}
}

// TestCheckSendsNoLoginToAnHTTPRealm is a registry on https whose challenge
// names a token address on http: the stored login must not go there, because
// only a registry added with --insecure may be spoken to in the clear.
func TestCheckSendsNoLoginToAnHTTPRealm(t *testing.T) {
	var asked atomic.Bool
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"a-token"}`))
	}))
	t.Cleanup(realm.Close)
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm.URL+`/token",service="fake"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(reg.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: reg.Certificate().Raw})
	result := registry.Client{
		URL:      strings.TrimPrefix(reg.URL, "https://") + "/someone",
		Username: "someone",
		Token:    "a-secret",
		CA:       ca,
	}.Check(context.Background())
	if result.OK {
		t.Fatalf("Check() = %+v, want a refusal", result)
	}
	if !strings.Contains(result.Detail, "isn't https") {
		t.Errorf("detail = %q, want it to say the token address isn't https", result.Detail)
	}
	if asked.Load() {
		t.Error("the http realm was asked for a token, with the login")
	}
}
