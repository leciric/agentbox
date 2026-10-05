package machines

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/desktop"
)

// Docker is the backend that runs machines as containers on this machine,
// with Docker or Podman.
type Docker struct {
	// Bin is the runtime's command: docker, podman, or a path to either.
	Bin string
	// Engine is which one it is: "docker" or "podman".
	Engine string
	// Rootless is a runtime whose root is the user: commands run as root in
	// the machine, which writes the worktree as the user. Otherwise they run
	// as the user's own uid.
	Rootless bool
	UID, GID int
}

// Labels on every machine's container.
const (
	labelMachine  = "agentbox.machine"
	labelWorktree = "agentbox.machine.worktree"
	labelConfig   = "agentbox.machine.config"
)

// machineHome is the machine user's home, made by the image.
const machineHome = "/home/machine"

// readyTimeout is how long a machine's entrypoint may take, which includes
// starting Docker inside for a project that wants it.
const readyTimeout = 60 * time.Second

// DetectDocker finds the container runtime: AGENTBOX_MACHINES_RUNTIME when it
// is set, Podman when it is installed, Docker otherwise.
func DetectDocker(ctx context.Context) (*Docker, error) {
	bin := os.Getenv("AGENTBOX_MACHINES_RUNTIME")
	if bin == "" {
		for _, name := range []string{"podman", "docker"} {
			if _, err := exec.LookPath(name); err == nil {
				bin = name
				break
			}
		}
	}
	if bin == "" {
		return nil, errors.New("machines need Docker or Podman, and neither is installed")
	}
	d := &Docker{Bin: bin, Engine: "docker", UID: os.Getuid(), GID: os.Getgid()}
	if strings.Contains(filepath.Base(bin), "podman") {
		d.Engine = "podman"
	}
	// Docker Desktop's VM maps a Mac's files to whoever the container runs
	// as, so the user's own uid is right there too.
	if runtime.GOOS == "linux" {
		switch d.Engine {
		case "podman":
			out, err := d.run(ctx, nil, "info", "--format", "{{.Host.Security.Rootless}}")
			d.Rootless = err == nil && strings.TrimSpace(out) == "true"
		default:
			out, err := d.run(ctx, nil, "info", "--format", "{{json .SecurityOptions}}")
			d.Rootless = err == nil && strings.Contains(out, "rootless")
		}
	}
	return d, nil
}

// Name is the runtime's.
func (d *Docker) Name() string { return d.Engine }

// run runs the runtime and returns its output; a failure carries its stderr.
// env is added to the runtime's environment, which is how secrets reach the
// machine without appearing on its command line.
func (d *Docker) run(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.Bin, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return out.String(), fmt.Errorf("%s %s: %s", d.Engine, args[0], msg)
		}
		return out.String(), fmt.Errorf("%s %s: %w", d.Engine, args[0], err)
	}
	return out.String(), nil
}

// user is who commands run as in the machine.
func (d *Docker) user() string {
	if d.Rootless {
		return "0:0"
	}
	return fmt.Sprintf("%d:%d", d.UID, d.GID)
}

// Command runs name in the machine, as its user, in the worktree.
func (d *Docker) Command(ctx context.Context, worktree string, name string, args ...string) *exec.Cmd {
	a := []string{"exec", "-i", "-u", d.user(), "-e", "HOME=" + machineHome, "-e", "DISPLAY=" + desktop.Display,
		"-w", worktree, containerName(worktree), name}
	return exec.CommandContext(ctx, d.Bin, append(a, args...)...)
}

// EnsureImage builds the machine image unless it is there.
func (d *Docker) EnsureImage(ctx context.Context, tag string, progress func(string)) error {
	if _, err := d.run(ctx, nil, "image", "inspect", tag); err == nil {
		return nil
	}
	return d.Build(ctx, tag, progress)
}

// Build builds the machine image as tag, telling progress each line of the
// build's output.
func (d *Docker) Build(ctx context.Context, tag string, progress func(string)) error {
	dir, err := os.MkdirTemp("", "agentbox-machine-image-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := WriteContext(dir); err != nil {
		return err
	}
	args := []string{"build", "-t", tag}
	for k, v := range BuildArgs() {
		args = append(args, "--build-arg", k+"="+v)
	}
	if d.Engine == "docker" {
		args = append(args, "--progress", "plain")
	}
	cmd := exec.CommandContext(ctx, d.Bin, append(args, dir)...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var tail []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		s := bufio.NewScanner(pr)
		for s.Scan() {
			line := s.Text()
			if tail = append(tail, line); len(tail) > 15 {
				tail = tail[1:]
			}
			if progress != nil {
				progress(line)
			}
		}
	}()
	err = cmd.Run()
	_ = pw.Close()
	<-done
	if err != nil {
		return fmt.Errorf("building the machine image: %w\n%s", err, strings.Join(tail, "\n"))
	}
	return nil
}

// runArgs are the arguments that make the worktree's machine, and the
// environment the runtime gets them with: a secret's value is passed in the
// runtime's own environment, `-e KEY` naming it, so it isn't on a command
// line anything can read.
func (d *Docker) runArgs(worktree string, c Config, img string) (args, env []string) {
	name := containerName(worktree)
	uid, gid := strconv.Itoa(d.UID), strconv.Itoa(d.GID)
	if d.Rootless {
		uid, gid = "0", "0"
	}
	args = []string{"run", "-d", "--init", "--name", name,
		"--hostname", strings.TrimPrefix(name, "agentbox-machine-"),
		"--label", labelMachine + "=1",
		"--label", labelWorktree + "=" + worktree,
		"--label", labelConfig + "=" + c.Hash(img),
		"--shm-size", "1g",
		// A fresh /run each start: a stopped machine's would say it's ready,
		// and hold a dead dockerd's socket and pid file.
		"--tmpfs", "/run:exec,mode=755",
		"-v", worktree + ":" + worktree,
		"-e", "MACHINE_UID=" + uid, "-e", "MACHINE_GID=" + gid,
	}
	if common := gitCommonDir(worktree); common != "" {
		args = append(args, "-v", common+":"+common)
	}
	if c.Memory != "" {
		args = append(args, "--memory", c.Memory)
	}
	if d.Engine == "podman" {
		// SELinux would otherwise want the worktree relabelled.
		args = append(args, "--security-opt", "label=disable")
	}
	for _, p := range c.Ports {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1::%d", p))
	}
	keys := make([]string, 0, len(c.env))
	for k := range c.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k)
		env = append(env, k+"="+c.env[k])
	}
	if c.DockerInside {
		args = append(args, "--privileged",
			"--ulimit", "nofile=1048576:1048576",
			"-v", name+"-docker:/var/lib/docker",
			"-e", "MACHINE_DOCKERD=1")
	}
	return append(args, img), env
}

// Start makes the worktree's machine, or starts the one there is, then its
// desktop.
func (d *Docker) Start(ctx context.Context, worktree string, c Config, progress func(string)) (Status, error) {
	img := c.Image
	if img == "" {
		img = ImageTag()
		if err := d.EnsureImage(ctx, img, progress); err != nil {
			return Status{}, err
		}
	}
	st, err := d.Status(ctx, worktree)
	if err != nil {
		return st, err
	}
	name := containerName(worktree)
	if st.Exists && st.config != c.Hash(img) {
		if _, err := d.run(ctx, nil, "rm", "-f", name); err != nil {
			return st, err
		}
		st.Exists = false
	}
	switch {
	case !st.Exists:
		args, env := d.runArgs(worktree, c, img)
		if _, err := d.run(ctx, env, args...); err != nil {
			return st, err
		}
	case !st.Running:
		if _, err := d.run(ctx, nil, "start", name); err != nil {
			return st, err
		}
	}
	if err := d.waitReady(ctx, worktree); err != nil {
		return st, err
	}
	if out, err := d.Command(ctx, worktree, "agentbox-browser", "start").CombinedOutput(); err != nil {
		return st, fmt.Errorf("starting the desktop: %s", strings.TrimSpace(string(out)))
	}
	return d.Status(ctx, worktree)
}

func (d *Docker) waitReady(ctx context.Context, worktree string) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		if _, err := d.run(ctx, nil, "exec", containerName(worktree), "test", "-e", "/run/machine-ready"); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			logs, _ := d.run(ctx, nil, "logs", "--tail", "20", containerName(worktree))
			return fmt.Errorf("the machine didn't start in %s:\n%s", readyTimeout, logs)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Stop stops the machine. Its container stays, with the machine's home and
// its Docker's images, so the next start is quick.
func (d *Docker) Stop(ctx context.Context, worktree string) error {
	st, err := d.Status(ctx, worktree)
	if err != nil || !st.Running {
		return err
	}
	_, err = d.run(ctx, nil, "stop", "-t", "5", st.Name)
	return err
}

// Remove deletes the machine and the volume its Docker inside kept.
func (d *Docker) Remove(ctx context.Context, worktree string) error {
	name := containerName(worktree)
	if _, err := d.run(ctx, nil, "rm", "-f", name); err != nil && !notFound(err) {
		return err
	}
	if _, err := d.run(ctx, nil, "volume", "rm", "-f", name+"-docker"); err != nil && !notFound(err) {
		return err
	}
	return nil
}

func notFound(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such") || strings.Contains(msg, "not found") || strings.Contains(msg, "no container")
}

// inspected is what `docker inspect` says of a container that matters here;
// Podman's says the same.
type inspected struct {
	Name  string
	State struct {
		Running   bool
		StartedAt time.Time
	}
	// Config.Labels.
	Config struct {
		Labels map[string]string
	}
	HostConfig struct {
		Privileged bool
		Memory     int64
	}
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
	}
}

func (i inspected) status(engine string) Status {
	st := Status{Backend: engine, Name: strings.TrimPrefix(i.Name, "/"), Worktree: i.Config.Labels[labelWorktree],
		Exists: true, Running: i.State.Running, DockerInside: i.HostConfig.Privileged, config: i.Config.Labels[labelConfig]}
	if i.State.Running && i.State.StartedAt.Year() > 1 {
		st.Started = i.State.StartedAt
	}
	if i.HostConfig.Memory > 0 {
		st.Memory = fmt.Sprintf("%dm", i.HostConfig.Memory>>20)
	}
	for spec, bindings := range i.NetworkSettings.Ports {
		port, err := strconv.Atoi(strings.TrimSuffix(spec, "/tcp"))
		if err != nil || len(bindings) == 0 || bindings[0].HostPort == "" {
			continue
		}
		host := bindings[0].HostIP
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		if st.Ports == nil {
			st.Ports = map[int]string{}
		}
		st.Ports[port] = host + ":" + bindings[0].HostPort
	}
	return st
}

func (d *Docker) inspect(ctx context.Context, names ...string) ([]inspected, error) {
	out, err := d.run(ctx, nil, append([]string{"inspect", "--type", "container"}, names...)...)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	var list []inspected
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("%s inspect: %w", d.Engine, err)
	}
	return list, nil
}

// Status is the worktree's machine.
func (d *Docker) Status(ctx context.Context, worktree string) (Status, error) {
	name := containerName(worktree)
	list, err := d.inspect(ctx, name)
	if err != nil {
		if notFound(err) {
			return Status{Backend: d.Engine, Name: name, Worktree: worktree}, nil
		}
		return Status{}, err
	}
	if len(list) == 0 {
		return Status{Backend: d.Engine, Name: name, Worktree: worktree}, nil
	}
	return list[0].status(d.Engine), nil
}

// List is every machine, running or not.
func (d *Docker) List(ctx context.Context) ([]Status, error) {
	out, err := d.run(ctx, nil, "ps", "-a", "--filter", "label="+labelMachine, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return nil, nil
	}
	list, err := d.inspect(ctx, names...)
	if err != nil {
		return nil, err
	}
	var all []Status
	for _, i := range list {
		all = append(all, i.status(d.Engine))
	}
	sort.Slice(all, func(a, b int) bool { return all[a].Worktree < all[b].Worktree })
	return all, nil
}

// MemoryUsage is how much memory the named running machines use, by name, as
// the runtime writes it: 312.4MiB.
func (d *Docker) MemoryUsage(ctx context.Context, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return map[string]string{}, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}"}, names...)
	out, err := d.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	return parseMemUsage(out), nil
}

func parseMemUsage(out string) map[string]string {
	usage := map[string]string{}
	for line := range strings.Lines(out) {
		name, mem, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		used, _, _ := strings.Cut(mem, "/")
		usage[strings.TrimPrefix(name, "/")] = strings.TrimSpace(used)
	}
	return usage
}
