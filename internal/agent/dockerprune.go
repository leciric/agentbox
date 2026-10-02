package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/state"
)

// DockerPruneTimeout is how long the daemon gives PruneDocker as a whole, so
// a Docker that hangs never keeps an agent from stopping for long.
const DockerPruneTimeout = 2 * time.Minute

// dockerCheck says whether the agent has a Docker to prune: "absent" when it
// isn't installed, "stopped" when its daemon isn't running, "running"
// otherwise. It asks systemd rather than docker itself, which would start a
// socket-activated daemon only to prune it.
const dockerCheck = `if ! command -v docker >/dev/null 2>&1; then echo absent
elif systemctl is-active --quiet docker.service; then echo running
else echo stopped; fi`

// DockerPrune is what PruneDocker freed, in bytes, or why it freed nothing.
type DockerPrune struct {
	// Skipped is why nothing was pruned: Docker isn't installed, isn't
	// running, or the machine isn't running. Empty when it was.
	Skipped    string
	BuildCache int64
	Images     int64
}

// Freed is everything PruneDocker freed.
func (p DockerPrune) Freed() int64 { return p.BuildCache + p.Images }

func (p DockerPrune) String() string {
	if p.Skipped != "" {
		return "nothing pruned: " + p.Skipped
	}
	return fmt.Sprintf("freed %s (%s of images, %s of build cache)", HumanBytes(p.Freed()), HumanBytes(p.Images), HumanBytes(p.BuildCache))
}

// PruneDocker frees the Docker space an agent can do without: its whole build
// cache, and every image no container uses, running or not. It never touches
// volumes, which hold the project's databases, nor containers. It only runs in
// a running machine whose Docker is running — a paused one can't run anything
// — and the caller bounds it with ctx (DockerPruneTimeout). Build cache goes
// first, so the images it held are free to go after it.
func (m *Manager) PruneDocker(ctx context.Context, a state.Agent) (DockerPrune, error) {
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return DockerPrune{}, err
	}
	if inst.Status != "Running" {
		return DockerPrune{Skipped: "the machine is " + strings.ToLower(inst.Status)}, nil
	}
	out, err := m.Incus.Exec(ctx, a.Instance, "bash", "-c", dockerCheck)
	if err != nil {
		return DockerPrune{}, fmt.Errorf("checking for Docker: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "running":
	case "absent":
		return DockerPrune{Skipped: "Docker isn't installed"}, nil
	default:
		return DockerPrune{Skipped: "Docker isn't running"}, nil
	}
	var p DockerPrune
	out, err = m.Incus.Exec(ctx, a.Instance, "docker", "builder", "prune", "--all", "--force")
	if err != nil {
		return p, fmt.Errorf("pruning Docker's build cache: %w", err)
	}
	p.BuildCache = dockerReclaimed(out)
	out, err = m.Incus.Exec(ctx, a.Instance, "docker", "image", "prune", "--all", "--force")
	if err != nil {
		return p, fmt.Errorf("pruning Docker's images: %w", err)
	}
	p.Images = dockerReclaimed(out)
	return p, nil
}

// dockerReclaimed reads what a docker prune says it freed, on its last line:
// "Total reclaimed space: 17.2GB" for images, "Total:\t7.05GB" for the build
// cache. Anything it can't read is 0.
func dockerReclaimed(out string) int64 {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	i := strings.LastIndex(last, ":")
	if i < 0 || !strings.HasPrefix(strings.TrimSpace(last), "Total") {
		return 0
	}
	n, err := ParseBytes(last[i+1:])
	if err != nil {
		return 0
	}
	return n
}
