package hub

import (
	"context"
	"testing"

	"agentbox/hubapi"
)

func TestResetPasswordEndsSessions(t *testing.T) {
	h := newTestHub(t)
	token := h.signIn("owner@example.com")
	if resp := h.do("GET", "/v1/me", token, nil); resp.StatusCode != 200 {
		t.Fatalf("before the reset: %d", resp.StatusCode)
	}
	if _, err := ResetPassword(context.Background(), h.store, "Owner@Example.com", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if resp := h.do("GET", "/v1/me", token, nil); resp.StatusCode != 401 {
		t.Errorf("the old session after the reset: %d, want 401", resp.StatusCode)
	}
	if resp := h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: "owner@example.com", Password: "correct horse battery"}); resp.StatusCode != 401 {
		t.Errorf("the old password after the reset: %d, want 401", resp.StatusCode)
	}
	decode[hubapi.HubSession](t, h.do("POST", "/v1/auth/login", "", hubapi.HubLoginRequest{Email: "owner@example.com", Password: "a brand new password"}), 200)
	if _, err := ResetPassword(context.Background(), h.store, "nobody@example.com", "a brand new password"); err == nil {
		t.Error("reset the password of an account that doesn't exist")
	}
}
