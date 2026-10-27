package agentimage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ProjectDockerfile is where a project describes its own image, from the
// project's root. It is a project's base on a cluster, where a machine is a
// container image rather than an Incus instance to copy: packages its agents
// need go here, so every agent of the project gets them and the image is
// reproducible across clusters.
const ProjectDockerfile = ".agentbox/agent.Dockerfile"

// ProjectOptions are BuildProject's.
type ProjectOptions struct {
	// Root is the project's checkout, which is the build context.
	Root string
	// Dockerfile is the project's Dockerfile, relative to Root; empty is
	// ProjectDockerfile.
	Dockerfile string
	// Base is the agent image the project's image builds on: the image its
	// cluster runs, registry prefix and tag all.
	Base string
	// Image is the tag to build, like hub.example/pawly-agent:20261027-100000.
	Image string
}

// BuildProject builds a project's own image with Docker, or with Podman when
// Docker is missing or its daemon does not answer. The project's Dockerfile is
// built from its checkout, and its first FROM is replaced by Base: a project
// writes the image once, from the agent image it is meant to extend, and the
// cluster it runs in decides where that image is.
func BuildProject(ctx context.Context, opts ProjectOptions) error {
	if opts.Root == "" {
		return errors.New("no project root: it is the checkout the image is built from")
	}
	if opts.Base == "" {
		return errors.New("no base image: a project image builds on the agent image its cluster runs")
	}
	dockerfile := opts.Dockerfile
	if dockerfile == "" {
		dockerfile = ProjectDockerfile
	}
	if strings.HasPrefix(dockerfile, "/") || strings.Contains(dockerfile, "..") {
		return fmt.Errorf("the project's Dockerfile is a path inside its checkout, not %q", dockerfile)
	}
	source := filepath.Join(opts.Root, dockerfile)
	body, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading %s: %w", source, err)
	}
	rewritten, err := rewriteProjectDockerfile(body, opts.Base)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	// The rewritten copy is written inside the build context: a Dockerfile can
	// COPY paths relative to it, and the project's own file is never changed.
	file, err := os.CreateTemp(opts.Root, ".agentbox-build-*.Dockerfile")
	if err != nil {
		return fmt.Errorf("writing the project's Dockerfile for the build: %w", err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := file.Write(rewritten); err != nil {
		_ = file.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	runtime, err := Runtime(ctx)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, runtime, ProjectBuildArgs(opts, name)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build: %w", runtime, err)
	}
	return nil
}

// ProjectBuildArgs is the command BuildProject runs, the runtime's own name
// first and the rewritten Dockerfile by path. It reads only its arguments, so a
// test can check it without Docker.
func ProjectBuildArgs(opts ProjectOptions, dockerfile string) []string {
	return []string{"build", "--platform", imagePlatform, "--file", dockerfile, "--tag", opts.Image, opts.Root}
}

// fromLine is the first FROM of a Dockerfile, with the flags before the image
// and whatever follows it, such as "AS build", kept apart.
var fromLine = regexp.MustCompile(`(?i)^([ \t]*from[ \t]+)((?:--[a-z0-9_.-]+=[^ \t]+[ \t]+)*)([^ \t\r\n]+)(.*)$`)

// rewriteProjectDockerfile replaces the image of the Dockerfile's first FROM by
// base, and leaves every other line, and the first line's platform flag and
// stage name, exactly as it was. A Dockerfile with no FROM is refused: there is
// nothing to build on.
func rewriteProjectDockerfile(body []byte, base string) ([]byte, error) {
	text := string(body)
	lines := strings.SplitAfter(text, "\n")
	done := false
	for i, line := range lines {
		if done {
			break
		}
		// SplitAfter keeps the newline; the regular expression sees the line
		// without it and the rest goes back exactly as it came.
		trimmed := strings.TrimSuffix(line, "\n")
		cr := ""
		if strings.HasSuffix(trimmed, "\r") {
			trimmed, cr = strings.TrimSuffix(trimmed, "\r"), "\r"
		}
		match := fromLine.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		lines[i] = match[1] + match[2] + base + match[4] + cr
		if strings.HasSuffix(line, "\n") {
			lines[i] += "\n"
		}
		done = true
	}
	if !done {
		return nil, errors.New("it has no FROM line to build on: start it with FROM agentbox-agent, which this replaces with the cluster's image")
	}
	return []byte(strings.Join(lines, "")), nil
}
