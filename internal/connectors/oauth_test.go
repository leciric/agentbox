package connectors

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	tok, err := OAuth{}.Refresh(context.Background(), srv.URL, c, "good", "https://x.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "a" || tok.Refresh != "" || time.Until(tok.Expires) < 59*time.Minute {
		t.Errorf("Refresh() = %+v", tok)
	}
	_, err = OAuth{}.Refresh(context.Background(), srv.URL, c, "spent", "https://x.test/mcp")
	if !errors.Is(err, ErrGrant) || !strings.Contains(err.Error(), "refresh token expired") {
		t.Errorf("a refused refresh = %v", err)
	}
	c.Secret = "wrong"
	if _, err := (OAuth{}).Refresh(context.Background(), srv.URL, c, "good", "https://x.test/mcp"); !errors.Is(err, ErrGrant) {
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
