package agentimage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests never run Docker: what Build would run is BuildArgs, and what Push
// reads is digestFrom. What the runtime does with them is the runtime's own.

func TestBuildArgsNamesTheDockerfileTheTagAndTheContext(t *testing.T) {
	root := filepath.Join("src", "agentbox")
	cases := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "the whole file it is asked for",
			opts: Options{Context: root, Image: "agentbox-agent:dev"},
			want: []string{"build", "--file", filepath.Join(root, Dockerfile), "--tag", "agentbox-agent:dev", root},
		},
		{
			name: "the dev stage only",
			opts: Options{Context: root, Image: "agentbox-agent:dev", Stage: "dev"},
			want: []string{"build", "--file", filepath.Join(root, Dockerfile), "--tag", "agentbox-agent:dev", "--target", "dev", root},
		},
		{
			name: "the agent stage by name",
			opts: Options{Context: root, Image: "hub.example.com/you/agentbox-agent:0.3.0", Stage: "agent"},
			want: []string{"build", "--file", filepath.Join(root, Dockerfile), "--tag", "hub.example.com/you/agentbox-agent:0.3.0", "--target", "agent", root},
		},
		{
			name: "no tag is the default one",
			opts: Options{Context: root},
			want: []string{"build", "--file", filepath.Join(root, Dockerfile), "--tag", DefaultImage, root},
		},
		{
			name: "no context is this directory",
			opts: Options{Image: "agentbox-agent:dev"},
			want: []string{"build", "--file", Dockerfile, "--tag", "agentbox-agent:dev", "."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildArgs(c.opts)
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Errorf("BuildArgs = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildRefusesWhatItCannotBuild(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "agentimage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, Dockerfile), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"no context", Options{}, "no build context"},
		{"a directory that is not the repository", Options{Context: t.TempDir()}, "isn't the repository root"},
		{"a stage the Dockerfile has no name for", Options{Context: root, Stage: "base"}, `no stage "base"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Build(context.Background(), c.opts)
			if err == nil {
				t.Fatal("Build accepted what it should refuse")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Build said %q, want something saying %q", err, c.want)
			}
		})
	}
}

func TestCopyCLIPutsTheBinaryInTheBuildContext(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "agentbox")
	if err := os.WriteFile(cli, []byte("the agentbox binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(root, cli); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, BinaryPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the agentbox binary" {
		t.Errorf("the build context has %q, not the binary it was given", got)
	}
	info, err := os.Stat(filepath.Join(root, BinaryPath))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("the binary is %v, and docker cannot run one that is not executable", info.Mode().Perm())
	}
}

func TestCopyCLILeavesTheBinaryItWouldCopyAlone(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, BinaryPath)
	if err := os.WriteFile(cli, []byte("the binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory nothing may be written to: copying the file onto itself
	// would need a temporary beside it, and a running executable cannot be
	// opened for writing at all.
	if err := os.Chmod(bin, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bin, 0o755) })
	before, err := os.Stat(cli)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(root, cli); err != nil {
		t.Fatalf("copying the binary onto itself: %v", err)
	}
	after, err := os.Stat(cli)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("copyCLI replaced the binary with a copy of itself")
	}
	got, err := os.ReadFile(cli)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the binary" {
		t.Errorf("the binary became %q", got)
	}
}

func TestBuildRefusesABinaryTheImageCannotRun(t *testing.T) {
	// The Dockerfile pins linux/amd64, so the binary copied into it has to be
	// one that platform runs: another one starts in the builder and dies in
	// every pod with "exec format error".
	for _, c := range []struct {
		goos, goarch string
		ok           bool
	}{
		{"linux", "amd64", true},
		{"darwin", "arm64", false},
		{"linux", "arm64", false},
		{"darwin", "amd64", false},
		{"windows", "amd64", false},
	} {
		t.Run(c.goos+"/"+c.goarch, func(t *testing.T) {
			err := checkPlatform(c.goos, c.goarch)
			if c.ok {
				if err != nil {
					t.Fatalf("checkPlatform(%s, %s) = %v, want it accepted", c.goos, c.goarch, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkPlatform(%s, %s) accepted a platform the image cannot run", c.goos, c.goarch)
			}
			if !strings.Contains(err.Error(), "scripts/build-agent-image.sh") {
				t.Errorf("checkPlatform(%s, %s) = %v, want it to name the script that builds for the image", c.goos, c.goarch, err)
			}
			if !strings.Contains(err.Error(), c.goos+"/"+c.goarch) {
				t.Errorf("checkPlatform(%s, %s) = %v, want it to name the platform it found", c.goos, c.goarch, err)
			}
		})
	}
}

func TestPushDigestIsWhatTheRegistrySaid(t *testing.T) {
	sha := func(hex string) string { return "@sha256:" + strings.Repeat(hex, 64) }
	cases := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "the line docker ends a push with",
			out: "The push refers to repository [hub.example.com/h4ad/agentbox-agent]\n" +
				"6f0e2c: Pushed\ndev: digest: sha256:" + strings.Repeat("a", 64) + " size: 1234\n",
			want: sha("a"),
		},
		{
			name: "a push that prints no digest, like the containerd image store",
			out: "The push refers to repository [hub.example.com/h4ad/agentbox-agent]\n" +
				"6f0e2c: Pushed\n",
			want: "",
		},
		{
			name: "another algorithm is not the digest a reference takes",
			out:  "dev: digest: sha512:" + strings.Repeat("b", 128) + " size: 1234\n",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pushDigest(c.out)
			if got != c.want {
				t.Errorf("pushDigest(%q) = %q, want %q", c.out, got, c.want)
			}
		})
	}
}

func TestPushReturnsTheDigestTheRegistryReported(t *testing.T) {
	sha := "@sha256:" + strings.Repeat("0", 64)
	cases := []struct {
		name string
		out  string
		want string
		err  string
	}{
		{
			name: "a repo digest",
			out:  "hub.example.com/you/agentbox-agent@sha256:" + strings.Repeat("0", 64) + "\n",
			want: sha,
		},
		{
			name: "nothing at all",
			out:  "\n",
			err:  "no digest",
		},
		{
			name: "a name with no digest",
			out:  "hub.example.com/you/agentbox-agent:dev\n",
			err:  "no digest",
		},
		{
			name: "something that is not a sum",
			out:  "hub.example.com/you/agentbox-agent@sha256:abc\n",
			err:  "not a digest",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := digestFrom(c.out)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("digestFrom(%q) = %q, %v, want an error saying %q", c.out, got, err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("digestFrom = %q, want %q", got, c.want)
			}
		})
	}
}
