package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
)

func TestPhones(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	p, token, err := st.AddPhone(ctx, "  "+strings.Repeat("é", 100)+" ")
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(p.Name)) != state.MaxPhoneNameLen || !strings.HasPrefix(token, p.ID+".") {
		t.Fatalf("AddPhone = %+v, %q", p, token)
	}
	got, err := st.PhoneByToken(ctx, token)
	if err != nil || got.ID != p.ID || !got.LastSeen.IsZero() {
		t.Fatalf("PhoneByToken = %+v, %v", got, err)
	}
	// Anything but the token itself is nobody.
	for _, bad := range []string{"", p.ID, token + "x", strings.TrimPrefix(token, p.ID+".")} {
		if _, err := st.PhoneByToken(ctx, bad); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("PhoneByToken(%q) = %v, want ErrNotFound", bad, err)
		}
	}
	at := time.Unix(1_800_000_000, 0)
	if err := st.SawPhone(ctx, p.ID, "192.168.1.20", at); err != nil {
		t.Fatal(err)
	}
	second, _, err := st.AddPhone(ctx, "")
	if err != nil || second.Name != "Phone" {
		t.Fatalf("AddPhone(\"\") = %+v, %v", second, err)
	}
	list, err := st.Phones(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("Phones = %+v, %v", list, err)
	}
	for _, ph := range list {
		if ph.ID == p.ID && (!ph.LastSeen.Equal(at) || ph.LastAddr != "192.168.1.20") {
			t.Errorf("after SawPhone: %+v", ph)
		}
	}
	if err := st.RemovePhone(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PhoneByToken(ctx, token); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a removed phone's token still works: %v", err)
	}
	if err := st.RemovePhone(ctx, p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RemovePhone twice = %v", err)
	}
}

func TestPhoneScopesAndAudit(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	p, token, err := st.AddPhone(ctx, "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Scopes, ",") != "read,chat,answer" || p.Can(state.PhoneScopeMerge) {
		t.Fatalf("a new phone's scopes = %v", p.Scopes)
	}
	if _, err := st.SetPhoneScopes(ctx, p.ID, []string{"read", "terminal"}); err == nil {
		t.Error("SetPhoneScopes took a scope that isn't one")
	}
	if _, err := st.SetPhoneScopes(ctx, "phone_nobody", []string{"read"}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetPhoneScopes(nobody) = %v, want ErrNotFound", err)
	}
	got, err := st.SetPhoneScopes(ctx, p.ID, []string{"merge", "read", "merge"})
	if err != nil || strings.Join(got.Scopes, ",") != "read,merge" {
		t.Fatalf("SetPhoneScopes = %+v, %v", got, err)
	}
	if got, _ := st.PhoneByToken(ctx, token); !got.Can(state.PhoneScopeMerge) || got.Can(state.PhoneScopeChat) {
		t.Errorf("PhoneByToken's scopes = %v", got.Scopes)
	}

	for i, route := range []string{"POST /v1/a", "POST /v1/b", "POST /v1/c"} {
		e := state.PhoneAuditEntry{PhoneID: p.ID, PhoneName: p.Name, Scope: "chat", Route: route, Status: 200, Result: "ok", Addr: "10.0.0.2", At: time.Unix(int64(1_800_000_000+i), 0)}
		if err := st.AddPhoneAudit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.PhoneAudit(ctx, p.ID, 0, 2)
	if err != nil || len(page) != 2 || page[0].Route != "POST /v1/c" || page[1].Route != "POST /v1/b" {
		t.Fatalf("PhoneAudit = %+v, %v", page, err)
	}
	rest, _ := st.PhoneAudit(ctx, p.ID, page[1].ID, 2)
	if len(rest) != 1 || rest[0].Route != "POST /v1/a" || rest[0].At.Unix() != 1_800_000_000 {
		t.Fatalf("PhoneAudit's next page = %+v", rest)
	}
	// Append-only: nothing changes or removes an entry, unpairing included.
	if _, err := st.DB().ExecContext(ctx, `UPDATE phone_audit SET result = 'ok' WHERE id = ?`, rest[0].ID); err == nil {
		t.Error("an audit entry was changed")
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM phone_audit`); err == nil {
		t.Error("the audit was deleted")
	}
	if err := st.RemovePhone(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.PhoneAudit(ctx, p.ID, 0, 10); len(all) != 3 {
		t.Errorf("after unpairing, the audit has %d entries, want 3", len(all))
	}
}
