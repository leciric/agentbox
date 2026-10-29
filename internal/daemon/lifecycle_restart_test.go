package daemon

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/credentials"
	"agentbox/internal/gateway"
	"agentbox/internal/state"
)

// podRecorder is a pod that records what the master asked it to write and run,
// and answers every stream with a zero exit.
type podRecorder struct {
	mu     sync.Mutex
	writes []string
	execs  []string
}

func (r *podRecorder) handle(_ context.Context, h gateway.Header, s *gateway.Stream) error {
	switch h.Op {
	case gateway.OpWrite:
		if _, err := io.Copy(io.Discard, gateway.NewFrameReader(s, gateway.FrameData)); err != nil {
			return err
		}
		r.mu.Lock()
		r.writes = append(r.writes, h.Path)
		r.mu.Unlock()
	case gateway.OpExec:
		if _, err := io.Copy(io.Discard, gateway.NewFrameReader(s, gateway.FrameStdin)); err != nil {
			return err
		}
		r.mu.Lock()
		r.execs = append(r.execs, strings.Join(h.Argv, " "))
		r.mu.Unlock()
	}
	return s.Exit(0)
}

func (r *podRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes, r.execs = nil, nil
}

func (r *podRecorder) sawWrite(suffix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, path := range r.writes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func (r *podRecorder) sawExec(part string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, command := range r.execs {
		if strings.Contains(command, part) {
			return true
		}
	}
	return false
}

// startPodFor connects a pod for ref with the recorder's handler and waits for
// the daemon to have its session.
func startPodFor(t *testing.T, d testDaemon, ref gateway.Ref, token string, rec *podRecorder) {
	t.Helper()
	hs := httptest.NewServer(d.srv.routes())
	t.Cleanup(hs.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pod := &gateway.Client{URL: hs.URL, Token: token, Handle: rec.handle}
	go func() { _ = pod.Run(ctx) }()
	if err := d.srv.gateway.WaitFor(ctx, ref, 10*time.Second); err != nil {
		t.Fatalf("the pod never connected: %v", err)
	}
}

// restartTestAgent is a running cluster agent whose project has the git
// profile a pod needs, connected to a recorder pod.
func restartTestAgent(t *testing.T, desired string) (testDaemon, state.Agent, *podRecorder) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	addPodProject(t, d)
	if err := d.srv.store.SetProjectGitRemote(ctx, state.GitRemote{
		Project: "demo", Profile: state.GitProfilePrivate,
		URL: "https://hub.example/demo.git", Auth: state.GitAuthToken, Credential: "private",
	}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SetProjectGitCredential(ctx, "demo", "private", credentials.GitCredentialToken, "pod-token"); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.AddAgent(ctx, state.Agent{
		Project: "demo", Name: "agent-01", Cluster: "e2e", AI: "none",
		Branch: "agentbox/agent-01", BaseRef: "main", Pod: "demo-agent-01",
		DesiredState: desired, Status: state.AgentReady, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetAgentToken(ctx, "demo", "agent-01", testPodToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	a, err := d.srv.store.Agent(ctx, "demo", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	rec := &podRecorder{}
	startPodFor(t, d, testPodRef, testPodToken, rec)
	return d, a, rec
}

// M5: adopting the pod a move made starts the agent on it, so the new pod gets
// the env, secrets, git config, brief and tmux session a bare row doesn't.
func TestMoveAdoptionConfiguresTheAgentOnItsNewPod(t *testing.T) {
	d, a, rec := restartTestAgent(t, state.DesiredRunning)
	ctx := context.Background()
	rec.reset()

	if err := moveAdopt(ctx, io.Discard, d.srv, a); err != nil {
		t.Fatalf("moveAdopt() = %v", err)
	}
	assertConfigured(t, rec, a)
}

// M5: the restart a failed move rolls back to is the same Start, so the source
// pod comes back configured rather than as a bare machine on the old claim.
func TestARestartAfterAFailedMoveConfiguresTheSourcePod(t *testing.T) {
	d, a, rec := restartTestAgent(t, state.DesiredStopped)
	ctx := context.Background()
	rec.reset()

	if err := moveRestart(ctx, d.srv, a); err != nil {
		t.Fatalf("moveRestart() = %v", err)
	}
	assertConfigured(t, rec, a)
}

// M5: the restart a snapshot or a failed restore does puts the pod back through
// Manager.Start, not just the row's desired state, so its configuration and
// tmux session are written into the new pod.
func TestRestartingAfterASnapshotConfiguresTheNewPod(t *testing.T) {
	d, a, rec := restartTestAgent(t, state.DesiredStopped)
	ctx := context.Background()
	rec.reset()

	if err := (podLifecycle{s: d.srv}).restart(ctx, a); err != nil {
		t.Fatalf("restart() = %v", err)
	}
	row, err := d.srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if row.DesiredState != state.DesiredRunning {
		t.Errorf("desired state = %q, want running", row.DesiredState)
	}
	assertConfigured(t, rec, a)
}

func assertConfigured(t *testing.T, rec *podRecorder, a state.Agent) {
	t.Helper()
	for _, path := range []string{"/.config/agentbox/env", "/.config/agentbox/secrets.env", "/.gitconfig", "/AGENTBOX.md"} {
		if !rec.sawWrite(path) {
			t.Errorf("nothing was written to %s after starting %s", path, a.Ref())
		}
	}
	if !rec.sawExec("tmux new-session") {
		t.Errorf("the tmux session of %s was never started", a.Ref())
	}
}
