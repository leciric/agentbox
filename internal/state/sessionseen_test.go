package state

import (
	"context"
	"testing"
	"time"
)

// A session's seen time only moves on: a window reporting a moment it showed
// the session after the phone had seen it later keeps the phone's.
func TestSeeLeadSessionKeepsTheLatest(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	early, late := time.UnixMilli(1_000_000), time.UnixMilli(2_000_000)
	if got, err := st.SeeLeadSession(ctx, "pawly", "main", late); err != nil || !got.Equal(late) {
		t.Fatalf("first seen = %v, %v", got, err)
	}
	if got, err := st.SeeLeadSession(ctx, "pawly", "main", early); err != nil || !got.Equal(late) {
		t.Fatalf("seen earlier = %v, %v; want %v kept", got, err, late)
	}
	if _, err := st.SeeLeadSession(ctx, "other", "s2", early); err != nil {
		t.Fatal(err)
	}
	seen, err := st.LeadSessionsSeen(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !seen["main"].Equal(late) {
		t.Fatalf("pawly's seen = %v", seen)
	}
}
