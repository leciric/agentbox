package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentbox/internal/credentials"
	"agentbox/internal/gitrepo"
	"agentbox/internal/image"
	mach "agentbox/internal/machine"
	"agentbox/internal/paths"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
	"agentbox/internal/workspace"
)

// clusterFixture is a project on this host whose agents can be made as pods in a
// fake cluster: the same store and repository a create uses, a Manager whose
// Kubernetes runtime is the fake, and the fake itself, which records what the
// pod's machine was asked to do.
type clusterFixture struct {
	m       *Manager
	st      *state.Store
	repo    gitrepo.Repo
	project state.Project
	pod     *podMachineStub
	// incusLog is where a real Incus call would be written. It has to stay
	// empty, because a cluster agent never touches Incus.
	incusLog string
}

func newClusterFixture(t *testing.T, readyErr error) clusterFixture {
	t.Helper()
	root := testutil.FixtureRepo(t, "hello-stack")
	st := openInternalStore(t)
	if err := st.AddProject(context.Background(), state.Project{Name: "hello-stack", Root: root, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.Project(context.Background(), "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	incusLog := filepath.Join(t.TempDir(), "incus-calls")
	pod := &podMachineStub{ref: "hello-stack/agent-01", readyErr: readyErr}
	// The project has a private profile its agents clone with, and the token
	// that profile signs in with: a pod can't exist without either
	// (gitprofile.go).
	credsDir := t.TempDir()
	creds := credentials.Store{Dir: credsDir}
	if err := st.SetProjectGitRemote(context.Background(), state.GitRemote{
		Project: "hello-stack", Profile: state.GitProfilePrivate,
		URL: "https://hub.example/hello-stack.git", Auth: state.GitAuthToken, Credential: "private",
	}); err != nil {
		t.Fatal(err)
	}
	if err := creds.SetProjectGitCredential(context.Background(), "hello-stack", "private", credentials.GitCredentialToken, "pod-token"); err != nil {
		t.Fatal(err)
	}
	return clusterFixture{
		project:  project,
		st:       st,
		repo:     repo,
		pod:      pod,
		incusLog: incusLog,
		m: &Manager{
			Store: st,
			Incus: fakeIncus(t, `echo "$*" >> `+incusLog),
			Kubernetes: clusterRuntime{
				m: pod,
			},
			Paths:   paths.Paths{Config: t.TempDir(), Data: t.TempDir()},
			Creds:   creds,
			Secrets: secrets.Store{State: st, KeyPath: filepath.Join(t.TempDir(), "secrets.key")},
			User:    image.User{Name: "dev", UID: 1000, GID: 1000},
			Log:     io.Discard,
		},
	}
}

// plan is the create a cluster agent's build is given: a new Claude-free agent
// (no model or effort to seed) in the fixture's project, made as a pod.
func (f clusterFixture) plan(t *testing.T) plan {
	t.Helper()
	commit, err := f.repo.ResolveCommit("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return plan{
		project:    f.project,
		repo:       f.repo,
		name:       "agent-01",
		ai:         "none",
		iface:      state.InterfaceChat,
		baseRef:    f.repo.CurrentBranch(),
		baseCommit: commit,
		cluster:    "k3d",
	}
}

// TestBuildOnAClusterMakesAPodAndNeverTouchesIncus is the cluster create path:
// no instance is copied, no host worktree is made, the pod's machine is the one
// that is configured, and the row says where its machine is.
func TestBuildOnAClusterMakesAPodAndNeverTouchesIncus(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()

	a, err := f.m.build(ctx, f.plan(t))
	if err != nil {
		t.Fatalf("build() = %v", err)
	}

	if a.Status != state.AgentReady {
		t.Errorf("status = %q, want %q", a.Status, state.AgentReady)
	}
	row, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if row.Cluster != "k3d" || row.Runtime != state.RuntimeKubernetes {
		t.Errorf("row has cluster %q and runtime %q, want k3d and %q", row.Cluster, row.Runtime, state.RuntimeKubernetes)
	}
	if row.Workspace != "/workspace" {
		t.Errorf("workspace = %q, want the pod's volume mount", row.Workspace)
	}
	if row.Worktree != "" {
		t.Errorf("worktree = %q, want none: the tree is inside the pod", row.Worktree)
	}
	if row.DesiredState != state.DesiredRunning {
		t.Errorf("desired state = %q, want %q", row.DesiredState, state.DesiredRunning)
	}
	if row.Status != state.AgentReady {
		t.Errorf("stored status = %q, want %q", row.Status, state.AgentReady)
	}

	// The pod's machine was awaited, then configured and given its session.
	if f.pod.waits != 1 {
		t.Errorf("WaitReady was called %d times, want once", f.pod.waits)
	}
	var configured bool
	for _, path := range f.pod.writes {
		if strings.HasPrefix(path, "/home/agent/") {
			configured = true
		}
		if strings.HasPrefix(path, "/home/dev/") {
			t.Errorf("wrote %q, which is the daemon's user's home, not the pod's", path)
		}
	}
	if !configured {
		t.Errorf("configure wrote %v, want files under the pod's /home/agent", f.pod.writes)
	}
	if len(f.pod.userExecs) == 0 {
		t.Error("the pod's tmux session was never started: no UserExec ran")
	}

	// Nothing Incus was asked for: not a copy, not a start, not a device.
	if _, err := os.Stat(f.incusLog); err == nil {
		calls, _ := os.ReadFile(f.incusLog)
		t.Errorf("a cluster create ran incus: %s", calls)
	}
}

// TestBuildKeepsTheRowWhenThePodNeverAnswers checks the one rule that keeps a
// failed pod deletable: a failure from the moment the pod is asked for leaves
// the row, marked destroyed, because the row is what tells the reconciler which
// pod to delete.
func TestBuildKeepsTheRowWhenThePodNeverAnswers(t *testing.T) {
	f := newClusterFixture(t, errors.New("no gateway session"))
	ctx := context.Background()

	_, err := f.m.build(ctx, f.plan(t))
	if err == nil {
		t.Fatal("build() = nil, want the pod's failure")
	}
	var create *CreateError
	if !errors.As(err, &create) || create.Step != "pod" {
		t.Fatalf("build() = %v, want a create error naming the pod step", err)
	}
	if !strings.Contains(err.Error(), "agentbox cluster doctor k3d") {
		t.Errorf("the pod error doesn't say how to check the cluster: %v", err)
	}

	row, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatalf("the row was removed, and with it the only way to find the pod: %v", err)
	}
	if row.DesiredState != state.DesiredDestroyed {
		t.Errorf("desired state = %q, want %q", row.DesiredState, state.DesiredDestroyed)
	}
	if row.DestroyedAt.IsZero() {
		t.Error("the row has no destroyed time, so nothing tells the cluster when to delete the pod")
	}
	if f.pod.waits != 1 {
		t.Errorf("WaitReady was called %d times, want once", f.pod.waits)
	}
	if len(f.pod.writes) != 0 {
		t.Errorf("a pod that never answered was configured anyway: %v", f.pod.writes)
	}
}

// TestWorkspaceOfAClusterAgentIsItsPod checks that a cluster agent's tree is
// read in the pod and never from a path on this host, and that an Incus agent
// keeps its worktree.
func TestWorkspaceOfAClusterAgentIsItsPod(t *testing.T) {
	m := &Manager{}
	pod := state.Agent{Project: "hello-stack", Name: "agent-01", Cluster: "k3d", Workspace: "/workspace"}
	ws, ok := m.workspace(pod).(workspace.Pod)
	if !ok {
		t.Fatalf("workspace(cluster agent) = %T, want workspace.Pod", m.workspace(pod))
	}
	if got := ws.Path(); got != "" {
		t.Errorf("Path() = %q, want an empty path for a tree that isn't here", got)
	}
	if ws.Dir != "/workspace" || ws.User != image.Placeholder.Name {
		t.Errorf("workspace(cluster agent) = %#v, want the pod's /workspace as its user %q", ws, image.Placeholder.Name)
	}
	if ws.Machine.Ref() != pod.Ref() {
		t.Errorf("the workspace's machine is %q, want the agent's own %q", ws.Machine.Ref(), pod.Ref())
	}

	// An Incus agent keeps its worktree, as it always has.
	incus := state.Agent{Project: "hello-stack", Name: "agent-01", Worktree: "/work/agent-01"}
	if local, ok := m.workspace(incus).(workspace.Local); !ok || local.Dir != incus.Worktree {
		t.Errorf("workspace(Incus agent) = %#v, want the host worktree", m.workspace(incus))
	}
}

// TestListReadsAClusterAgentsStateFromItsRow checks that listing a cluster
// agent asks its pod's machine, not Incus, and that a host with no Incus
// installed can still list the agents it doesn't run.
func TestListReadsAClusterAgentsStateFromItsRow(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if _, err := f.m.build(ctx, f.plan(t)); err != nil {
		t.Fatalf("build() = %v", err)
	}
	// What the reconciler writes once it has made and adopted the pod: a pod
	// with nothing wrong with it, for an agent asked to run.
	if err := f.st.SetAgentPod(ctx, "hello-stack", "agent-01", "ab-hello-stack-agent-01", "uid-1", ""); err != nil {
		t.Fatal(err)
	}

	statuses, err := f.m.List(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("List() = %+v, want the one cluster agent", statuses)
	}
	if got := statuses[0].State; got != "running" {
		t.Errorf("state = %q, want running, which is what the row asks for", got)
	}
	if _, err := os.Stat(f.incusLog); err == nil {
		calls, _ := os.ReadFile(f.incusLog)
		t.Errorf("listing a cluster agent ran incus: %s", calls)
	}
}

// TestPausingAClusterAgentSaysToStopIt checks the substitute pause: a pod is
// stopped and started, and the error says so rather than leaving the caller to
// guess why freezing failed.
func TestPausingAClusterAgentSaysToStopIt(t *testing.T) {
	f := newClusterFixture(t, nil)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Cluster: "k3d", Workspace: "/workspace"}
	ctx := context.Background()

	err := f.m.Pause(ctx, a)
	if !errors.Is(err, mach.ErrNotSupported) {
		t.Fatalf("Pause() = %v, want the machine's not-supported error under it", err)
	}
	if !strings.Contains(err.Error(), "agentbox stop hello-stack/agent-01") {
		t.Errorf("Pause() = %v, want it to say how to stop the agent instead", err)
	}
	err = f.m.Resume(ctx, a)
	if !errors.Is(err, mach.ErrNotSupported) {
		t.Fatalf("Resume() = %v, want the machine's not-supported error under it", err)
	}
	if !strings.Contains(err.Error(), "agentbox start hello-stack/agent-01") {
		t.Errorf("Resume() = %v, want it to say how to start the agent instead", err)
	}
}

// TestDestroyOfAClusterAgentAsksTheClusterAndArchivesIt checks the pod's
// destroy: the machine is asked to go (which is what the reconciler reads), no
// host worktree is touched, and the agent is kept as a past agent.
func TestDestroyOfAClusterAgentAsksTheClusterAndArchivesIt(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if _, err := f.m.build(ctx, f.plan(t)); err != nil {
		t.Fatalf("build() = %v", err)
	}
	a, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}

	if err := f.m.Destroy(ctx, a, DestroyOptions{Force: true}); err != nil {
		t.Fatalf("Destroy() = %v", err)
	}
	if !f.pod.destroyed {
		t.Error("the pod was never asked to go, so nothing would delete it")
	}
	if _, err := f.st.Agent(ctx, "hello-stack", "agent-01"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the row is still there (%v), want the agent archived", err)
	}
	if _, err := os.Stat(f.incusLog); err == nil {
		calls, _ := os.ReadFile(f.incusLog)
		t.Errorf("destroying a cluster agent ran incus: %s", calls)
	}
}

// TestDestroyOfAClusterAgentDeletesItsMachineBeforeArchiving checks the order
// the row makes possible: the daemon's hook runs while the row still names the
// pod, and the row is archived only once the machine is gone.
func TestDestroyOfAClusterAgentDeletesItsMachineBeforeArchiving(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if _, err := f.m.build(ctx, f.plan(t)); err != nil {
		t.Fatalf("build() = %v", err)
	}
	a, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	var rowThere bool
	f.m.DestroyMachine = func(ctx context.Context, a state.Agent) error {
		calls++
		_, err := f.st.Agent(ctx, a.Project, a.Name)
		rowThere = err == nil
		return nil
	}

	if err := f.m.Destroy(ctx, a, DestroyOptions{Force: true}); err != nil {
		t.Fatalf("Destroy() = %v", err)
	}
	if calls != 1 {
		t.Errorf("the machine hook ran %d times, want once", calls)
	}
	if !rowThere {
		t.Error("the row was archived before its machine was deleted, and nothing names the pod any more")
	}
	if _, err := f.st.Agent(ctx, "hello-stack", "agent-01"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the row is still there (%v), want the agent archived", err)
	}
}

// TestDestroyKeepsAClusterAgentsRowWhenItsMachineCannotBeDeleted checks that a
// failed deletion leaves the agent as it was: the row is the only thing that
// names the pod, so archiving it would orphan the pod for good.
func TestDestroyKeepsAClusterAgentsRowWhenItsMachineCannotBeDeleted(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if _, err := f.m.build(ctx, f.plan(t)); err != nil {
		t.Fatalf("build() = %v", err)
	}
	a, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("the cluster is unreachable")
	var calls int
	f.m.DestroyMachine = func(context.Context, state.Agent) error {
		calls++
		return failure
	}

	err = f.m.Destroy(ctx, a, DestroyOptions{Force: true})
	if !errors.Is(err, failure) {
		t.Fatalf("Destroy() = %v, want the machine's failure", err)
	}
	if calls != 1 {
		t.Errorf("the machine hook ran %d times, want once", calls)
	}
	row, err := f.st.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatalf("the row was archived after its machine wasn't deleted: %v", err)
	}
	if row.Status != a.Status {
		t.Errorf("status = %q, want %q back, so the agent can be found and destroyed again", row.Status, a.Status)
	}
}

// TestCreateMakesAPodWithoutAnIncusBaseImage is the whole create: a host with
// no image built and a cluster it has, making an agent whose machine is a pod
// there. Nothing Incus is asked for, including the base image check.
func TestCreateMakesAPodWithoutAnIncusBaseImage(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()
	if err := f.st.AddCluster(ctx, state.Cluster{Name: "k3d", Namespace: "agentbox", Kubeconfig: []byte("sealed"), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	a, err := f.m.Create(ctx, "hello-stack", CreateOptions{AI: "none", Name: "agent-01", Cluster: "k3d"})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if a.Cluster != "k3d" || a.Runtime != state.RuntimeKubernetes || a.Workspace != "/workspace" {
		t.Errorf("Create() = cluster %q, runtime %q, workspace %q, want k3d, %q and /workspace", a.Cluster, a.Runtime, a.Workspace, state.RuntimeKubernetes)
	}
	if a.Source != "" {
		t.Errorf("source = %q, want none: a pod is made from the cluster's image, not an Incus snapshot", a.Source)
	}
	if _, err := os.Stat(f.incusLog); err == nil {
		calls, _ := os.ReadFile(f.incusLog)
		t.Errorf("creating in a cluster ran incus: %s", calls)
	}
}

// TestCreateRefusesAClusterItDoesNotHave checks the refusal happens before
// anything is stored, and says how to add the cluster.
func TestCreateRefusesAClusterItDoesNotHave(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()

	_, err := f.m.Create(ctx, "hello-stack", CreateOptions{AI: "none", Cluster: "nope"})
	if err == nil {
		t.Fatal("Create() = nil, want the unknown cluster refused")
	}
	if !strings.Contains(err.Error(), "agentbox cluster add nope") {
		t.Errorf("Create() = %v, want it to name the command that adds the cluster", err)
	}
	agents, err := f.st.Agents(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Errorf("Create() left %d agent(s) behind, want none", len(agents))
	}
	if _, err := os.Stat(filepath.Join(f.m.Paths.Worktrees(), "hello-stack")); err == nil {
		t.Error("Create() made a worktree for an agent it refused")
	}
}

// TestMachineOfAClusterAgentWithoutAKubernetesRuntimeSaysSo checks the wiring
// mistake: a cluster agent whose daemon never set the runtime gets an error
// from every operation instead of a panic.
func TestMachineOfAClusterAgentWithoutAKubernetesRuntimeSaysSo(t *testing.T) {
	m := &Manager{}
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Cluster: "k3d"}
	_, err := m.machine(a).Info(context.Background())
	if err == nil {
		t.Fatal("Info() = nil, want the missing runtime reported")
	}
	if !strings.Contains(err.Error(), "no Kubernetes runtime") {
		t.Errorf("Info() = %v, want it to name the missing runtime", err)
	}
	if _, err := m.machine(a).WaitReady(context.Background(), time.Second); err == nil {
		t.Error("WaitReady() = nil, want the missing runtime reported")
	}
}

// TestWriteSkillsWritesUnderTheMachinesUser checks the seam's one leftover
// host path: a pod installs skills under the image's agent user, and Incus keeps
// writing under the daemon's own account.
func TestWriteSkillsWritesUnderTheMachinesUser(t *testing.T) {
	f := newClusterFixture(t, nil)
	ctx := context.Background()

	pod := state.Agent{Project: "hello-stack", Name: "agent-01", Cluster: "k3d", Workspace: "/workspace"}
	if err := f.m.WriteSkills(ctx, pod); err != nil {
		t.Fatalf("WriteSkills(pod) = %v", err)
	}
	if len(f.pod.execs) != 1 {
		t.Fatalf("the pod ran %d installs, want one", len(f.pod.execs))
	}
	install := f.pod.execs[0]
	if got := install[len(install)-2:]; !reflect.DeepEqual(got, []string{"/home/agent", "1000:1000"}) {
		t.Errorf("the pod's skills install = %v, want them under /home/agent as 1000:1000", install)
	}

	// An Incus agent's machine is reached through Runtime here, so the test
	// never runs the real incus binary; what it checks is the path and owner.
	f.m.User = image.User{Name: "dev", UID: 1234, GID: 5678}
	f.m.Runtime = clusterRuntime{m: f.pod}
	incus := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", Worktree: "/work/agent-02"}
	if err := f.m.WriteSkills(ctx, incus); err != nil {
		t.Fatalf("WriteSkills(Incus agent) = %v", err)
	}
	install = f.pod.execs[1]
	if got := install[len(install)-2:]; !reflect.DeepEqual(got, []string{"/home/dev", "1234:5678"}) {
		t.Errorf("the Incus install = %v, want it under /home/dev as this host's user", install)
	}
}

// clusterRuntime is a cluster in a test: every agent in it gets the same pod.
type clusterRuntime struct{ m *podMachineStub }

func (r clusterRuntime) Kind() mach.Kind             { return mach.KindKubernetes }
func (r clusterRuntime) Machine(string) mach.Machine { return r.m }

// podMachineStub is a pod's machine as these tests need it. It embeds the
// interface, so a method a test did not expect panics loudly instead of being
// answered here by accident.
type podMachineStub struct {
	mach.Machine
	ref string
	// readyErr is what WaitReady reports instead of becoming ready.
	readyErr error
	// state is what Info reports; "Running" unless a test says otherwise.
	state     string
	destroyed bool

	waits     int
	writes    []string
	userExecs []string
	// execs are the commands ExecInput ran, as it was given them.
	execs [][]string
}

func (m *podMachineStub) Ref() string     { return m.ref }
func (m *podMachineStub) Kind() mach.Kind { return mach.KindKubernetes }

func (m *podMachineStub) Info(context.Context) (mach.Info, error) {
	state := m.state
	if state == "" {
		state = "Running"
	}
	return mach.Info{Ref: m.ref, State: state}, nil
}

func (m *podMachineStub) Destroy(context.Context) error {
	m.destroyed = true
	return nil
}

// Pause and Resume answer as the real pod machine does: a pod is stopped and
// started, and there is no freeze to take or lift.
func (m *podMachineStub) Pause(context.Context) error {
	return fmt.Errorf("a pod is stopped, not frozen: %w", mach.ErrNotSupported)
}

func (m *podMachineStub) Resume(context.Context) error {
	return fmt.Errorf("a pod is started, not resumed: %w", mach.ErrNotSupported)
}

func (m *podMachineStub) WaitReady(context.Context, time.Duration) (mach.Info, error) {
	m.waits++
	if m.readyErr != nil {
		return mach.Info{}, m.readyErr
	}
	return mach.Info{Ref: m.ref, State: "Running"}, nil
}

func (m *podMachineStub) WriteFile(_ context.Context, path string, _ []byte, _, _ int, _ os.FileMode) error {
	m.writes = append(m.writes, path)
	return nil
}

func (m *podMachineStub) UserExec(_ context.Context, _, command string, _ io.Reader, _, _ io.Writer) error {
	m.userExecs = append(m.userExecs, command)
	return nil
}

func (m *podMachineStub) ExecInput(_ context.Context, _ io.Reader, command ...string) (string, error) {
	m.execs = append(m.execs, command)
	return "", nil
}

// Dial is only for a desktop's or the agent API's socket, which these tests
// never open.
func (m *podMachineStub) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("a fake pod has no network")
}
