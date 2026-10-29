package daemon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"agentbox/internal/agent"
	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// lifecycleServer is a cluster agent on a fake cluster, with its pod made by a
// reconciler round as it would be.
func lifecycleServer(t *testing.T) (*Server, state.Agent, *k8s.Cluster) {
	t.Helper()
	srv, a := snapshotServer(t, state.DesiredRunning)
	kc := clusterWithFakeClient(t, srv, reconcilerCluster(), podClient())
	if err := reconcileRound(t, srv, reconcilerCluster(), kc); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(context.Background(), a.Project, a.Name); err != nil {
		t.Fatalf("the round made no pod: %v", err)
	}
	return srv, a, kc
}

// reconcileUntilGone runs rounds in the background, the way the daemon's loop
// would while a snapshot or a restore waits for the pod to go.
func reconcileUntilGone(t *testing.T, srv *Server, kc *k8s.Cluster) func() {
	t.Helper()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			pods, _ := kc.List(context.Background())
			agents, _ := srv.store.AgentsOnCluster(context.Background(), "k3d")
			_ = srv.reconcileAgents(context.Background(), reconcilerCluster(), kc, pods, agents)
		}
	}()
	return func() { close(done); <-stopped }
}

// B1: the row hasn't adopted the pod a just-started agent has, so it reads as
// not running. Whether the claim is free is the cluster's to say.
func TestStopForClaimStopsAPodTheRowHasNotAdopted(t *testing.T) {
	srv, a, kc := lifecycleServer(t)
	ctx := context.Background()
	if err := srv.store.SetAgentPod(ctx, a.Project, a.Name, "", "", ""); err != nil {
		t.Fatal(err)
	}
	a, _ = srv.store.Agent(ctx, a.Project, a.Name)
	if podInfo(a).Running() {
		t.Fatal("the setup is wrong: the row reads as running")
	}
	stop := reconcileUntilGone(t, srv, kc)
	defer stop()

	wasRunning, err := (podLifecycle{s: srv}).stopForClaim(ctx, kc, a, "a test")
	if err != nil {
		t.Fatalf("stopForClaim() = %v", err)
	}
	if !wasRunning {
		t.Error("wasRunning = false for an agent that asked for a pod: it would never be started again")
	}
	if _, err := kc.Get(ctx, a.Project, a.Name); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("the pod is still there after stopForClaim: %v", err)
	}
}

// B1: a stopped row whose pod the reconciler hasn't deleted yet still has the
// claim mounted.
func TestStopForClaimWaitsForAPodStillUp(t *testing.T) {
	srv, a, kc := lifecycleServer(t)
	ctx := context.Background()
	if err := srv.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	a, _ = srv.store.Agent(ctx, a.Project, a.Name)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = kc.Delete(context.Background(), a.Project, a.Name)
	}()
	wasRunning, err := (podLifecycle{s: srv}).stopForClaim(ctx, kc, a, "a test")
	if err != nil {
		t.Fatalf("stopForClaim() = %v", err)
	}
	if wasRunning {
		t.Error("wasRunning = true for a stopped agent: a snapshot would start it")
	}
	if _, err := kc.Get(ctx, a.Project, a.Name); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("stopForClaim returned with the pod still there: %v", err)
	}
}

// B2: a stop, a destroy or a second restore while a restore runs are refused,
// and the restore's own calls go through.
func TestARestoreHoldsTheAgent(t *testing.T) {
	srv, a, _ := lifecycleServer(t)
	ctx := context.Background()
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	p := podLifecycle{s: srv}
	srv.jobs = newJobs(ctx, srv.store, srv.events)

	release := make(chan struct{})
	inside := make(chan error, 1)
	w := httptest.NewRecorder()
	err := srv.startAgentJob(ctx, w, "restore", a, func(ctx context.Context, _ io.Writer) (any, error) {
		// The holder's own stop, as Manager.Restore's pod would make it.
		inside <- p.Stop(ctx, ref)
		<-release
		return nil, nil
	})
	if err != nil {
		t.Fatalf("the first restore = %v", err)
	}
	if err := <-inside; err != nil {
		t.Errorf("the restore's own stop = %v, want it let through", err)
	}

	// A second restore: refused before a job is made.
	second := httptest.NewRecorder()
	err = srv.startAgentJob(ctx, second, "restore", a, func(context.Context, io.Writer) (any, error) {
		t.Error("a second restore ran beside the first")
		return nil, nil
	})
	if !errors.Is(err, ErrAgentBusy) {
		t.Errorf("a second restore = %v, want ErrAgentBusy", err)
	}
	rec := httptest.NewRecorder()
	writeError(rec, err)
	if rec.Code != http.StatusConflict {
		t.Errorf("a busy agent answers %d, want 409", rec.Code)
	}

	// A stop from anywhere else: the idle sweep, the app.
	if err := p.Stop(ctx, ref); !errors.Is(err, ErrAgentBusy) {
		t.Errorf("a stop mid-restore = %v, want ErrAgentBusy", err)
	}
	// A destroy.
	if err := srv.destroyAgentNow(ctx, srv.manager(io.Discard), a, agent.DestroyOptions{}); !errors.Is(err, ErrAgentBusy) {
		t.Errorf("a destroy mid-restore = %v, want ErrAgentBusy", err)
	}
	if got, _ := srv.store.Agent(ctx, a.Project, a.Name); got.DesiredState == state.DesiredDestroyed {
		t.Error("the refused destroy marked the row destroyed")
	}

	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for p.Stop(ctx, ref) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the agent is still held after its restore ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// B2: a destroy that lands anyway (marked by another path) is not undone by a
// snapshot's or restore's restart.
func TestARestartAfterADestroyLeavesItDestroyed(t *testing.T) {
	srv, a, _ := lifecycleServer(t)
	ctx := context.Background()
	if err := srv.store.MarkAgentDestroyed(ctx, a.Project, a.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := (podLifecycle{s: srv}).restart(ctx, a); !errors.Is(err, state.ErrDestroyed) {
		t.Errorf("restart() = %v, want ErrDestroyed", err)
	}
	if _, err := (podLifecycle{s: srv}).stopForClaim(ctx, nil, a, "a test"); !errors.Is(err, state.ErrDestroyed) {
		t.Errorf("stopForClaim() of a destroyed agent = %v, want ErrDestroyed", err)
	}
	got, err := srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.DesiredState != state.DesiredDestroyed {
		t.Errorf("desired = %q, want destroyed", got.DesiredState)
	}
}

// B2: a snapshot holds the agent against a stop too, and a host agent is never
// held.
func TestASnapshotHoldsTheAgentAndAHostAgentIsNot(t *testing.T) {
	srv, a, _ := lifecycleServer(t)
	ctx := context.Background()
	held, release, err := srv.holdAgent(ctx, a, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	if err := (podLifecycle{s: srv}).Stop(ctx, ref); !errors.Is(err, ErrAgentBusy) {
		t.Errorf("a stop mid-snapshot = %v, want ErrAgentBusy", err)
	}
	if _, again, err := srv.holdAgent(held, a, "snapshot"); err != nil {
		t.Errorf("the holder taking it again = %v, want it reentrant", err)
	} else {
		again()
		if err := (podLifecycle{s: srv}).Stop(ctx, ref); !errors.Is(err, ErrAgentBusy) {
			t.Error("a reentrant release let go of the outer hold")
		}
	}
	release()
	release()
	if err := (podLifecycle{s: srv}).Stop(ctx, ref); err != nil {
		t.Errorf("a stop after the snapshot = %v", err)
	}

	host := state.Agent{Project: "pawly", Name: "host-01"}
	_, r1, err := srv.holdAgent(ctx, host, "restore")
	if err != nil {
		t.Fatal(err)
	}
	defer r1()
	if _, r2, err := srv.holdAgent(ctx, host, "destroy"); err != nil {
		t.Errorf("a host agent was held: %v", err)
	} else {
		r2()
	}
}

// M1: a create that failed after its claim was made, with its pod already gone,
// leaves neither the claim nor the row.
func TestReconcilerFinishesAFailedCreate(t *testing.T) {
	srv, a, kc := lifecycleServer(t)
	ctx := context.Background()
	if err := srv.store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentCreating); err != nil {
		t.Fatal(err)
	}
	if err := kc.Delete(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkAgentDestroyed(ctx, a.Project, a.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, reconcilerCluster(), kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	if _, err := kc.Client().CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, k8s.ClaimName(a.Project, a.Name), metav1.GetOptions{}); err == nil {
		t.Error("the failed create's claim is still there")
	}
	if _, err := srv.store.Agent(ctx, a.Project, a.Name); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the failed create's row is still there: %v", err)
	}
}

// M1: a destroy in progress, or one that failed, keeps its row for the destroy
// to archive, and still loses its claim.
func TestReconcilerKeepsADestroyedRowForItsDestroy(t *testing.T) {
	srv, a, kc := lifecycleServer(t)
	ctx := context.Background()
	if err := kc.Delete(ctx, a.Project, a.Name); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkAgentDestroyed(ctx, a.Project, a.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, reconcilerCluster(), kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	if _, err := kc.Client().CoreV1().PersistentVolumeClaims("agentbox").Get(ctx, k8s.ClaimName(a.Project, a.Name), metav1.GetOptions{}); err == nil {
		t.Error("the destroyed agent's claim is still there")
	}
	if _, err := srv.store.Agent(ctx, a.Project, a.Name); err != nil {
		t.Errorf("the row of a destroy in progress went: %v", err)
	}
}

// M8: a round whose row is stale (the agent moved since it was read) makes no
// pod and leaves the live pod's token alone; a pod it made anyway is deleted.
func TestReconcilerActsOnTheRowAsItIsNow(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredRunning)
	ctx := context.Background()
	kc := clusterWithFakeClient(t, srv, reconcilerCluster(), podClient())
	stale, err := srv.store.AgentsOnCluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MoveAgent(ctx, a.Project, a.Name, state.MovePlacement{Cluster: "eks", Workspace: a.Workspace, Claim: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetAgentToken(ctx, a.Project, a.Name, "live-token-0123456789abcdef0123456789abcdef", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := srv.reconcileAgents(ctx, reconcilerCluster(), kc, nil, stale); err != nil {
		t.Fatalf("the round = %v", err)
	}
	if _, err := kc.Get(ctx, a.Project, a.Name); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("a stale round made a pod for an agent in another cluster: %v", err)
	}
	if p, n, err := srv.store.AgentForToken(ctx, "live-token-0123456789abcdef0123456789abcdef"); err != nil || p != a.Project || n != a.Name {
		t.Errorf("the live pod's token = %s/%s (%v), want it kept", p, n, err)
	}

	// A pod a round made before the move: deleted, as its row is elsewhere.
	spec := srv.podSpec(ctx, reconcilerCluster(), nil, a)
	if _, err := kc.Create(ctx, spec, "stray-token-0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := reconcileRound(t, srv, reconcilerCluster(), kc); err != nil {
		t.Fatalf("the round = %v", err)
	}
	if _, err := kc.Get(ctx, a.Project, a.Name); !errors.Is(err, k8s.ErrNotFound) {
		t.Errorf("the stray pod is still there: %v", err)
	}
}
