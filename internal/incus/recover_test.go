package incus

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRecoverLeftoversTakesBackOnlyWhatItIsAskedFor(t *testing.T) {
	ctx := context.Background()
	f := startFakeAPI(t)
	var c Client

	// Nothing lost: nothing asked of Incus but to look.
	got, err := c.RecoverLeftovers(ctx, "base-next")
	if err != nil || got != nil {
		t.Fatalf("RecoverLeftovers() = %v, %v with nothing lost", got, err)
	}

	// Lost, and one of ours: taken back, and then an instance like any other.
	f.mu.Lock()
	f.lost = []string{"base-next"}
	f.mu.Unlock()
	got, err = c.RecoverLeftovers(ctx, "base-next", "base-old")
	if err != nil || !slices.Equal(got, []string{"base-next"}) {
		t.Fatalf("RecoverLeftovers() = %v, %v", got, err)
	}
	if _, err := c.Instance(ctx, "base-next"); err != nil {
		t.Fatalf("after taking it back: %v", err)
	}
	imports := 0
	for _, change := range f.recorded() {
		if strings.HasPrefix(change, "POST /internal/recover/import ") {
			imports++
			if !strings.Contains(change, `"name":"default"`) || !strings.Contains(change, `"driver":"btrfs"`) || !strings.Contains(change, `"source":"/pool"`) {
				t.Errorf("import asked for %s, not the default pool", change)
			}
		}
	}
	if imports != 1 {
		t.Errorf("imported %d times:\n%s", imports, strings.Join(f.recorded(), "\n"))
	}

	// Lost with another that isn't ours: taking back would bring both.
	f.mu.Lock()
	f.lost = []string{"base-old", "someone-else"}
	f.mu.Unlock()
	_, err = c.RecoverLeftovers(ctx, "base-next", "base-old")
	if err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("RecoverLeftovers() = %v, want it to name someone-else", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.lost) != 2 {
		t.Errorf("imported anyway: %v left lost", f.lost)
	}
}

// A dependency gone between validating and importing: Incus imports nothing
// and says so in its answer, which isn't taken for having taken it back.
func TestRecoverLeftoversSaysWhenTheImportTookNothingBack(t *testing.T) {
	f := startFakeAPI(t)
	var c Client
	f.mu.Lock()
	f.lost = []string{"base-next"}
	f.importDeps = []string{"network incusbr0 is missing"}
	f.mu.Unlock()
	got, err := c.RecoverLeftovers(context.Background(), "base-next")
	if !errors.Is(err, ErrCantRecover) || !strings.Contains(err.Error(), "incusbr0") || got != nil {
		t.Fatalf("RecoverLeftovers() = %v, %v", got, err)
	}
}
