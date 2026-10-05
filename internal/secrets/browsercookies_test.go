package secrets_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentbox/internal/cookieimport"
	"agentbox/internal/secrets"
)

// A project's browser cookies are sealed like its secrets, and stay out of
// everything that deals in environment variables: the env file, the lists,
// the brief's names and a connector's Resolve.
func TestBrowserCookiesAreSealedAndKeptOutOfTheEnv(t *testing.T) {
	s, st, _ := store(t)
	ctx := context.Background()
	if _, ok, err := s.BrowserCookies(ctx, "pawly"); err != nil || ok {
		t.Fatalf("before an import: ok=%v err=%v", ok, err)
	}
	if _, err := s.Set(ctx, "pawly", "", "OPENAI_API_KEY", "sk-1"); err != nil {
		t.Fatal(err)
	}
	in := secretsBrowserCookies()
	if err := s.SetBrowserCookies(ctx, "pawly", in); err != nil {
		t.Fatal(err)
	}

	stored, err := st.ProjectSecrets(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range stored {
		if strings.Contains(string(sec.Value), "sess-abc") {
			t.Errorf("%s is stored in the clear", sec.Name)
		}
	}
	got, ok, err := s.BrowserCookies(ctx, "pawly")
	if err != nil || !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("BrowserCookies = %+v ok=%v err=%v, want %+v", got, ok, err, in)
	}

	values, _ := s.ForAgent(ctx, "pawly", "agent-01")
	names, _ := s.NamesForAgent(ctx, "pawly", "agent-01")
	listed, _ := s.List(ctx, "pawly", "")
	whole, _ := s.Project(ctx, "pawly")
	if len(values) != 1 || len(names) != 1 || len(listed) != 1 || len(whole) != 1 {
		t.Errorf("the cookies show as a secret: %d values, names %v, %d listed, %d in the project", len(values), names, len(listed), len(whole))
	}
	if _, err := s.Resolve(ctx, "pawly", "", "browser-cookies"); err == nil {
		t.Error("Resolve opened the browser cookies")
	}

	if err := s.RemoveBrowserCookies(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.BrowserCookies(ctx, "pawly"); ok {
		t.Error("removed, the import is still there")
	}
	if err := s.RemoveBrowserCookies(ctx, "pawly"); err != nil {
		t.Errorf("removing twice: %v", err)
	}
	if err := s.SetBrowserCookies(ctx, "pawly", secretsBrowserCookies(func(bc *secrets.BrowserCookies) { bc.Cookies = nil })); err == nil {
		t.Error("an import with no cookies was stored")
	}
}

func secretsBrowserCookies(edit ...func(*secrets.BrowserCookies)) secrets.BrowserCookies {
	bc := secrets.BrowserCookies{
		Domains:    []string{"github.com"},
		Format:     cookieimport.FormatNetscape,
		Cookies:    []cookieimport.Cookie{{Domain: "github.com", Name: "user_session", Value: "sess-abc", Path: "/", Secure: true, HTTPOnly: true}},
		ImportedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
	}
	for _, e := range edit {
		e(&bc)
	}
	return bc
}
