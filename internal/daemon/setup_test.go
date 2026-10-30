package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// unusableIncus is an incus that is installed but won't answer: what a machine
// looks like before host setup, or to a process that may not open the socket.
const unusableIncus = `echo "Error: Get \"http://unix.socket/1.0\": dial unix /var/lib/incus/unix.socket: connect: permission denied" >&2
exit 1
`

// noIncusBin is a command that isn't on $PATH, which is what makes Ping answer
// exec.ErrNotFound: exec looks a bare name up in $PATH, while a path that
// doesn't exist fails with ENOENT and would read as a different problem.
const noIncusBin = "agentbox-test-no-such-incus"

// noIncusDaemon is a daemon on a machine with no incus binary at all: the
// control plane packaging/daemon/Dockerfile makes, with no Incus and no Docker
// in it.
func noIncusDaemon(t *testing.T, root string) testDaemon {
	t.Helper()
	return startTestDaemon(t, root, "exit 0\n", testConfig{setup: func(s *Server) {
		s.cfg.Incus.Bin = noIncusBin
	}})
}

// testCluster records a cluster in a test daemon's store, as `cluster add`
// leaves one: reached a moment ago, with an image to run and the master URL
// its pods dial.
func testCluster(t *testing.T, srv *Server, name string) state.Cluster {
	t.Helper()
	cluster := state.Cluster{
		Name: name, Namespace: "agentbox", Image: "agentbox-agent:dev",
		MasterURL: "https://master.example.test", Tailnet: state.TailnetNone,
		MaxAgents: 4, Kubeconfig: []byte("sealed-for-a-test"),
		CreatedAt: time.Now(), LastSeen: time.Now(),
	}
	if err := srv.store.AddCluster(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}
	return cluster
}

// TestSetupOnAControlPlane: a daemon in a cluster with no Incus on its machine
// has no host to set up, so Setup leaves those checks out rather than holding
// the installation up over something it cannot have, and its clusters are what
// Ready follows.
func TestSetupOnAControlPlane(t *testing.T) {
	t.Parallel()
	d := noIncusDaemon(t, t.TempDir())
	ctx := context.Background()
	cluster := testCluster(t, d.srv, "k3d")

	status, err := d.client.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{api.SetupBackendCluster}; !slices.Equal(status.Backends, want) {
		t.Errorf("Backends = %v, want %v", status.Backends, want)
	}
	for _, id := range []string{"incus", "host", "storage", "image"} {
		for _, c := range status.Checks {
			if c.ID == id {
				t.Errorf("a control plane reported the %q check, which it could never pass: %+v", id, c)
			}
		}
	}
	if c := setupCheck(t, d, "cluster/k3d"); c.Status != api.SetupOK || !c.Required ||
		c.Backend != api.SetupBackendCluster || c.Cluster != "k3d" {
		t.Errorf("cluster check = %+v, want an ok, required check of k3d", c)
	}
	if !status.Ready {
		t.Errorf("Ready = false with a cluster that can start an agent: %+v", status.Checks)
	}
	// The image manifest is still what the app shows before a build, even
	// though this installation builds no image of its own.
	if status.Image.Version != image.Version || status.Image.Hint == "" || len(status.Image.Downloads) != len(image.Downloads) {
		t.Errorf("Image = %+v, want the version, hint and downloads", status.Image)
	}

	// The image is what a pod runs, and a cluster without one can't start an
	// agent: it is missing, and it holds the installation up.
	cluster.Image = ""
	if err := d.srv.store.SetClusterConfig(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	status, err = d.client.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c := setupCheck(t, d, "cluster/k3d"); c.Status != api.SetupMissing ||
		!strings.Contains(c.Detail, "no agent image") || !strings.Contains(c.Fix, "--image") {
		t.Errorf("cluster check without an image = %+v, want SetupMissing with the command that sets one", c)
	}
	if status.Ready {
		t.Errorf("Ready = true with a cluster that has no image")
	}

	// And the master URL is the other half of a pod that can start: no pod of
	// this cluster reaches the daemon without it.
	cluster.Image = "agentbox-agent:dev"
	cluster.MasterURL = ""
	if err := d.srv.store.SetClusterConfig(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	if c := setupCheck(t, d, "cluster/k3d"); c.Status != api.SetupMissing ||
		!strings.Contains(c.Detail, "master URL") || !strings.Contains(c.Fix, "--master-url") {
		t.Errorf("cluster check without a master URL = %+v, want SetupMissing with the command that sets one", c)
	}
	if status, err := d.client.Setup(ctx); err != nil {
		t.Fatal(err)
	} else if status.Ready {
		t.Errorf("Ready = true with a cluster whose pods can't reach the daemon")
	}
}

// TestSetupWithoutIncusOrClustersIsStillTheHost: a machine with no Incus and
// nothing else either is a first run, not a control plane, so Setup still asks
// for host setup: only a cluster makes the host something this daemon can do
// without.
func TestSetupWithoutIncusOrClustersIsStillTheHost(t *testing.T) {
	t.Parallel()
	d := noIncusDaemon(t, t.TempDir())
	status, err := d.client.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{api.SetupBackendHost}; !slices.Equal(status.Backends, want) {
		t.Errorf("Backends = %v, want %v", status.Backends, want)
	}
	if c := setupCheck(t, d, "incus"); c.Status != api.SetupMissing || !c.Required || !strings.Contains(c.Detail, "Incus isn't installed") {
		t.Errorf("incus check = %+v, want a required SetupMissing saying Incus isn't installed", c)
	}
}

// TestSetupReportsClustersBesideTheHost: a host that also has clusters keeps
// every host check it had, and gains one check per cluster after them.
func TestSetupReportsClustersBesideTheHost(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{}))
	testCluster(t, d.srv, "k3d")

	status, err := d.client.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{api.SetupBackendHost, api.SetupBackendCluster}; !slices.Equal(status.Backends, want) {
		t.Errorf("Backends = %v, want %v", status.Backends, want)
	}
	for _, id := range []string{"incus", "host", "image"} {
		if c := setupCheck(t, d, id); !c.Required {
			t.Errorf("%q check = %+v, want it still required on a host that runs agents", id, c)
		}
	}
	// No project names it, so it is worth reporting and not worth blocking on.
	if c := setupCheck(t, d, "cluster/k3d"); c.Required || c.Status != api.SetupOK || c.Backend != api.SetupBackendCluster {
		t.Errorf("cluster check = %+v, want an ok check no project makes required", c)
	}

	// A project that runs its agents there is what makes it required: an agent
	// of that project can't be created until the cluster can take one.
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetProjectCluster(ctx, "pawly", "k3d"); err != nil {
		t.Fatal(err)
	}
	if c := setupCheck(t, d, "cluster/k3d"); !c.Required {
		t.Errorf("cluster check = %+v, want it required while a project runs its agents there", c)
	}
}

func setupCheck(t *testing.T, d testDaemon, id string) api.SetupCheck {
	t.Helper()
	status, err := d.client.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range status.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", id, status.Checks)
	return api.SetupCheck{}
}

// joinedGroupFile writes a group file that puts user in group, with a GID no
// process of this test has: joined, but not in this session.
func joinedGroupFile(t *testing.T, user string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "group")
	if err := os.WriteFile(path, []byte("root:x:0:\nincus-admin:x:424242:"+user+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSetupAdvisesLoggingInOnlyWhenTheACLDidNotTake. Host setup grants the
// Incus socket to this user by ACL, which processes already running get, so
// joining incus-admin is the fallback rather than the route. Telling someone to
// log out when the socket is already theirs would be advice that fixes nothing.
func TestSetupAdvisesLoggingInOnlyWhenTheACLDidNotTake(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "unix.socket")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	groups := joinedGroupFile(t, "dev")
	old := groupFile
	groupFile = groups
	t.Cleanup(func() { groupFile = old })

	// No ACL: the socket isn't this process's to open, and the group is all
	// that host setup managed. A new login is what's left.
	t.Setenv("INCUS_SOCKET", filepath.Join(root, "no-socket-here"))
	d := startTestDaemon(t, root, unusableIncus)
	check := setupCheck(t, d, "incus")
	if !strings.Contains(check.Detail, "log out and back in") || check.Fix != "log out and back in" {
		t.Errorf("with no ACL, the Incus check = %+v; want it to say to log out and back in", check)
	}

	// With the ACL, the socket is open to this process. Incus still failing is
	// something else, and its own message is what helps.
	t.Setenv("INCUS_SOCKET", socket)
	check = setupCheck(t, d, "incus")
	if strings.Contains(check.Detail, "log out") {
		t.Errorf("with the socket already open, the Incus check = %+v; want no advice to log out", check)
	}
	if !strings.Contains(check.Detail, "permission denied") {
		t.Errorf("the Incus check = %+v; want incus' own message", check)
	}
}

// storagePoolIncus stands in for incus answering the "default" pool's own
// query (not its /resources, which PoolSpace reads) with the given driver.
func storagePoolIncus(driver string) string {
	return `case "$1" in
  query)
    case "$2" in
      /1.0/storage-pools/default) echo '{"driver": "` + driver + `"}' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0
`
}

// TestSetupStorageIsOKOnBtrfsOrZFS: those drivers give agentbox create an
// instant copy-on-write snapshot, so there's nothing to warn about.
func TestSetupStorageIsOKOnBtrfsOrZFS(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"btrfs", "zfs"} {
		d := startTestDaemon(t, t.TempDir(), storagePoolIncus(driver))
		check := setupCheck(t, d, "storage")
		if check.Status != api.SetupOK {
			t.Errorf("storage check on %s = %+v, want SetupOK", driver, check)
		}
	}
}

// TestSetupStorageWarnsWhenNotBtrfsOrZFS: a dir (or any other) driver still
// works, but agentbox create copies the base image's files instead of taking
// an instant snapshot, so it's slow — worth a warning, never a blocker.
func TestSetupStorageWarnsWhenNotBtrfsOrZFS(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), storagePoolIncus("dir"))
	check := setupCheck(t, d, "storage")
	if check.Status != api.SetupWarn || !strings.Contains(check.Detail, `"dir"`) {
		t.Errorf(`storage check = %+v, want SetupWarn mentioning "dir"`, check)
	}
}

// TestSetupStorageWarnsWhenTheDriverCantBeRead: the pool query itself failing
// is also worth a caveat, not a reason to hold up the wizard.
func TestSetupStorageWarnsWhenTheDriverCantBeRead(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), `case "$1" in
  query)
    case "$2" in
      /1.0/storage-pools/default) echo "Error: pool not found" >&2; exit 1 ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0
`)
	check := setupCheck(t, d, "storage")
	if check.Status != api.SetupWarn || !strings.Contains(check.Detail, "pool not found") {
		t.Errorf("storage check = %+v, want SetupWarn carrying incus' own message", check)
	}
}

// TestVersionReportsWhetherTheDaemonCanReachIncus: it is what the app and the
// command line compare against their own, to spot a daemon that started before
// host setup and restart it.
func TestVersionReportsWhetherTheDaemonCanReachIncus(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "unix.socket")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INCUS_SOCKET", socket)
	d := startTestDaemon(t, root, fakeIncus)

	info, err := d.client.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Incus == nil || !*info.Incus {
		t.Errorf("Version().Incus = %v, want true: the command and the socket are both there", info.Incus)
	}

	t.Setenv("INCUS_SOCKET", filepath.Join(root, "gone"))
	info, err = d.client.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Incus == nil || *info.Incus {
		t.Errorf("Version().Incus = %v, want false: there's no socket to open", info.Incus)
	}
}

// imageIncus stands in for incus with a base image built by the version and
// components given, with the tools this AgentBox pins for them, so Setup sees
// a real answer for what the image has in it.
func imageIncus(version string, components image.Components) string {
	tools := image.ToolsFor(components)
	return toolsIncus(version, components, image.ToolsVersion(tools), toolSpecs(tools), "", "")
}

func toolSpecs(tools []image.Tool) string {
	specs := make([]string, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, t.Spec)
	}
	return strings.Join(specs, " ")
}

// imageConfig is the base image's configuration, as incus query answers it.
func imageConfig(version string, components image.Components, toolsVersion, tools string) string {
	return fmt.Sprintf(`{"config": {"user.agentbox.image-version": %q, "user.agentbox.with-android": %q, "user.agentbox.with-codex": %q, "user.agentbox.with-opencode": %q, "user.agentbox.tools-version": %q, "user.agentbox.tools": %q}, "devices": {}}`,
		version, flag01(components.Android), flag01(components.Codex), flag01(components.OpenCode), toolsVersion, tools)
}

// toolsIncus is imageIncus with the tools the image records given, and a
// test's own cases, which come before the defaults. Every command is logged
// to $INCUS_LOG, one a line. Once $INCUS_LOG.updated exists, the image
// answers with updated as its configuration instead.
func toolsIncus(version string, components image.Components, toolsVersion, tools, extra, updated string) string {
	config := imageConfig(version, components, toolsVersion, tools)
	return `echo "$*" >>"$INCUS_LOG"
case "$1" in
` + extra + `
  list) echo '[{"name": "agentbox-base", "status": "Stopped", "config": ` + config + `}]' ;;
  init) echo "Error: this fake incus builds nothing" >&2; exit 1 ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) if [ -f "$INCUS_LOG.updated" ]; then echo '` + updated + `'; else echo '` + config + `'; fi ;;
    esac ;;
esac
exit 0
`
}

func flag01(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// An image built with the current version and the components this installation
// chose is ready; turning one on afterwards makes it outdated, the same as a
// version bump does.
func TestSetupImageComponents(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{}))
	ctx := context.Background()

	if got := setupCheck(t, d, "image"); got.Status != api.SetupOK {
		t.Errorf("a default image: %+v", got)
	}
	status, err := d.client.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Image.Version != image.Version || status.Image.Components != (api.ImageComponents{}) {
		t.Errorf("Image = %+v", status.Image)
	}
	// The manifest is the whole list, optional parts included: the app shows
	// what turning one on would cost, not only what today's build fetches.
	if len(status.Image.Downloads) != len(image.Downloads) || status.Image.Hint == "" {
		t.Errorf("Image.Downloads = %+v, Hint = %q", status.Image.Downloads, status.Image.Hint)
	}

	if err := d.srv.store.SetFlag(ctx, state.SettingImageCodex, true); err != nil {
		t.Fatal(err)
	}
	base := setupCheck(t, d, "image")
	if base.Status != api.SetupOutdated || !strings.Contains(base.Detail, "you now want Codex") {
		t.Errorf("after turning Codex on: %+v", base)
	}
}

// Codex is optional in the image as well as in the credentials, and an image
// without it says so rather than leaving an agent to fail later.
func TestSetupCodexNotInImage(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{}))
	codex := setupCheck(t, d, "codex")
	if !strings.Contains(codex.Detail, image.CodexMissing) || codex.Fix != "agentbox image build --codex" {
		t.Errorf("without Codex in the image: %+v", codex)
	}

	// With Codex built in, it is the login that's missing again.
	withCodex := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{Codex: true}))
	if codex := setupCheck(t, withCodex, "codex"); !strings.Contains(codex.Detail, "can't run Codex yet") {
		t.Errorf("with Codex in the image: %+v", codex)
	}
}

// OpenCode is optional in the image and in the credentials, and reads the same
// way Codex does: the image first, then the login.
func TestSetupOpenCodeNotInImage(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{}))
	oc := setupCheck(t, d, "opencode")
	if !strings.Contains(oc.Detail, image.OpenCodeMissing) || oc.Fix != "agentbox image build --opencode" {
		t.Errorf("without OpenCode in the image: %+v", oc)
	}

	// With OpenCode built in, it is the login that's missing again.
	withOpenCode := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{OpenCode: true}))
	if oc := setupCheck(t, withOpenCode, "opencode"); !strings.Contains(oc.Detail, "can't run OpenCode yet") || oc.Fix != "agentbox auth opencode" {
		t.Errorf("with OpenCode in the image: %+v", oc)
	}
	// And the settings say what the app and the lead read: not ready, and no
	// model menu to offer, until both halves are there.
	settings, err := withOpenCode.client.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.OpenCodeReady || len(settings.OpenCodeModelChoices) != 0 {
		t.Errorf("Settings with no OpenCode login: ready = %v, models = %+v", settings.OpenCodeReady, settings.OpenCodeModelChoices)
	}
}

// A build remembers the components it was given, and one that asks for nothing
// keeps them: a rebuild after a version bump doesn't drop a component.
func TestBuildImageRemembersComponents(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{}))
	ctx := context.Background()
	on, off := true, false

	for _, c := range []struct {
		req  api.BuildImageRequest
		want image.Components
	}{
		{api.BuildImageRequest{}, image.Components{}},
		{api.BuildImageRequest{Codex: &on}, image.Components{Codex: true}},
		{api.BuildImageRequest{}, image.Components{Codex: true}},
		{api.BuildImageRequest{Android: &on}, image.Components{Android: true, Codex: true}},
		{api.BuildImageRequest{Codex: &off}, image.Components{Android: true}},
		{api.BuildImageRequest{OpenCode: &on}, image.Components{Android: true, OpenCode: true}},
		{api.BuildImageRequest{}, image.Components{Android: true, OpenCode: true}},
		{api.BuildImageRequest{OpenCode: &off}, image.Components{Android: true}},
	} {
		got, err := d.srv.chooseImageComponents(ctx, c.req)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("chooseImageComponents(%+v) = %+v, want %+v", c.req, got, c.want)
		}
		// And it is remembered, not only returned.
		if saved, err := d.srv.imageComponents(ctx); err != nil || saved != c.want {
			t.Errorf("after %+v the settings hold %+v (%v), want %+v", c.req, saved, err, c.want)
		}
	}
}
