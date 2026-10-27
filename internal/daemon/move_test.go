package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/api"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// moveTestCluster is one cluster of a move, with the fake API server the daemon
// asks it through.
type moveTestCluster struct {
	state  state.Cluster
	client *k8s.Cluster
	fake   *fake.Clientset
}

// moveTestClusters is a source and a target, both fake, and a project to move an
// agent of. Names stay short: every test's daemon listens on a unix socket
// under its own temporary directory.
func moveTestClusters(t *testing.T, srv *Server) (source, target moveTestCluster) {
	t.Helper()
	source = moveTestCluster{state: state.Cluster{
		Name: "k3d", Namespace: "agentbox", Image: "agentbox-agent:dev",
		MasterURL: "https://master.example.test", CreatedAt: time.Now(),
	}}
	target = moveTestCluster{state: state.Cluster{
		Name: "k3s", Namespace: "agentbox", Image: "agentbox-agent:dev",
		MasterURL: "https://master.example.test", CreatedAt: time.Now(),
	}}
	for _, c := range []*moveTestCluster{&source, &target} {
		c.fake = podClient()
		c.client = clusterWithFakeClient(t, srv, c.state, c.fake)
	}
	return source, target
}

// moveTestAgent is an agent of the test project on the source cluster, with the
// pod its row says it has when one does.
func moveTestAgent(t *testing.T, srv *Server, desired string) state.Agent {
	t.Helper()
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	reconcilerAgent(t, srv.store, "agent-01")
	if desired != state.DesiredRunning {
		if err := srv.store.SetAgentDesiredState(ctx, "pawly", "agent-01", desired); err != nil {
			t.Fatal(err)
		}
	}
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// moveTestClaim gives a fake cluster the claim a move archives, so that its
// removal can be asserted.
func moveTestClaim(t *testing.T, c moveTestCluster, name string) {
	t.Helper()
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.state.Namespace}}
	if _, err := c.fake.CoreV1().PersistentVolumeClaims(c.state.Namespace).Create(context.Background(), claim, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func moveTestHasClaim(c moveTestCluster, name string) bool {
	_, err := c.fake.CoreV1().PersistentVolumeClaims(c.state.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	return err == nil
}

// moveTestHooks replaces what a move asks of a real cluster and pod, and records
// what each step was asked. A field left nil takes the hook's own harmless
// answer: the daemon's real checkpoint needs a pod, and its real archive needs
// a REST config, which a fake clientset has none of.
type moveTestHooks struct {
	checkpoints int
	restarts    int
	archivedAs  string
	restoredAs  string
	restoredImg string
	adopted     bool

	onArchive func(c *k8s.Cluster, spec k8s.Spec, w io.Writer) error
	onRestore func(c *k8s.Cluster, spec k8s.Spec, r io.Reader) error
	onAdopt   func(srv *Server) error
}

// withMoveTestHooks puts the hooks in place of the daemon's steps and restores them
// afterwards. The seams are package variables, so this test is not parallel:
// another test moving at the same time would see these.
func withMoveTestHooks(t *testing.T, h *moveTestHooks) *moveTestHooks {
	t.Helper()
	oldCheckpoint, oldArchive, oldRestore, oldAdopt, oldRestart := moveCheckpoint, moveArchive, moveRestore, moveAdopt, moveRestart
	moveCheckpoint = func(ctx context.Context, log io.Writer, s *Server, a state.Agent, target string) (string, error) {
		h.checkpoints++
		return "commit-1", nil
	}
	moveArchive = func(c *k8s.Cluster, ctx context.Context, spec k8s.Spec, image, tag string, w io.Writer) error {
		if h.onArchive != nil {
			return h.onArchive(c, spec, w)
		}
		h.archivedAs = spec.WorkspaceClaim()
		_, err := w.Write([]byte("a tarball"))
		return err
	}
	moveRestore = func(c *k8s.Cluster, ctx context.Context, spec k8s.Spec, image, tag string, r io.Reader) error {
		if h.onRestore != nil {
			return h.onRestore(c, spec, r)
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return err
		}
		h.restoredAs, h.restoredImg = spec.WorkspaceClaim(), image
		return nil
	}
	moveAdopt = func(ctx context.Context, log io.Writer, s *Server, a state.Agent) error {
		h.adopted = true
		if h.onAdopt != nil {
			return h.onAdopt(s)
		}
		return nil
	}
	moveRestart = func(ctx context.Context, s *Server, a state.Agent) error {
		h.restarts++
		return s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredRunning)
	}
	t.Cleanup(func() {
		moveCheckpoint, moveArchive, moveRestore, moveAdopt, moveRestart = oldCheckpoint, oldArchive, oldRestore, oldAdopt, oldRestart
	})
	return h
}

// moveTestRequest is POST /v1/agents/{project}/{agent}/move, with the path values
// the router would have set.
func moveTestRequest(t *testing.T, project, agent, cluster string) *http.Request {
	t.Helper()
	body, err := json.Marshal(api.MoveAgentRequest{Cluster: cluster})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/"+project+"/"+agent+"/move", strings.NewReader(string(body)))
	r.SetPathValue("project", project)
	r.SetPathValue("agent", agent)
	return r
}

// TestMoveRefusesWhatItCannotMove is the endpoint's refusals: a machine on this
// host, the cluster the agent is already in, this host as a target, and no
// target at all.
func TestMoveRefusesWhatItCannotMove(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	reconcilerProject(t, srv.store)
	moveTestClusters(t, srv)
	if err := srv.store.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "host-1", Instance: "ab-pawly-host-1", AI: "claude",
		Branch: "agentbox/host-1", BaseRef: "main", BaseCommit: "abc", Status: "ready", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	reconcilerAgent(t, srv.store, "agent-01")

	for _, tc := range []struct {
		name, agent, cluster, want string
	}{
		{"on this host", "host-1", "k3s", "runs on this host"},
		{"the same cluster", "agent-01", "k3d", "already runs in cluster k3d"},
		{"this host as a target", "agent-01", "local", "is this host"},
		{"no target", "agent-01", "", "needs a target cluster"},
		{"no such cluster", "agent-01", "nope", `no cluster named "nope"`},
	} {
		err := srv.moveAgent(httptest.NewRecorder(), moveTestRequest(t, "pawly", tc.agent, tc.cluster))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: move = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestMoveCarriesTheClaimAndTheRowToTheTarget is a whole move: the worktree is
// checkpointed, the claim is archived and restored under a new name, the row
// names the target with no image pin, and the source's claim is gone.
func TestMoveCarriesTheClaimAndTheRowToTheTarget(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	a := moveTestAgent(t, srv, state.DesiredRunning)
	if err := srv.store.SetAgentPod(ctx, a.Project, a.Name, "ab-pawly-agent-01", "uid-source", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetAgentClaim(ctx, a.Project, a.Name, "", "agentbox-agent:pinned"); err != nil {
		t.Fatal(err)
	}
	source, target := moveTestClusters(t, srv)
	oldClaim := k8s.ClaimName(a.Project, a.Name)
	moveTestClaim(t, source, oldClaim)
	h := withMoveTestHooks(t, &moveTestHooks{})

	var log strings.Builder
	a, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.moveAgentRun(ctx, &log, a, target.state.Name); err != nil {
		t.Fatalf("moveAgentRun() = %v\n%s", err, log.String())
	}

	row, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if row.Cluster != "k3s" || row.Runtime != state.RuntimeKubernetes {
		t.Errorf("the row is on cluster %q, runtime %q; want k3s, kubernetes", row.Cluster, row.Runtime)
	}
	if row.Claim == "" || !strings.HasPrefix(row.Claim, oldClaim+"-r") {
		t.Errorf("claim = %q, want a new one named from %q", row.Claim, oldClaim)
	}
	if row.Image != "" {
		t.Errorf("image = %q, want the target cluster's own, not the source's pin", row.Image)
	}
	if row.DesiredState != state.DesiredRunning {
		t.Errorf("desired state = %q, want running", row.DesiredState)
	}
	if row.Pod != "" || row.PodUID != "" {
		t.Errorf("the row still names pod %q (%s), which is the source's", row.Pod, row.PodUID)
	}
	if h.checkpoints != 1 {
		t.Errorf("checkpoints = %d, want 1", h.checkpoints)
	}
	if h.archivedAs != oldClaim {
		t.Errorf("archived claim = %q, want the one the row had, %q", h.archivedAs, oldClaim)
	}
	if h.restoredAs != row.Claim {
		t.Errorf("restored claim = %q, want the row's new one, %q", h.restoredAs, row.Claim)
	}
	if h.restoredImg != "agentbox-agent:dev" {
		t.Errorf("restored with image %q, want the target cluster's, not the pin", h.restoredImg)
	}
	if !h.adopted {
		t.Error("the move never adopted the target pod")
	}
	if moveTestHasClaim(source, oldClaim) {
		t.Errorf("the source still has claim %s", oldClaim)
	}
	for _, want := range []string{"checkpointed pawly/agent-01 at commit-1", "archived", "restored it as", "adopted pawly/agent-01", "removed pawly/agent-01's pod and claim"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the log is missing %q:\n%s", want, log.String())
		}
	}
}

// TestAFailedTargetKeepsTheSourceRowAndRestartsIt: a restore that fails leaves
// the agent where it was, with the image it had, its desired state, and its pod
// asked for again. The claim the move made on the target is not kept.
func TestAFailedTargetKeepsTheSourceRowAndRestartsIt(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	a := moveTestAgent(t, srv, state.DesiredRunning)
	if err := srv.store.SetAgentPod(ctx, a.Project, a.Name, "ab-pawly-agent-01", "uid-source", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetAgentClaim(ctx, a.Project, a.Name, "", "agentbox-agent:pinned"); err != nil {
		t.Fatal(err)
	}
	source, target := moveTestClusters(t, srv)
	h := &moveTestHooks{}
	h.onRestore = func(c *k8s.Cluster, spec k8s.Spec, r io.Reader) error {
		_, _ = io.Copy(io.Discard, r)
		h.restoredAs = spec.WorkspaceClaim()
		// The real restore makes the claim before unpacking into it; this one
		// fails with the claim there, which is what the rollback has to remove.
		claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: spec.WorkspaceClaim(), Namespace: c.Namespace()}}
		if _, err := c.Client().CoreV1().PersistentVolumeClaims(c.Namespace()).Create(context.Background(), claim, metav1.CreateOptions{}); err != nil {
			return err
		}
		return errors.New("the target's registry refused the image")
	}
	withMoveTestHooks(t, h)

	var log strings.Builder
	a, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	err = srv.moveAgentRun(ctx, &log, a, target.state.Name)
	if err == nil || !strings.Contains(err.Error(), "registry refused") {
		t.Fatalf("moveAgentRun() = %v, want the target's refusal", err)
	}
	if h.adopted {
		t.Error("a move whose restore failed adopted the target pod")
	}
	if h.restarts != 1 {
		t.Errorf("restarts = %d, want the source asked for again once", h.restarts)
	}

	row, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if row.Cluster != source.state.Name || row.Claim != "" || row.Image != "agentbox-agent:pinned" {
		t.Errorf("the row is cluster %q, claim %q, image %q; want the source's, untouched", row.Cluster, row.Claim, row.Image)
	}
	if row.DesiredState != state.DesiredRunning || row.Pod != "ab-pawly-agent-01" {
		t.Errorf("desired state %q, pod %q; want running and the source pod", row.DesiredState, row.Pod)
	}
	if moveTestHasClaim(target, h.restoredAs) {
		t.Errorf("the failed move kept claim %s on the target", h.restoredAs)
	}
}

// TestAFailedAdoptionRollsTheRowBack: once the row names the target, a target
// whose pod never answers puts the row back on the source, with the source's
// image and claim, and removes the target pod and claim.
func TestAFailedAdoptionRollsTheRowBack(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	a := moveTestAgent(t, srv, state.DesiredRunning)
	if err := srv.store.SetAgentPod(ctx, a.Project, a.Name, "ab-pawly-agent-01", "uid-source", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetAgentClaim(ctx, a.Project, a.Name, "", "agentbox-agent:pinned"); err != nil {
		t.Fatal(err)
	}
	source, target := moveTestClusters(t, srv)
	h := withMoveTestHooks(t, &moveTestHooks{
		onRestore: func(c *k8s.Cluster, spec k8s.Spec, r io.Reader) error {
			_, _ = io.Copy(io.Discard, r)
			claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: spec.WorkspaceClaim(), Namespace: c.Namespace()}}
			_, err := c.Client().CoreV1().PersistentVolumeClaims(c.Namespace()).Create(context.Background(), claim, metav1.CreateOptions{})
			return err
		},
		onAdopt: func(srv *Server) error {
			// The reconciler made the target pod before the target stopped
			// answering.
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ab-pawly-agent-01", Namespace: "agentbox"}}
			if _, err := target.fake.CoreV1().Pods("agentbox").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
				return err
			}
			return errors.New("its agentd never connected")
		},
	})

	var log strings.Builder
	a, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	err = srv.moveAgentRun(ctx, &log, a, target.state.Name)
	if err == nil || !strings.Contains(err.Error(), "back in cluster k3d on claim") {
		t.Fatalf("moveAgentRun() = %v, want a rollback that names the source", err)
	}
	if h.restarts != 1 {
		t.Errorf("restarts = %d, want the source asked for again once", h.restarts)
	}

	row, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if row.Cluster != source.state.Name || row.Claim != "" || row.Image != "agentbox-agent:pinned" {
		t.Errorf("the row is cluster %q, claim %q, image %q; want the source's, untouched", row.Cluster, row.Claim, row.Image)
	}
	if row.DesiredState != state.DesiredRunning {
		t.Errorf("desired state = %q, want running", row.DesiredState)
	}
	if _, err := target.client.Get(ctx, "pawly", "agent-01"); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("the target pod is still there after the rollback: %v", err)
	}
	if moveTestHasClaim(target, h.restoredAs) {
		t.Error("the target kept the claim the move made")
	}
}

// TestMoveOfAStoppedAgentSkipsTheCheckpoint: a stopped agent has no pod to run
// git in, and its worktree is on the claim the archive carries. It moves, and
// starts on the target.
func TestMoveOfAStoppedAgentSkipsTheCheckpoint(t *testing.T) {
	srv := reconcilerServer(t)
	ctx := context.Background()
	a := moveTestAgent(t, srv, state.DesiredStopped)
	source, target := moveTestClusters(t, srv)
	moveTestClaim(t, source, k8s.ClaimName(a.Project, a.Name))
	h := withMoveTestHooks(t, &moveTestHooks{})

	var log strings.Builder
	a, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.moveAgentRun(ctx, &log, a, target.state.Name); err != nil {
		t.Fatalf("moveAgentRun() = %v\n%s", err, log.String())
	}
	if h.checkpoints != 0 {
		t.Errorf("checkpoints = %d, want none for a stopped agent", h.checkpoints)
	}
	if !strings.Contains(log.String(), "no running pod") {
		t.Errorf("the log doesn't say why there was no checkpoint:\n%s", log.String())
	}
	row, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if row.Cluster != target.state.Name || row.DesiredState != state.DesiredRunning {
		t.Errorf("the row is cluster %q, desired %q; want k3s and running", row.Cluster, row.DesiredState)
	}
}

// TestTransferClaimStreamsAndCountsWhatPassed: the archive goes to the restore
// through a pipe, and the count is what the move logs.
func TestTransferClaimStreamsAndCountsWhatPassed(t *testing.T) {
	source := moveTestCluster{fake: podClient()}
	source.client = k8s.FromClient("k3d", "agentbox", source.fake)
	target := moveTestCluster{fake: podClient()}
	target.client = k8s.FromClient("k3s", "agentbox", target.fake)
	withMoveTestHooks(t, &moveTestHooks{})

	spec := k8s.Spec{Cluster: "k3d", Project: "pawly", Agent: "agent-01", Claim: "a-claim"}
	n, err := transferClaim(context.Background(), source.client, spec, target.client, spec)
	if err != nil {
		t.Fatalf("transferClaim() = %v", err)
	}
	if n != int64(len("a tarball")) {
		t.Errorf("counted %d bytes, want %d", n, len("a tarball"))
	}
}

// TestTransferClaimStopsTheArchiveWhenTheRestoreFails: a target that refuses
// the stream doesn't leave the source's helper writing to nobody.
func TestTransferClaimStopsTheArchiveWhenTheRestoreFails(t *testing.T) {
	source := moveTestCluster{fake: podClient()}
	source.client = k8s.FromClient("k3d", "agentbox", source.fake)
	target := moveTestCluster{fake: podClient()}
	target.client = k8s.FromClient("k3s", "agentbox", target.fake)
	withMoveTestHooks(t, &moveTestHooks{
		onRestore: func(c *k8s.Cluster, spec k8s.Spec, r io.Reader) error {
			return errors.New("no room for the helper")
		},
	})

	spec := k8s.Spec{Cluster: "k3d", Project: "pawly", Agent: "agent-01", Claim: "a-claim"}
	_, err := transferClaim(context.Background(), source.client, spec, target.client, spec)
	if err == nil || !strings.Contains(err.Error(), "no room for the helper") {
		t.Fatalf("transferClaim() = %v, want the target's error", err)
	}
}
