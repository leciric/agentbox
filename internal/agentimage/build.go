// Package agentimage builds and pushes the container image an agent runs as
// when its machine is a pod in a Kubernetes cluster, instead of an Incus
// instance.
//
// The image is internal/agentimage/Dockerfile built from the repository root,
// which is also the build context: the Dockerfile copies bin/agentbox out of
// it, and Build puts the binary there first. Docker is used when it answers,
// and Podman when it does not.
package agentimage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strings"
)

// Dockerfile is where the Dockerfile is, from the repository root.
const Dockerfile = "internal/agentimage/Dockerfile"

// BinaryPath is where the binary the image carries is, from the repository
// root. It is inside bin/, which is gitignored, so Build leaves nothing that
// could be committed.
const BinaryPath = "bin/agentbox"

// DefaultImage is the tag an image is built as when Options.Image is empty.
const DefaultImage = "agentbox-agent:dev"

// imagePlatform is what the Dockerfile pins the image to, and the only platform
// a binary it carries can run on.
const imagePlatform = "linux/amd64"

// Options say what to build.
type Options struct {
	// Context is the repository root, which is the build context.
	Context string
	// Image is the tag, like agentbox-agent:dev.
	Image string
	// Stage is "", "dev" or "agent": the stage to stop at. Empty builds the
	// whole file, which ends at agent.
	Stage string
	// CLIPath is the agentbox binary to put in the image, for a caller that
	// has one elsewhere. Empty copies the running executable.
	CLIPath string
}

// Build builds the image with Docker, or with Podman when Docker is missing or
// its daemon does not answer.
func Build(ctx context.Context, opts Options) error {
	if opts.Context == "" {
		return errors.New("no build context: it is the repository root, which has internal/agentimage")
	}
	if _, err := os.Stat(filepath.Join(opts.Context, Dockerfile)); err != nil {
		return fmt.Errorf("%s isn't the repository root: %w", opts.Context, err)
	}
	if !slices.Contains([]string{"", "dev", "agent"}, opts.Stage) {
		return fmt.Errorf("no stage %q: it is dev, agent, or nothing for the whole image", opts.Stage)
	}
	// Checked before the runtime is probed, because a wrong platform is a
	// minute into a build otherwise, and dies in every pod afterwards.
	if err := checkPlatform(goruntime.GOOS, goruntime.GOARCH); err != nil {
		return err
	}
	runtime, err := Runtime(ctx)
	if err != nil {
		return err
	}
	if err := copyCLI(opts.Context, opts.CLIPath); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, runtime, BuildArgs(opts)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build: %w", runtime, err)
	}
	return nil
}

// checkPlatform refuses a build whose binary the image cannot run. The
// Dockerfile pins linux/amd64, and a binary from another platform dies with
// "exec format error" in every pod, with nothing wrong visible in the build.
func checkPlatform(goos, goarch string) error {
	if goos+"/"+goarch == imagePlatform {
		return nil
	}
	return fmt.Errorf("the agent image is %s and this is %s/%s: run scripts/build-agent-image.sh, which builds the binary for the image's platform", imagePlatform, goos, goarch)
}

// BuildArgs is the command Build runs, the runtime's own name first. It reads
// only opts, so a test can check it without Docker.
func BuildArgs(opts Options) []string {
	image := opts.Image
	if image == "" {
		image = DefaultImage
	}
	context := opts.Context
	if context == "" {
		context = "."
	}
	args := []string{"build", "--file", filepath.Join(context, Dockerfile), "--tag", image}
	if opts.Stage != "" {
		args = append(args, "--target", opts.Stage)
	}
	return append(args, context)
}

// Push tags nothing: ref is already the name to push, tag or digest and all.
// It pushes with the runtime's own login, and returns the digest the registry
// reported as "@sha256:...", which is what appends to ref.
func Push(ctx context.Context, ref string) (digest string, err error) {
	if strings.TrimSpace(ref) == "" {
		return "", errors.New("no image reference to push")
	}
	runtime, err := Runtime(ctx)
	if err != nil {
		return "", err
	}
	push := exec.CommandContext(ctx, runtime, "push", ref)
	var pushed strings.Builder
	push.Stdout = io.MultiWriter(os.Stdout, &pushed)
	push.Stderr = io.MultiWriter(os.Stderr, &pushed)
	if err := push.Run(); err != nil {
		return "", fmt.Errorf("%s push %s: %w", runtime, ref, err)
	}
	// The push's own output is the registry's answer to what it stored, and it
	// is the only one that always names this push: the local image store may
	// keep no repo digest at all, or one for another repository.
	if digest := pushDigest(pushed.String()); digest != "" {
		return digest, nil
	}
	inspect := exec.CommandContext(ctx, runtime, "image", "inspect", "--format", "{{index .RepoDigests 0}}", ref)
	var stderr strings.Builder
	inspect.Stderr = &stderr
	out, err := inspect.Output()
	if err != nil {
		return "", fmt.Errorf("%s reported no digest for %s: %w: %s", runtime, ref, err, strings.TrimSpace(stderr.String()))
	}
	return digestFrom(string(out))
}

// pushDigestPattern is a runtime saying what the registry stored: docker ends a
// push with a line like "digest: sha256:... size: 1234". A sha512 digest is
// left out on purpose: the value is appended to the reference as sha256.
var pushDigestPattern = regexp.MustCompile(`(?i)\bdigest: (sha256:[0-9a-f]{64})(?:[^0-9a-f]|$)`)

// pushDigest is the digest a push printed, as Push returns it, and "" when it
// printed none, which is what a runtime that says nothing about the manifest
// does.
func pushDigest(out string) string {
	match := pushDigestPattern.FindStringSubmatch(out)
	if match == nil {
		return ""
	}
	return "@" + match[1]
}

// digestFrom is the registry's digest in what "image inspect" printed: the
// "@sha256:..." part of a repo digest, or an error when there is none.
func digestFrom(out string) (string, error) {
	repo := strings.TrimSpace(out)
	at := strings.LastIndex(repo, "@")
	if at < 0 {
		return "", fmt.Errorf("the registry reported no digest for %q", repo)
	}
	digest := repo[at:]
	const prefix = "@sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return "", fmt.Errorf("the registry reported %q, which is not a digest", repo)
	}
	return digest, nil
}

// Runtime is the container runtime to use: "docker", or "podman" when Docker
// is not installed or its daemon does not answer.
func Runtime(ctx context.Context) (string, error) {
	var tried []string
	for _, name := range []string{"docker", "podman"} {
		path, err := exec.LookPath(name)
		if err != nil {
			tried = append(tried, name+" isn't installed")
			continue
		}
		if err := exec.CommandContext(ctx, path, "version").Run(); err != nil {
			tried = append(tried, name+" doesn't answer")
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("no container runtime: %s", strings.Join(tried, ", "))
}

// copyCLI puts the agentbox binary where the Dockerfile copies it from, in the
// build context. An empty cli is the running executable, which is what
// scripts/build-agent-image.sh leaves there itself.
func copyCLI(context, cli string) error {
	src := cli
	if src == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("finding the running agentbox binary: %w", err)
		}
		src = exe
	}
	src, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(context, BinaryPath)
	// The CLI running from bin/agentbox is the file it would copy, and
	// truncating a running executable fails with "text file busy".
	if sameFile(src, dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("reading the agentbox binary for the image: %w", err)
	}
	defer func() { _ = in.Close() }()
	if info, err := in.Stat(); err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a file", src)
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("making %s: %w", dir, err)
	}
	// Written beside it and renamed into place, so an interrupted copy never
	// leaves half a binary in the build context for a later build to pick up.
	tmp, err := os.CreateTemp(dir, ".agentbox-*")
	if err != nil {
		return fmt.Errorf("making room for %s: %w", dst, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copying %s: %w", src, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("making %s executable: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("putting %s in the build context: %w", dst, err)
	}
	return nil
}

// sameFile says whether a and b are one file, following links.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
