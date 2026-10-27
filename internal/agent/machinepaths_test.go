package agent_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/credentials"
	"agentbox/internal/image"
	mach "agentbox/internal/machine"
	"agentbox/internal/state"
)

// TestMachinePathsFollowTheRuntime is the seam's contract: an Incus agent
// keeps the daemon's user and its host worktree, and a pod gets the image's
// agent user and its volume's mount, whatever the host has.
func TestMachinePathsFollowTheRuntime(t *testing.T) {
	m := &agent.Manager{User: image.User{Name: "dev", UID: 1000, GID: 1000}}
	incus := state.Agent{Worktree: "/work/agent-01"}
	pod := state.Agent{Cluster: "prod", Workspace: "/workspace", Worktree: "/host/agent-01"}

	if got, want := m.MachineUser(incus), (image.User{Name: "dev", UID: 1000, GID: 1000}); got != want {
		t.Errorf("MachineUser(incus) = %+v, want the daemon's own %+v", got, want)
	}
	if got := m.MachineHome(incus); got != "/home/dev" {
		t.Errorf("MachineHome(incus) = %q, want /home/dev", got)
	}
	if got := m.Worktree(incus); got != "/work/agent-01" {
		t.Errorf("Worktree(incus) = %q, want the host worktree", got)
	}

	if got := m.MachineUser(pod); got != image.Placeholder {
		t.Errorf("MachineUser(pod) = %+v, want image.Placeholder", got)
	}
	if got := m.MachineHome(pod); got != "/home/agent" {
		t.Errorf("MachineHome(pod) = %q, want /home/agent", got)
	}
	if got := m.Worktree(pod); got != "/workspace" {
		t.Errorf("Worktree(pod) = %q, want the pod's mount", got)
	}

	// A pod the cluster hasn't told a mount yet gets the image's own.
	if got := m.Worktree(state.Agent{Cluster: "prod"}); got != "/workspace" {
		t.Errorf("Worktree(pod without a workspace) = %q, want /workspace", got)
	}
	// And a worktree with no cluster is still the host's path.
	if got := m.Worktree(state.Agent{Worktree: "/work/agent-02"}); got != "/work/agent-02" {
		t.Errorf("Worktree(agent with no cluster) = %q, want its host worktree", got)
	}
}

// TestAgentPathsAreUnderTheMachinesUser checks every file an agent's tools
// read under its own user's home: the daemon's on Incus, the image's agent in
// a pod.
func TestAgentPathsAreUnderTheMachinesUser(t *testing.T) {
	m := &agent.Manager{User: image.User{Name: "dev", UID: 1000, GID: 1000}}
	for _, tc := range []struct {
		name string
		a    state.Agent
		home string
	}{
		{"incus", state.Agent{Worktree: "/work/agent-01"}, "/home/dev"},
		{"pod", state.Agent{Cluster: "prod", Workspace: "/workspace", Worktree: "/host/agent-01"}, "/home/agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.MachineHome(tc.a); got != tc.home {
				t.Errorf("MachineHome() = %q, want %q", got, tc.home)
			}
			for what, got := range map[string]string{
				"env":     m.EnvPathFor(tc.a),
				"bashenv": m.BashEnvPathFor(tc.a),
				"secrets": m.SecretsPathFor(tc.a),
			} {
				want := map[string]string{
					"env":     tc.home + "/.config/agentbox/env",
					"bashenv": tc.home + "/.config/agentbox/bash_env",
					"secrets": tc.home + "/.config/agentbox/secrets.env",
				}[what]
				if got != want {
					t.Errorf("%s path = %q, want %q", what, got, want)
				}
			}
		})
	}
}

// TestAttachArgvHasNoRunuserInAPod checks the one argv difference: a pod's
// agentd already runs as the agent user, so its terminal is tmux alone, while
// Incus still goes through runuser as it always has.
func TestAttachArgvHasNoRunuserInAPod(t *testing.T) {
	pm := &podMachine{ref: "ab-p-agent-01"}
	m := &agent.Manager{
		User:       image.User{Name: "dev", UID: 1000, GID: 1000},
		Kubernetes: podRuntime{m: pm},
	}
	pod := state.Agent{Instance: "ab-p-agent-01", Cluster: "prod", Workspace: "/workspace", Worktree: "/host/agent-01"}

	want := []string{"tmux", "new-session", "-A", "-s", "main", "-c", "/workspace"}
	if got := m.AttachArgv(pod); !reflect.DeepEqual(got, want) {
		t.Errorf("AttachArgv(pod) = %#v, want %#v", got, want)
	}
	if slices.Contains(m.AttachArgv(pod), "runuser") {
		t.Errorf("a pod's attach argv uses runuser: %#v", m.AttachArgv(pod))
	}

	// Attach hands the machine that argv, and no wrapper of its own.
	if _, err := m.Attach(context.Background(), pod, 120, 40); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pm.attach.Argv, want) {
		t.Errorf("Attach ran %#v, want %#v", pm.attach.Argv, want)
	}
	if pm.attach.Cols != 120 || pm.attach.Rows != 40 {
		t.Errorf("Attach opened %dx%d, want 120x40", pm.attach.Cols, pm.attach.Rows)
	}

	incus := state.Agent{Instance: "ab-p-agent-01", Worktree: "/work/agent-01"}
	if got, want := m.AttachArgv(incus), agent.ShellArgv("dev", "/work/agent-01"); !reflect.DeepEqual(got, want) {
		t.Errorf("AttachArgv(incus) = %#v, want %#v", got, want)
	} else if !slices.Contains(got, "runuser") {
		t.Errorf("an Incus terminal no longer runs through runuser: %#v", got)
	}
}

// TestChatProcessForAPodStreamsTheAdapterOverUserExec runs the pod's chat path
// end to end: the same command line ChatCommand builds, as the image's agent,
// with the chat's three streams carried over one UserExec.
func TestChatProcessForAPodStreamsTheAdapterOverUserExec(t *testing.T) {
	pm := &podMachine{ref: "ab-p-agent-01"}
	m := &agent.Manager{
		User:       image.User{Name: "dev", UID: 1000, GID: 1000},
		Kubernetes: podRuntime{m: pm},
		Creds:      credentials.Store{Dir: t.TempDir()},
		Store:      openStore(t),
	}
	pod := state.Agent{
		Project: "p", Name: "agent-01", Instance: "ab-p-agent-01", AI: "claude",
		Cluster: "prod", Workspace: "/workspace", Worktree: "/host/agent-01",
	}

	proc, err := m.ChatProcess(context.Background(), pod, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Stop()

	line := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"
	if _, err := io.WriteString(proc.Stdin, line); err != nil {
		t.Fatal(err)
	}
	echoed, err := bufio.NewReader(proc.Stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if echoed != line {
		t.Errorf("the adapter read back %q, want %q", echoed, line)
	}
	if err := proc.Wait(); err != nil {
		t.Errorf("Wait() = %v, want the fake adapter's success", err)
	}
	if got := proc.Stderr(); !strings.Contains(got, "fake adapter") {
		t.Errorf("Stderr() = %q, want what the adapter wrote", got)
	}

	// The exec that carried the streams is the adapter's own command line,
	// not the `mise where` check that came before it.
	if pm.user != "agent" {
		t.Errorf("the adapter ran as %q, want the pod's agent user", pm.user)
	}
	for _, want := range []string{"cd '/workspace' &&", "exec 'claude-agent-acp'"} {
		if !strings.Contains(pm.command, want) {
			t.Errorf("the adapter's command doesn't have %q:\n%s", want, pm.command)
		}
	}
	if strings.Contains(pm.command, "mise") {
		t.Errorf("a pod's adapter still goes through mise, which its image does not have:\n%s", pm.command)
	}
	if strings.Contains(pm.command, "/host/agent-01") {
		t.Errorf("the adapter was pointed at the host's path:\n%s", pm.command)
	}
}

// TestChatProcessRefusesCursorInAPod checks the one tool the image cannot run
// yet: Cursor's adapter is AgentBox's own script over the Cursor SDK, which the
// image does not carry, so the chat says that instead of failing inside node.
func TestChatProcessRefusesCursorInAPod(t *testing.T) {
	pm := &podMachine{ref: "ab-p-agent-01"}
	m := &agent.Manager{
		User:       image.User{Name: "dev", UID: 1000, GID: 1000},
		Kubernetes: podRuntime{m: pm},
		Creds:      credentials.Store{Dir: t.TempDir()},
		Store:      openStore(t),
	}
	pod := state.Agent{
		Project: "p", Name: "agent-01", Instance: "ab-p-agent-01", AI: "cursor",
		Cluster: "prod", Workspace: "/workspace",
	}
	_, err := m.ChatProcess(context.Background(), pod, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Cursor") {
		t.Fatalf("ChatProcess(cursor pod) = %v, want a refusal naming Cursor", err)
	}
	if pm.command != "" {
		t.Errorf("the refusal still ran a command in the pod: %q", pm.command)
	}
}

// TestChatProcessStopCancelsTheExec checks that Stop ends a pod's chat by
// cancelling the UserExec it is running, the way Stop kills an Incus exec.
func TestChatProcessStopCancelsTheExec(t *testing.T) {
	pm := &podMachine{ref: "ab-p-agent-01", block: true}
	m := &agent.Manager{
		User:       image.User{Name: "dev", UID: 1000, GID: 1000},
		Kubernetes: podRuntime{m: pm},
		Creds:      credentials.Store{Dir: t.TempDir()},
		Store:      openStore(t),
	}
	pod := state.Agent{
		Project: "p", Name: "agent-01", Instance: "ab-p-agent-01", AI: "claude",
		Cluster: "prod", Workspace: "/workspace",
	}
	proc, err := m.ChatProcess(context.Background(), pod, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	proc.Stop()
	if err := proc.Wait(); err == nil {
		t.Error("Wait() = nil after Stop, want the cancelled exec's error")
	}
}

func openStore(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// podRuntime is one Kubernetes cluster in a test, with one pod.
type podRuntime struct{ m *podMachine }

func (r podRuntime) Kind() mach.Kind             { return mach.KindKubernetes }
func (r podRuntime) Machine(string) mach.Machine { return r.m }

// podMachine answers the calls this test makes as a running pod's agentd
// does: it reports itself running, records the terminal it was asked for, and
// speaks one JSON line back over UserExec's streams.
type podMachine struct {
	ref string
	// block makes the adapter's own command wait for its context, as a real
	// adapter does until the chat stops it.
	block bool

	user    string
	command string
	attach  mach.AttachOptions
}

func (m *podMachine) Ref() string     { return m.ref }
func (m *podMachine) Kind() mach.Kind { return mach.KindKubernetes }

func (m *podMachine) Info(context.Context) (mach.Info, error) {
	return mach.Info{Ref: m.ref, State: "Running"}, nil
}

func (m *podMachine) WaitReady(context.Context, time.Duration) (mach.Info, error) {
	return m.Info(context.Background())
}

func (m *podMachine) Start(context.Context) error                     { return nil }
func (m *podMachine) Stop(context.Context) error                      { return nil }
func (m *podMachine) StopWithin(context.Context, time.Duration) error { return nil }
func (m *podMachine) ForceStop(context.Context) error                 { return nil }
func (m *podMachine) Pause(context.Context) error                     { return nil }
func (m *podMachine) Resume(context.Context) error                    { return nil }
func (m *podMachine) Destroy(context.Context) error                   { return nil }

func (m *podMachine) Exec(context.Context, ...string) (string, error) { return "", nil }

func (m *podMachine) ExecInput(context.Context, io.Reader, ...string) (string, error) {
	return "", nil
}

func (m *podMachine) UserExec(ctx context.Context, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	m.user, m.command = user, command
	if strings.Contains(command, "mise where") {
		return nil // the adapter is already installed
	}
	if m.block {
		<-ctx.Done()
		return ctx.Err()
	}
	// Speak as an adapter would: one JSON line in, the same one out, and
	// something on stderr, so the test sees all three streams cross.
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil {
		return err
	}
	if _, err := fmt.Fprint(stdout, line); err != nil {
		return err
	}
	_, err = fmt.Fprint(stderr, "agentd: fake adapter\n")
	return err
}

func (m *podMachine) Attach(_ context.Context, opts mach.AttachOptions) (mach.Terminal, error) {
	m.attach = opts
	return nil, nil
}

// WriteFile is never reached by this test's chat: Claude Code's adapter is
// installed by mise rather than carried by AgentBox.
func (m *podMachine) WriteFile(context.Context, string, []byte, int, int, os.FileMode) error {
	return nil
}

func (m *podMachine) PullFile(context.Context, string, string) error              { return nil }
func (m *podMachine) PullDir(context.Context, string, string) error               { return nil }
func (m *podMachine) PushFile(context.Context, string, string, os.FileMode) error { return nil }
func (m *podMachine) Snapshot(context.Context, string) error                      { return nil }

func (m *podMachine) SnapshotList(context.Context) ([]mach.Snapshot, error) { return nil, nil }

func (m *podMachine) RestoreSnapshot(context.Context, string) error { return nil }
func (m *podMachine) DeleteSnapshot(context.Context, string) error  { return nil }

func (m *podMachine) HasSnapshot(context.Context, string) (bool, error) { return false, nil }

func (m *podMachine) Usage(context.Context) (int64, error) { return 0, nil }

// Dial is only for a desktop's or the agent API's socket, which this test
// never opens.
func (m *podMachine) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("a fake pod has no network")
}
