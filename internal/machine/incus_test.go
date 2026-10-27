package machine

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"agentbox/internal/incus"
)

// TestInfoReadsTheIncusInstance checks the adapter's translation, which is all
// it does: the manager reads State, IPv4 and LastUsedAt and nothing else.
func TestInfoReadsTheIncusInstance(t *testing.T) {
	inst := incus.Instance{
		Name:       "ab-demo-agent-01",
		Status:     "Running",
		LastUsedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}
	got := info(inst)
	if got.Ref != inst.Name || got.State != "Running" || !got.Running() || got.Paused() {
		t.Errorf("info() = %+v, want a running machine named %s", got, inst.Name)
	}
	if !got.LastUsedAt.Equal(inst.LastUsedAt) {
		t.Errorf("info().LastUsedAt = %v, want %v", got.LastUsedAt, inst.LastUsedAt)
	}
}

// TestNotFoundKeepsItsCause checks that a caller can ask whether a machine is
// gone without importing Incus, and still read Incus' own words when it isn't.
func TestNotFoundKeepsItsCause(t *testing.T) {
	if err := notFound(nil); err != nil {
		t.Errorf("notFound(nil) = %v, want nil", err)
	}
	missing := notFound(fmt.Errorf("getting instance: %w", incus.ErrNotFound))
	if !errors.Is(missing, ErrNotFound) {
		t.Errorf("notFound(ErrNotFound) = %v, want ErrNotFound", missing)
	}
	other := notFound(errors.New("incus isn't answering"))
	if errors.Is(other, ErrNotFound) {
		t.Errorf("notFound(other) = %v, want it not to be ErrNotFound", other)
	}
	if other.Error() != "incus isn't answering" {
		t.Errorf("notFound(other) = %q, want the error unchanged", other)
	}
}
