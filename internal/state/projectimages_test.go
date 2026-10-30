package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

// A project image is the project's own base on a cluster: one is current, the
// one before it is kept for a single revert, and removing either is what the
// CLI's base commands do.
func TestProjectImageRoundTripsAndKeepsOneBack(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	if _, ok, err := st.ProjectImage(ctx, "pawly"); err != nil || ok {
		t.Fatalf("a project with no image = %v, %v; want none, nil", ok, err)
	}

	first := state.ProjectImage{
		Project: "pawly", Image: "hub.example/pawly-agent:20261027-100000",
		Digest: "sha256:1111", SavedFrom: "pawly/agent-01",
		SavedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := st.SetProjectImage(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.ProjectImage(ctx, "pawly")
	if err != nil || !ok {
		t.Fatalf("ProjectImage() = %v, %v; want the saved one", ok, err)
	}
	if got != first {
		t.Errorf("ProjectImage() = %+v, want %+v", got, first)
	}
	if _, ok, err := st.PreviousProjectImage(ctx, "pawly"); err != nil || ok {
		t.Errorf("the first save kept something to revert to: %v, %v", ok, err)
	}
	if want := first.Image + "@" + first.Digest; got.Ref() != want {
		t.Errorf("Ref() = %q, want the digest form %q", got.Ref(), want)
	}

	second := first
	second.Image = "hub.example/pawly-agent:20261027-110000"
	second.Digest = "sha256:2222"
	second.SavedAt = first.SavedAt.Add(time.Hour)
	if err := st.SetProjectImage(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.ProjectImage(ctx, "pawly")
	if got != second {
		t.Errorf("ProjectImage() = %+v, want the second save", got)
	}
	was, ok, err := st.PreviousProjectImage(ctx, "pawly")
	if err != nil || !ok || was != first {
		t.Errorf("PreviousProjectImage() = %+v, %v, %v; want the first save", was, ok, err)
	}

	back, err := st.RevertProjectImage(ctx, "pawly")
	if err != nil {
		t.Fatalf("RevertProjectImage() = %v", err)
	}
	if back.Image != first.Image || back.SavedFrom != first.SavedFrom || !back.SavedAt.Equal(first.SavedAt) {
		t.Errorf("RevertProjectImage() = %+v, want %+v", back, first)
	}
	if _, ok, err := st.PreviousProjectImage(ctx, "pawly"); err != nil || ok {
		t.Errorf("a revert left something to revert to: %v, %v", ok, err)
	}
	if _, err := st.RevertProjectImage(ctx, "pawly"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("a second revert = %v, want ErrNotFound", err)
	}

	// A save after a revert keeps what it replaced, and dropping only that
	// leaves the project on its image.
	third := second
	third.Image = "hub.example/pawly-agent:20261027-120000"
	if err := st.SetProjectImage(ctx, third); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveProjectImage(ctx, "pawly", true); err != nil {
		t.Fatalf("RemoveProjectImage(previous) = %v", err)
	}
	if got, _, _ := st.ProjectImage(ctx, "pawly"); got != third {
		t.Errorf("dropping the previous image changed the current one: %+v", got)
	}
	if _, ok, err := st.PreviousProjectImage(ctx, "pawly"); err != nil || ok {
		t.Errorf("the previous image survived its removal: %v, %v", ok, err)
	}
	if err := st.RemoveProjectImage(ctx, "pawly", false); err != nil {
		t.Fatalf("RemoveProjectImage() = %v", err)
	}
	if _, ok, err := st.ProjectImage(ctx, "pawly"); err != nil || ok {
		t.Errorf("the project image survived its removal: %v, %v", ok, err)
	}
	if err := st.RemoveProjectImage(ctx, "pawly", false); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("removing it twice = %v, want ErrNotFound", err)
	}
}

// TestProjectImageRefWithoutADigest: an image a local runtime imported has no
// registry digest, and its tag is what a pod runs.
func TestProjectImageRefWithoutADigest(t *testing.T) {
	image := state.ProjectImage{Image: "agentbox-agent-pawly:dev"}
	if image.Ref() != image.Image {
		t.Errorf("Ref() = %q, want the tag alone", image.Ref())
	}
}
