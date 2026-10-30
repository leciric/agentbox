package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"agentbox/internal/state"
)

func TestPhonePush(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	p, _, err := st.AddPhone(ctx, "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PhonePushOf(ctx, p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("PhonePushOf before registering = %v, want ErrNotFound", err)
	}
	want := state.PhonePush{PhoneID: p.ID, Token: "ExponentPushToken[a]", Kinds: []string{"needs_you", "ci_failed"}, QuietFrom: 22 * 60, QuietTo: 7 * 60, TimeZone: "America/Sao_Paulo"}
	if err := st.SetPhonePush(ctx, want); err != nil {
		t.Fatal(err)
	}
	// Registering again replaces it.
	want.Kinds = []string{"pr_ready"}
	if err := st.SetPhonePush(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.PhonePushOf(ctx, p.ID)
	if err != nil || got.Token != want.Token || !slices.Equal(got.Kinds, want.Kinds) || got.QuietFrom != 22*60 || got.QuietTo != 7*60 || got.TimeZone != want.TimeZone {
		t.Fatalf("PhonePushOf = %+v, %v", got, err)
	}
	all, err := st.PhonePushes(ctx)
	if err != nil || len(all) != 1 || all[0].Phone.Name != "Pixel" || !all[0].Phone.Can(state.PhoneScopeRead) {
		t.Fatalf("PhonePushes = %+v, %v", all, err)
	}

	// A token the push service gave up on is forgotten.
	if err := st.DropPushToken(ctx, want.Token); err != nil {
		t.Fatal(err)
	}
	if got, err := st.PhonePushOf(ctx, p.ID); err != nil || got.Token != "" || !slices.Equal(got.Kinds, want.Kinds) {
		t.Fatalf("PhonePushOf after DropPushToken = %+v, %v; want its choices kept, no token", got, err)
	}

	// Unpairing the phone takes its pushes with it.
	if err := st.SetPhonePush(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := st.RemovePhone(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if all, err := st.PhonePushes(ctx); err != nil || len(all) != 0 {
		t.Fatalf("PhonePushes after unpairing = %+v, %v", all, err)
	}
}
