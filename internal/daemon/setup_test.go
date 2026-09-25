package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// unusableIncus is an incus that is installed but won't answer: what a machine
// looks like before host setup, or to a process that may not open the socket.
const unusableIncus = `echo "Error: Get \"http://unix.socket/1.0\": dial unix /var/lib/incus/unix.socket: connect: permission denied" >&2
exit 1
`

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
// components given, so Setup sees a real answer for what the image has in it.
func imageIncus(version string, components image.Components) string {
	config := fmt.Sprintf(`{"config": {"user.agentbox.image-version": %q, "user.agentbox.with-android": %q, "user.agentbox.with-codex": %q, "user.agentbox.with-opencode": %q}, "devices": {}}`,
		version, flag01(components.Android), flag01(components.Codex), flag01(components.OpenCode))
	return `case "$1" in
  list) echo '[{"name": "agentbox-base", "status": "Stopped", "config": ` + config + `}]' ;;
  init) echo "Error: this fake incus builds nothing" >&2; exit 1 ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '` + config + `' ;;
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
