package agent

import (
	"slices"
	"testing"
)

func TestCargoWorkspacesAreTheLockfilesAndTopManifests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		files []string
		want  []string
	}{{
		// A workspace at the top, and a crate with a lockfile of its own.
		files: []string{"Cargo.toml", "Cargo.lock", "crates/a/Cargo.toml", "tools/gen/Cargo.toml", "tools/gen/Cargo.lock", "docs/Cargo.lock", "web/package.json"},
		want:  []string{".", "tools/gen"},
	}, {
		// A library with no lockfile, and a fuzz crate inside it.
		files: []string{"lib/x/Cargo.toml", "lib/x/fuzz/Cargo.toml", "b/Cargo.toml"},
		want:  []string{"b", "lib/x"},
	}, {
		files: []string{"package.json"},
	}} {
		if got := cargoWorkspaces(tc.files); !slices.Equal(got, tc.want) {
			t.Errorf("cargoWorkspaces(%q) = %q, want %q", tc.files, got, tc.want)
		}
	}
}
