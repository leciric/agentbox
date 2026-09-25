package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// TestInstallWallpaperOnlyWritesWhenTheHashDiffers checks the half-a-megabyte
// guard: an agent whose wallpaper already matches this binary's isn't written
// to again, and one that doesn't match (or reports nothing at all) is.
func TestInstallWallpaperOnlyWritesWhenTheHashDiffers(t *testing.T) {
	sum := sha256.Sum256(wallpaper)
	want := hex.EncodeToString(sum[:])

	writes := filepath.Join(t.TempDir(), "writes")
	script := `#!/bin/sh
case "$1" in
  exec)
    if [ "$3" = "-T" ]; then cat >/dev/null; echo written >>` + writes + `
    else [ -n "$HASH" ] && printf '%s' "$HASH"; fi ;;
esac`
	incusPath := filepath.Join(t.TempDir(), "incus")
	if err := os.WriteFile(incusPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Incus: incus.Client{Bin: incusPath}}
	a := state.Agent{Instance: "ab-hello-stack-agent-01"}

	t.Setenv("HASH", "")
	m.installWallpaper(context.Background(), a)
	first, err := os.ReadFile(writes)
	if err != nil || len(first) == 0 {
		t.Fatalf("installWallpaper() with no hash reported didn't write: %v, %q", err, first)
	}

	_ = os.Remove(writes)
	t.Setenv("HASH", want)
	m.installWallpaper(context.Background(), a)
	if _, err := os.ReadFile(writes); !os.IsNotExist(err) {
		t.Errorf("installWallpaper() rewrote a wallpaper that already matched: %v", err)
	}
}
