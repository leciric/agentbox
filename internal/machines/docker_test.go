package machines

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeRuntime is a container runtime in a shell script: it logs each command
// line, answers inspect with $dir/container.json once `run` made it (or
// before, when a test wrote $dir/container.json itself), and runs what is
// exec'd in it right here, in the worktree, so the tools' scripts really run.
const fakeRuntime = `#!/bin/sh
dir=$(dirname "$0")
printf '%s\n' "$*" >>"$dir/log"
case "$1" in
info) echo '[]' ;;
image) exit 0 ;;
inspect)
  [ -e "$dir/container.json" ] || { echo "Error: No such container: x" >&2; exit 1; }
  cat "$dir/container.json" ;;
run)
  env >"$dir/run.env"
  cp "$dir/made.json" "$dir/container.json"
  echo id ;;
rm|stop|start|volume|logs) [ "$1" = rm ] && rm -f "$dir/container.json"; exit 0 ;;
exec)
  if [ "$2" = -i ]; then w=${10}; shift 11; cd "$w"; else shift 2; fi
  case "$1" in agentbox-browser|playwright-mcp) exit 0 ;; esac
  [ "$*" = "test -e /run/machine-ready" ] && exit 0
  exec "$@" ;;
ps) [ -e "$dir/container.json" ] && echo agentbox-machine-x ;;
esac
`

type fake struct {
	dir string
	d   *Docker
}

func newFake(t *testing.T) fake {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(fakeRuntime), 0o755); err != nil {
		t.Fatal(err)
	}
	return fake{dir, &Docker{Bin: bin, Engine: "docker", UID: 1000, GID: 1000}}
}

// made is what inspect says once `run` made the machine.
func (f fake) made(t *testing.T, worktree, config string, running bool) {
	t.Helper()
	c := []map[string]any{{
		"Name":       "/" + containerName(worktree),
		"State":      map[string]any{"Running": running},
		"Config":     map[string]any{"Labels": map[string]string{labelWorktree: worktree, labelConfig: config}},
		"HostConfig": map[string]any{"Memory": 4 << 30},
		"NetworkSettings": map[string]any{"Ports": map[string]any{
			"3000/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "49153"}},
			"5173/tcp": nil,
		}},
	}}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(f.dir, "made.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f fake) exists(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "made.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "container.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f fake) log(t *testing.T) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(f.dir, "log"))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func commandsLike(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func TestRunArgs(t *testing.T) {
	d := &Docker{Engine: "docker", UID: 1000, GID: 1001}
	wt := t.TempDir()
	c := Config{Memory: "2g", Ports: []int{3000}, env: map[string]string{"API_KEY": "s3cret"}}
	args, env := d.runArgs(wt, c, "img:1")
	line := strings.Join(args, " ")
	for _, want := range []string{"-v " + wt + ":" + wt, "--memory 2g", "-p 127.0.0.1::3000", "-e API_KEY ",
		"MACHINE_UID=1000", "MACHINE_GID=1001", "--init", "--tmpfs /run:exec,mode=755", labelConfig + "=" + c.Hash("img:1")} {
		if !strings.Contains(line, want) {
			t.Errorf("run args lack %q: %s", want, line)
		}
	}
	if strings.Contains(line, "s3cret") {
		t.Errorf("a secret's value is on the command line: %s", line)
	}
	if strings.Contains(line, "--privileged") || strings.Contains(line, "/var/lib/docker") {
		t.Errorf("an ordinary machine is privileged: %s", line)
	}
	if !slices.Equal(env, []string{"API_KEY=s3cret"}) {
		t.Errorf("env = %v", env)
	}
	if args[len(args)-1] != "img:1" {
		t.Errorf("the image isn't last: %v", args)
	}

	c.DockerInside = true
	d.Rootless, d.Engine = true, "podman"
	args, _ = d.runArgs(wt, c, "img:1")
	line = strings.Join(args, " ")
	for _, want := range []string{"--privileged", "--ulimit nofile=1048576:1048576", containerName(wt) + "-docker:/var/lib/docker",
		"MACHINE_DOCKERD=1", "MACHINE_UID=0", "MACHINE_GID=0", "--security-opt label=disable"} {
		if !strings.Contains(line, want) {
			t.Errorf("docker-inside args lack %q: %s", want, line)
		}
	}
}

func TestStartMakesTheMachine(t *testing.T) {
	f := newFake(t)
	wt := t.TempDir()
	c := Config{Memory: "4g", Ports: []int{3000}, env: map[string]string{"TOKEN_FOR_APP": "v4lue"}}
	f.made(t, wt, c.Hash(ImageTag()), true)
	st, err := f.d.Start(context.Background(), wt, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Ports[3000] != "127.0.0.1:49153" || st.Memory != "4096m" {
		t.Errorf("status = %+v", st)
	}
	if _, ok := st.Ports[5173]; ok {
		t.Errorf("an unpublished port is listed: %v", st.Ports)
	}
	log := f.log(t)
	if len(commandsLike(log, "run ")) != 1 {
		t.Errorf("run %d times:\n%s", len(commandsLike(log, "run ")), strings.Join(log, "\n"))
	}
	if len(commandsLike(log, "exec -i -u 1000:1000 -e HOME=/home/machine -e DISPLAY=:99 -w "+wt+" "+containerName(wt)+" agentbox-browser start")) != 1 {
		t.Errorf("the desktop wasn't started:\n%s", strings.Join(log, "\n"))
	}
	runEnv, _ := os.ReadFile(filepath.Join(f.dir, "run.env"))
	if !strings.Contains(string(runEnv), "TOKEN_FOR_APP=v4lue") {
		t.Error("the runtime didn't get the secret in its environment")
	}
	if strings.Contains(strings.Join(log, "\n"), "v4lue") {
		t.Error("the secret's value is on a command line")
	}

	// Started again, it is left as it is.
	if _, err := f.d.Start(context.Background(), wt, c, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(commandsLike(f.log(t), "run ")); n != 1 {
		t.Errorf("a running machine was made again: run %d times", n)
	}
}

func TestStartRemakesAMachineWithAnotherConfig(t *testing.T) {
	f := newFake(t)
	wt := t.TempDir()
	c := Config{Memory: "4g"}
	f.made(t, wt, "an older config", false)
	f.exists(t)
	f.made(t, wt, c.Hash(ImageTag()), true)
	if _, err := f.d.Start(context.Background(), wt, c, nil); err != nil {
		t.Fatal(err)
	}
	log := f.log(t)
	if len(commandsLike(log, "rm -f "+containerName(wt))) != 1 || len(commandsLike(log, "run ")) != 1 {
		t.Errorf("the machine wasn't made again:\n%s", strings.Join(log, "\n"))
	}
}

func TestStartStartsAStoppedMachine(t *testing.T) {
	f := newFake(t)
	wt := t.TempDir()
	c := Config{}
	f.made(t, wt, c.Hash(ImageTag()), false)
	f.exists(t)
	if _, err := f.d.Start(context.Background(), wt, c, nil); err != nil {
		t.Fatal(err)
	}
	log := f.log(t)
	if len(commandsLike(log, "start "+containerName(wt))) != 1 || len(commandsLike(log, "run ")) != 0 {
		t.Errorf("the stopped machine wasn't started:\n%s", strings.Join(log, "\n"))
	}
}

func TestStatusOfNoMachine(t *testing.T) {
	f := newFake(t)
	st, err := f.d.Status(context.Background(), "/w/x")
	if err != nil || st.Exists || st.Running || st.Name != containerName("/w/x") {
		t.Errorf("status = %+v, %v", st, err)
	}
	if err := f.d.Stop(context.Background(), "/w/x"); err != nil {
		t.Errorf("stopping no machine: %v", err)
	}
	if err := f.d.Remove(context.Background(), "/w/x"); err != nil {
		t.Errorf("removing no machine: %v", err)
	}
}

func TestList(t *testing.T) {
	f := newFake(t)
	wt := t.TempDir()
	f.made(t, wt, "c", true)
	f.exists(t)
	list, err := f.d.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Worktree != wt || !list[0].Running {
		t.Errorf("list = %+v, %v", list, err)
	}
}

func TestContainerName(t *testing.T) {
	a, b := containerName("/home/u/src/My App"), containerName("/home/u/other/My App")
	if a == b {
		t.Error("two worktrees with the same name share a machine")
	}
	if !strings.HasPrefix(a, "agentbox-machine-my-app-") {
		t.Errorf("name = %s", a)
	}
	if containerName("/home/u/src/My App/") != a {
		t.Error("a trailing slash makes another machine")
	}
}

func TestDetectDockerHonoursTheOverride(t *testing.T) {
	f := newFake(t)
	t.Setenv("AGENTBOX_MACHINES_RUNTIME", f.d.Bin)
	d, err := DetectDocker(context.Background())
	if err != nil || d.Bin != f.d.Bin || d.Engine != "docker" || d.Rootless {
		t.Errorf("detected %+v, %v", d, err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
}
