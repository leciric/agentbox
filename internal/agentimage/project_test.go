package agentimage

import (
	"strings"
	"testing"
)

// A project's Dockerfile is written once, on the agent image it extends, and
// the cluster it runs in decides where that image is: only the first FROM's
// image changes.
func TestRewriteProjectDockerfileReplacesTheFirstFrom(t *testing.T) {
	body := "# a project's image\n" +
		"FROM --platform=linux/amd64 debian:trixie-slim AS base\n" +
		"RUN apt-get update && apt-get install -y ripgrep\n" +
		"\n" +
		"FROM base AS final\n" +
		"COPY . /src\n"
	got, err := rewriteProjectDockerfile([]byte(body), "hub.example/agentbox-agent:dev")
	if err != nil {
		t.Fatalf("rewriteProjectDockerfile() = %v", err)
	}
	want := "# a project's image\n" +
		"FROM --platform=linux/amd64 hub.example/agentbox-agent:dev AS base\n" +
		"RUN apt-get update && apt-get install -y ripgrep\n" +
		"\n" +
		"FROM base AS final\n" +
		"COPY . /src\n"
	if string(got) != want {
		t.Errorf("rewriteProjectDockerfile() =\n%s\nwant\n%s", got, want)
	}
}

func TestRewriteProjectDockerfileKeepsTheLastLine(t *testing.T) {
	got, err := rewriteProjectDockerfile([]byte("FROM agentbox-agent:dev"), "hub.example/agentbox-agent:dev")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FROM hub.example/agentbox-agent:dev" {
		t.Errorf("rewriteProjectDockerfile() = %q", got)
	}
}

func TestRewriteProjectDockerfileRefusesWhatHasNoFrom(t *testing.T) {
	_, err := rewriteProjectDockerfile([]byte("# only a comment\nRUN true\n"), "hub.example/agentbox-agent:dev")
	if err == nil || !strings.Contains(err.Error(), "no FROM") {
		t.Errorf("a Dockerfile with no FROM = %v, want it refused", err)
	}
}

// TestProjectBuildArgsNamesTheDockerfileTheImageAndTheContext checks the
// command without Docker: the rewritten file is passed by path, the image by
// tag, and the project's checkout is the context.
func TestProjectBuildArgsNamesTheDockerfileTheImageAndTheContext(t *testing.T) {
	opts := ProjectOptions{Root: "/projects/pawly", Image: "hub.example/pawly-agent:1"}
	got := strings.Join(ProjectBuildArgs(opts, "/projects/pawly/.agentbox-build-1.Dockerfile"), " ")
	for _, want := range []string{
		"build", "--platform " + imagePlatform,
		"--file /projects/pawly/.agentbox-build-1.Dockerfile",
		"--tag hub.example/pawly-agent:1", "/projects/pawly",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ProjectBuildArgs() = %q, want it to hold %q", got, want)
		}
	}
}
