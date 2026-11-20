package agent

import (
	"os"
	"os/exec"
	"slices"
	"testing"

	"agentbox/internal/image"
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

// The env line points Cargo's intermediate files at the machine's own disk,
// one directory a workspace, unless the agent chose a place itself.
func TestCargoBuildDirEnv(t *testing.T) {
	m := &Manager{User: image.User{Name: "dev"}}
	line := m.cargoBuildDirEnv()
	for _, tc := range []struct{ set, want string }{
		{"", "/home/dev/.cache/agentbox/cargo-build/{workspace-path-hash}"},
		{"/mine", "/mine"},
	} {
		cmd := exec.Command("sh", "-c", line+`printf %s "$CARGO_BUILD_BUILD_DIR"`)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		if tc.set != "" {
			cmd.Env = append(cmd.Env, "CARGO_BUILD_BUILD_DIR="+tc.set)
		}
		out, err := cmd.Output()
		if err != nil || string(out) != tc.want {
			t.Errorf("with %q set: %q, %v; want %q", tc.set, out, err, tc.want)
		}
	}
}
