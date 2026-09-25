package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/hubtest"
)

// TestRemoteThroughAHub exercises this machine's side of a hub end to end: the
// daemon connects as an environment, and a client reaches it back through the
// tunnel. The hub is hubtest's stand-in — the real one is a separate program,
// so what this proves is that AgentBox speaks the protocol in agentbox/hubapi,
// not that any particular hub implements it correctly.
func TestRemoteThroughAHub(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ctx := context.Background()
	h := hubtest.New(t)
	session, err := api.HubClient{URL: h.URL}.Login(ctx, api.HubLoginRequest{Email: "owner@example.com", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	hc := api.HubClient{URL: h.URL, Token: session.Token}
	created, err := hc.CreateEnvironment(ctx, "vps")
	if err != nil {
		t.Fatal(err)
	}

	d := startTestDaemon(t, root, fakeIncus)
	if st, err := d.client.RemoteStatus(ctx); err != nil || st.Configured {
		t.Fatalf("before connecting: %+v, %v", st, err)
	}
	if _, err := d.client.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: h.URL, Token: "not-a-token"}); err == nil {
		t.Error("a malformed token was accepted")
	}
	st, err := d.client.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: h.URL + "/", Token: created.Token})
	if err != nil || !st.Connected || st.Hub != h.URL {
		t.Fatalf("ConnectRemote = %+v, %v", st, err)
	}
	if info, err := os.Stat(filepath.Join(d.paths.Config, "remote.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("remote.json: %v, %v", info, err)
	}

	remote := hc.Environment(created.Environment.ID)
	if v, err := remote.Version(ctx); err != nil || v.Version != Version {
		t.Errorf("the version through the hub = %+v, %v", v, err)
	}
	if projects, err := remote.Projects(ctx); err != nil || len(projects) != 0 {
		t.Errorf("projects through the hub = %v, %v", projects, err)
	}
	if err := remote.Shutdown(ctx); err == nil || !strings.Contains(err.Error(), "works only on the machine itself") {
		t.Errorf("stopping the daemon through the hub: %v", err)
	}
	if _, err := remote.ConnectRemote(ctx, api.RemoteConnectRequest{Hub: "https://other.example", Token: created.Token}); err == nil {
		t.Error("moved the environment to another hub, through the hub")
	}
	if err := d.client.Ping(ctx); err != nil {
		t.Errorf("the daemon stopped: %v", err)
	}
	if envs, err := hc.Environments(ctx); err != nil || len(envs) != 1 || !envs[0].Online || envs[0].Version != Version {
		t.Errorf("environments = %+v, %v", envs, err)
	}

	if err := d.client.DisconnectRemote(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hub to show vps offline", func() bool {
		envs, err := hc.Environments(ctx)
		return err == nil && len(envs) == 1 && !envs[0].Online
	})
	if st, _ := d.client.RemoteStatus(ctx); st.Configured {
		t.Errorf("after disconnecting: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(d.paths.Config, "remote.json")); !os.IsNotExist(err) {
		t.Errorf("remote.json after disconnecting: %v", err)
	}
}
