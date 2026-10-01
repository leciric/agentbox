package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenAuthMethod(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		supported []string
		want      string
	}{
		{nil, "none"},
		{[]string{"client_secret_basic", "none"}, "none"},
		{[]string{"client_secret_basic", "client_secret_post"}, "client_secret_post"}, // Figma's
		{[]string{"client_secret_basic"}, "client_secret_basic"},
	} {
		if got := tokenAuthMethod(tc.supported); got != tc.want {
			t.Errorf("tokenAuthMethod(%v) = %s, want %s", tc.supported, got, tc.want)
		}
	}
}

// A confidential client sends its secret in a basic header, and a server that
// sends expires_in as a string is understood; a refused grant is ErrGrant.
func TestTokenRequests(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// RFC 6749 2.3.1: the credentials are form-encoded before they go
		// in the header.
		id, secret, ok := r.BasicAuth()
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
		switch {
		case !ok || id != "client id" || secret != "s3cret" || r.PostForm.Get("client_id") != "":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
		case r.PostForm.Get("refresh_token") == "spent":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"refresh token expired"}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"a","token_type":"Bearer","expires_in":"3600"}`)
		}
	}))
	defer srv.Close()
	c := Client{ID: "client id", Secret: "s3cret", AuthMethod: "client_secret_basic"}
	tok, err := OAuth{Loopback: true}.Refresh(context.Background(), srv.URL, c, "good", "https://x.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "a" || tok.Refresh != "" || time.Until(tok.Expires) < 59*time.Minute {
		t.Errorf("Refresh() = %+v", tok)
	}
	_, err = OAuth{Loopback: true}.Refresh(context.Background(), srv.URL, c, "spent", "https://x.test/mcp")
	if !errors.Is(err, ErrGrant) || !strings.Contains(err.Error(), "refresh token expired") {
		t.Errorf("a refused refresh = %v", err)
	}
	c.Secret = "wrong"
	if _, err := (OAuth{Loopback: true}).Refresh(context.Background(), srv.URL, c, "good", "https://x.test/mcp"); !errors.Is(err, ErrGrant) {
		t.Errorf("a refused client = %v", err)
	}
}

func TestWellKnown(t *testing.T) {
	t.Parallel()
	got := wellKnown("https://mcp.notion.com/mcp/", "oauth-protected-resource")
	if strings.Join(got, " ") != "https://mcp.notion.com/.well-known/oauth-protected-resource/mcp https://mcp.notion.com/.well-known/oauth-protected-resource" {
		t.Errorf("wellKnown() = %v", got)
	}
	if got := canonical("HTTPS://MCP.Notion.com/mcp#x"); got != "https://mcp.notion.com/mcp" {
		t.Errorf("canonical() = %s", got)
	}
}

// TestRedirectsArentFollowedWithSecrets has an https token endpoint redirect
// to plain http: the refresh token and client secret must not follow it, and
// nor may a GET go from https to http.
func TestRedirectsArentFollowedWithSecrets(t *testing.T) {
	t.Parallel()
	var leaked atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer tlsSrv.Close()
	o := OAuth{HTTP: tlsSrv.Client(), Loopback: true}
	c := Client{ID: "agentbox", Secret: "shh", AuthMethod: "client_secret_post"}
	if _, err := o.Refresh(context.Background(), tlsSrv.URL+"/token", c, "refresh", "https://x.test/mcp"); err == nil {
		t.Error("a refresh redirected to http succeeded")
	}
	if _, err := o.getJSON(context.Background(), tlsSrv.URL+"/.well-known/oauth-authorization-server", &ServerMetadata{}); err == nil {
		t.Error("a GET redirected from https to http was followed")
	}
	if n := leaked.Load(); n != 0 {
		t.Errorf("the plain http server got %d requests", n)
	}
}

// TestThisMachineIsntAConnector: URLs on this machine are refused, and so is
// dialing it, unless Loopback (tests) allows them.
func TestThisMachineIsntAConnector(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"http://localhost:8080/mcp", "https://127.0.0.1/mcp", "https://app.localhost/mcp", "https://[::1]/mcp", "https://169.254.169.254/latest"} {
		if err := CheckURL(u); err == nil {
			t.Errorf("CheckURL(%s) passed", u)
		}
		if err := (OAuth{Loopback: true}).CheckURL(u); err != nil && !strings.Contains(u, "169.254") {
			t.Errorf("with Loopback, CheckURL(%s) = %v", u, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if conn, err := (OAuth{}).Dialer().DialContext(context.Background(), "tcp", srv.Listener.Addr().String()); err == nil {
		_ = conn.Close()
		t.Error("dialed this machine's loopback")
	}
	conn, err := (OAuth{Loopback: true}).Dialer().DialContext(context.Background(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("with Loopback: %v", err)
	}
	_ = conn.Close()
}

// TestServerMetadataMustBeTheIssuers checks RFC 8414 §3.3's issuer check, and
// that a server whose metadata doesn't say it does PKCE with S256 is refused.
func TestServerMetadataMustBeTheIssuers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		what   string
		issuer func(self string) string
		pkce   []string
		want   string
	}{
		{"another issuer", func(string) string { return "https://evil.example" }, []string{"S256"}, "is for the issuer"},
		{"no PKCE methods", func(self string) string { return self }, nil, "PKCE"},
		{"only plain PKCE", func(self string) string { return self }, []string{"plain"}, "PKCE"},
		{"good", func(self string) string { return self }, []string{"S256"}, ""},
	} {
		var self string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/mcp":
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+self+`/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
			case "/.well-known/oauth-protected-resource/mcp":
				writeTestJSON(w, map[string]any{"resource": self + "/mcp", "authorization_servers": []string{self}})
			case "/.well-known/oauth-authorization-server":
				writeTestJSON(w, map[string]any{"issuer": tc.issuer(self), "authorization_endpoint": self + "/authorize",
					"token_endpoint": self + "/token", "code_challenge_methods_supported": tc.pkce})
			default:
				http.NotFound(w, r)
			}
		}))
		self = srv.URL
		_, err := OAuth{Loopback: true}.Discover(context.Background(), srv.URL+"/mcp")
		srv.Close()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.what, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: Discover = %v, want an error about %q", tc.what, err, tc.want)
		}
	}
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
