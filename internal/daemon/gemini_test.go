package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// useGeminiMenu answers for Gemini CLI's model menu instead of installing it.
func useGeminiMenu(d testDaemon) {
	d.srv.mu.Lock()
	defer d.srv.mu.Unlock()
	d.srv.geminiModelsFor = func(context.Context) ([]acp.ConfigChoice, error) {
		return []acp.ConfigChoice{{Value: "auto", Name: "Auto"}, {Value: "gemini-3.1-pro-preview", Name: "gemini-3.1-pro-preview"}}, nil
	}
}

// A Gemini API key is an account like Codex's: Setup and the auth status say
// agents can run Gemini CLI, and the model menu is asked for.
func TestGeminiAPIKeyAccount(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	useGeminiMenu(d)
	ctx := context.Background()
	if c := setupCheck(t, d, "gemini"); c.Status != api.SetupOptional || c.Fix != "agentbox auth gemini" {
		t.Errorf("before signing in: %+v", c)
	}
	if err := d.client.SaveToolAccount(ctx, "gemini", api.ToolAccountRequest{APIKey: "  "}); err == nil {
		t.Error("an empty key was taken")
	}
	if err := d.client.SaveToolAccount(ctx, "gemini", api.ToolAccountRequest{Account: "studio", APIKey: "AIza-test\n"}); err != nil {
		t.Fatal(err)
	}
	if c := setupCheck(t, d, "gemini"); c.Status != api.SetupOK {
		t.Errorf("after signing in: %+v", c)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil || !auth.Gemini || len(auth.GeminiAccounts) != 1 || auth.GeminiAccounts[0].Kind != "apikey" || !auth.GeminiAccounts[0].Default {
		t.Errorf("Auth = %+v, %v", auth, err)
	}
	eventually(t, "the model menu arriving", func() bool {
		s, err := d.client.Settings(ctx)
		return err == nil && s.GeminiReady && len(s.GeminiModelChoices) == 2
	})
	out, err := patchSettings(t, d, `{"defaultGeminiModel":"gemini-3.1-pro-preview"}`)
	if err != nil || out.DefaultGeminiModel != "gemini-3.1-pro-preview" {
		t.Errorf("the default model: %q, %v", out.DefaultGeminiModel, err)
	}
	if err := d.client.RemoveToolAccount(ctx, "gemini", "", "studio"); err != nil {
		t.Fatal(err)
	}
	if s, err := d.client.Settings(ctx); err != nil || s.GeminiReady {
		t.Errorf("after signing out: ready %v, %v", s.GeminiReady, err)
	}
	if _, err := d.client.StartGeminiLogin(ctx, "bad name!"); err == nil || !strings.Contains(err.Error(), "account") {
		t.Errorf("a sign-in for an invalid account name: %v", err)
	}
}
