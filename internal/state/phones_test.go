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
