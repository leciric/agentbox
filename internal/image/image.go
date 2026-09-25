// Package image builds the base instance that agents are copied from.
//
// Every machine makes its own image: Debian plus provision.sh, around a
// placeholder user that personalise.sh then renames to the host's own before
// the image is snapshotted as ready. Nobody publishes it. It holds software we
// may not redistribute, Claude Code first of all, and Debian's GPL packages
// would need their source published with it; built here, it's the user's own
// install of each.
package image

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/incus"
)

//go:embed provision.sh
var provision []byte

//go:embed personalise.sh
var personalise []byte

const (
	Base     = "agentbox-base"
	Snapshot = "ready"
	// Generic is the image before it is made this machine's: the placeholder
	// user, no host of its own.
	Generic = "generic"
	Profile = "agentbox"
	Source  = "images:debian/13"
)

// User is the host user agents act as: the same name, UID and GID in every agent.
type User struct {
	Name string
	UID  int
	GID  int
}

// Placeholder is the user provision.sh creates. It belongs to no machine:
// Build renames it to the host's user before snapshotting the image as ready.
// (Go has no constant structs.)
var Placeholder = User{Name: "agent", UID: 1000, GID: 1000}

// SnapshotRef is the snapshot agents are copied from.
func SnapshotRef() string { return Base + "/" + Snapshot }

// IDMap maps only the host user's UID and GID 1:1 into an instance. Files an
// agent writes in its mounted worktree belong to the host user, while
// container root stays an unprivileged UID on the host. (With shift=true
// mounts, container root would write files as host root.)
func IDMap(u User) string {
	return fmt.Sprintf("uid %d %d\ngid %d %d", u.UID, u.UID, u.GID, u.GID)
}

// CheckHost verifies that Incus may map the user's UID and GID, which
// agentbox host setup sets up.
func CheckHost(u User) error {
	for _, c := range []struct {
		file string
		id   int
	}{{"/etc/subuid", u.UID}, {"/etc/subgid", u.GID}} {
		b, err := os.ReadFile(c.file)
		if err != nil {
			return err
		}
		want := fmt.Sprintf("root:%d:1", c.id)
		if !slices.Contains(strings.Split(string(b), "\n"), want) {
			return fmt.Errorf("%s has no %q entry, so agents can't map your user: run sudo agentbox host setup", c.file, want)
		}
	}
	return nil
}

// Version changes whenever provision.sh changes what agents get, so Setup can
// ask you to rebuild a base image made by an older AgentBox.
const Version = "2026.09.25.2"

// CodexMissing is what Setup and agent creation say about an image built
// without Codex. Both use the same words, because the fix is the same one.
// OpenCodeMissing says it for OpenCode, which is optional the same way.
const (
	CodexMissing    = "not built into the image; enable it and rebuild"
	OpenCodeMissing = CodexMissing
)

const (
	versionKey   = "user.agentbox.image-version"
	androidKey   = "user.agentbox.with-android"
	codexKey     = "user.agentbox.with-codex"
	opencodeKey  = "user.agentbox.with-opencode"
	devCachesKey = "user.agentbox.with-dev-caches"
)

// Installed is what the base image on this machine was built with.
type Installed struct {
	// Version is the image version that built it, or "" for an image from
	// before versions were recorded.
	Version string
	// Components are the optional parts in it. An image built before they
	// existed records neither and so reads as having neither, which is the
	// right answer for the rebuild prompt either way: its version no longer
	// matches, so Setup already asks for a rebuild.
	Components Components
}

// InstalledBuild reads what the base image was built with.
func InstalledBuild(ctx context.Context, inc incus.Client) (Installed, error) {
	config, err := inc.Config(ctx, Base)
	if err != nil {
		return Installed{}, err
	}
	return Installed{
		Version: config[versionKey],
		Components: Components{
			Android:   config[androidKey] == "1",
			Codex:     config[codexKey] == "1",
			OpenCode:  config[opencodeKey] == "1",
			DevCaches: config[devCachesKey] == "1",
		},
	}, nil
}

// Ready reports whether the base snapshot exists.
func Ready(ctx context.Context, inc incus.Client) (bool, error) {
	return inc.HasSnapshot(ctx, Base, Snapshot)
}

var kernelModules = []string{
	"overlay", "br_netfilter", "ip_tables", "ip6_tables", "iptable_nat",
	"nf_nat", "nf_tables", "xt_conntrack", "xt_MASQUERADE", "xt_addrtype",
}

// EnsureProfile creates the Incus profile that lets agents run Docker.
func EnsureProfile(ctx context.Context, inc incus.Client) error {
	if _, err := inc.Run(ctx, "profile", "show", Profile); err == nil {
		return nil
	}
	// Containers can't load kernel modules, so Incus loads Docker's on the host when an agent starts.
	var modules []string
	for _, m := range kernelModules {
		if exec.CommandContext(ctx, "modinfo", "-n", m).Run() == nil {
			modules = append(modules, m)
		}
	}
	if _, err := inc.Run(ctx, "profile", "create", Profile); err != nil {
		return err
	}
	_, err := inc.Run(ctx, "profile", "set", Profile,
		"security.nesting=true",
		"security.syscalls.intercept.mknod=true",
		"security.syscalls.intercept.setxattr=true",
		"linux.kernel_modules="+strings.Join(modules, ","))
	return err
}

// Components are the parts of the base image that are only in it if you ask.
// All are off by default: together they are about 650 MB on top of a 1.1 GB
// build, and most agents use none of them.
type Components struct {
	// Android adds scrcpy, which mirrors the screen of an agent's Android
	// emulator. Only agents of Android projects ever use it.
	Android bool
	// Codex adds the Codex CLI and the ACP adapter the app's chat drives it
	// with. An agent created with --ai codex needs them.
	Codex bool
	// OpenCode adds the OpenCode CLI, which is its own ACP adapter
	// (`opencode acp`). An agent created with --ai opencode needs it.
	OpenCode bool
	// DevCaches fills the Go module and build caches, npm's cache and
	// Electron's download from AgentBox's own repository, so an agent working
	// on AgentBox itself starts testing without downloading or compiling its
	// dependencies first. Only a machine that develops AgentBox wants it.
	DevCaches bool
}

// Any reports whether any component is asked for.
func (c Components) Any() bool { return c.Android || c.Codex || c.OpenCode || c.DevCaches }

// Env renders the components as the environment variables provision.sh checks.
func (c Components) Env() []string {
	return []string{
		"AGENTBOX_WITH_ANDROID=" + envFlag(c.Android),
		"AGENTBOX_WITH_CODEX=" + envFlag(c.Codex),
		"AGENTBOX_WITH_OPENCODE=" + envFlag(c.OpenCode),
		"AGENTBOX_WITH_DEV_CACHES=" + envFlag(c.DevCaches),
	}
}

// Summary names the optional components that are on, for a person to read.
func (c Components) Summary() string {
	var on []string
	if c.Android {
		on = append(on, "the Android tools")
	}
	if c.Codex {
		on = append(on, "Codex")
	}
	if c.OpenCode {
		on = append(on, "OpenCode")
	}
	if c.DevCaches {
		on = append(on, "AgentBox's development caches")
	}
	if len(on) == 0 {
		return "no optional components"
	}
	return strings.Join(on, " and ")
}

func envFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// Options control how Build makes the base image.
type Options struct {
	// Components are the optional parts to put in it.
	Components Components
}

// readyTimeout is how long an instance gets to boot and take an address.
const readyTimeout = 2 * time.Minute

// Copies must boot with a new machine-id, or DHCP gives them all the same address.
const scrubMachineID = "truncate -s0 /etc/machine-id && rm -f /var/lib/dbus/machine-id"

// Build makes the base image and snapshots it. Existing agents are independent
// copies and aren't affected.
func Build(ctx context.Context, inc incus.Client, u User, opts Options, log io.Writer) error {
	step := stepper(log)
	// scrcpy is x86_64's, and so are the emulators it mirrors.
	if opts.Components.Android && runtime.GOARCH != "amd64" {
		return fmt.Errorf("the Android tools are x86_64 only, and this machine is %s: build the image without them", runtime.GOARCH)
	}
	if err := EnsureProfile(ctx, inc); err != nil {
		return err
	}
	// Build under another name and swap it in at the end: agents can still be
	// created from the current image meanwhile, and a failed build keeps it.
	next := Base + "-next"
	if err := remove(ctx, inc, next); err != nil {
		return err
	}
	fail := func(err error) error {
		inc.Run(context.WithoutCancel(ctx), "delete", "--force", next)
		return err
	}

	if err := buildLocally(ctx, inc, next, u, opts.Components, log); err != nil {
		return fail(err)
	}

	step("Snapshotting the image as %s, before it is made yours", Generic)
	// The components are recorded next to the version, so Setup can ask for a
	// rebuild when you turn one on, the same way a version bump does.
	if err := run(ctx, inc,
		[]string{"config", "set", next,
			versionKey + "=" + Version,
			androidKey + "=" + envFlag(opts.Components.Android),
			codexKey + "=" + envFlag(opts.Components.Codex),
			opencodeKey + "=" + envFlag(opts.Components.OpenCode),
			devCachesKey + "=" + envFlag(opts.Components.DevCaches)},
		[]string{"snapshot", "create", next, Generic},
	); err != nil {
		return fail(err)
	}

	step("Making it this machine's: the user %s (%d:%d) instead of %s", u.Name, u.UID, u.GID, Placeholder.Name)
	if err := personaliseFor(ctx, inc, next, u, log); err != nil {
		return fail(err)
	}

	step("Snapshotting it as %s", SnapshotRef())
	if err := run(ctx, inc, []string{"snapshot", "create", next, Snapshot}); err != nil {
		return fail(err)
	}

	step("Replacing the previous %s", Base)
	old := Base + "-old"
	inc.Run(ctx, "delete", "--force", old)
	// A new machine has no previous image, so there's nothing to delete after the swap.
	replaced := false
	if _, err := inc.Instance(ctx, Base); err == nil {
		if err := run(ctx, inc, []string{"rename", Base, old}); err != nil {
			return fail(err)
		}
		replaced = true
	}
	if err := run(ctx, inc, []string{"rename", next, Base}); err != nil {
		return err
	}
	if !replaced {
		return nil
	}
	return run(ctx, inc, []string{"delete", "--force", old})
}

// DebianMirror is the Debian mirror a local build downloads Debian's packages
// from, from the daemon's AGENTBOX_DEBIAN_MIRROR: for a connection on which
// deb.debian.org is slow. Empty means deb.debian.org.
func DebianMirror() string {
	return strings.TrimRight(os.Getenv("AGENTBOX_DEBIAN_MIRROR"), "/")
}

// buildLocally makes the image from scratch: Debian plus provision.sh, around
// the placeholder user.
func buildLocally(ctx context.Context, inc incus.Client, next string, u User, components Components, log io.Writer) error {
	step := stepper(log)
	step("Creating %s from %s", next, Source)
	if err := run(ctx, inc,
		[]string{"init", Source, next, "--profile", "default", "--profile", Profile},
		// The host's map from the start, so Incus never has to shift the
		// instance's files: agents copied from it use the same map.
		[]string{"config", "set", next, "raw.idmap=" + IDMap(u)},
		[]string{"start", next},
	); err != nil {
		return err
	}
	if _, err := inc.WaitReady(ctx, next, readyTimeout); err != nil {
		return err
	}

	step("Installing Docker, mise, Go, Node.js, pnpm, Claude Code, the GitHub CLI, the browser and media tools, with %s", components.Summary())
	step("Downloading about %d MB; %s", TotalMB(DownloadsFor(components)), DownloadsHint)
	if err := inc.WriteFile(ctx, next, "/root/provision.sh", provision, 0, 0, 0o700); err != nil {
		return err
	}
	// The components reach provision.sh as environment variables rather than
	// arguments, so a script run by hand in an instance defaults to neither.
	args := []string{"exec", next, "-T"}
	for _, env := range components.Env() {
		args = append(args, "--env", env)
	}
	if mirror := DebianMirror(); mirror != "" {
		args = append(args, "--env", "AGENTBOX_DEBIAN_MIRROR="+mirror)
	}
	cmd := inc.Command(ctx, append(args, "--", "/root/provision.sh",
		Placeholder.Name, strconv.Itoa(Placeholder.UID), strconv.Itoa(Placeholder.GID))...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("provisioning %s: %w", next, err)
	}
	return run(ctx, inc,
		[]string{"exec", next, "--", "sh", "-c", "rm /root/provision.sh && " + scrubMachineID},
		[]string{"stop", next},
	)
}

// personaliseFor turns the user-agnostic image into this machine's: the host
// user in place of the placeholder, and the host's ID map.
func personaliseFor(ctx context.Context, inc incus.Client, next string, u User, log io.Writer) error {
	if err := run(ctx, inc,
		// Set before it starts: Incus shifts the filesystem once here, rather
		// than for every agent copied from the image afterwards.
		[]string{"config", "set", next, "raw.idmap=" + IDMap(u)},
		[]string{"start", next},
	); err != nil {
		return err
	}
	if _, err := inc.WaitReady(ctx, next, readyTimeout); err != nil {
		return err
	}
	if err := inc.WriteFile(ctx, next, "/root/personalise.sh", personalise, 0, 0, 0o700); err != nil {
		return err
	}
	cmd := inc.Command(ctx, "exec", next, "-T", "--", "/root/personalise.sh",
		Placeholder.Name, u.Name, strconv.Itoa(u.UID), strconv.Itoa(u.GID))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("making %s yours: %w", next, err)
	}
	return run(ctx, inc,
		// Booting to personalise it gave it a machine-id of its own again.
		[]string{"exec", next, "--", "sh", "-c", "rm -f /root/personalise.sh && " + scrubMachineID},
		[]string{"stop", next},
	)
}

// remove deletes an instance left behind by an interrupted build.
func remove(ctx context.Context, inc incus.Client, name string) error {
	if _, err := inc.Instance(ctx, name); err != nil {
		if errors.Is(err, incus.ErrNotFound) {
			return nil
		}
		return err
	}
	_, err := inc.Run(ctx, "delete", "--force", name)
	return err
}

func run(ctx context.Context, inc incus.Client, commands ...[]string) error {
	for _, args := range commands {
		if _, err := inc.Run(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// stepper writes the headings of a job log.
func stepper(log io.Writer) func(string, ...any) {
	return func(format string, args ...any) { fmt.Fprintf(log, "==> "+format+"\n", args...) }
}
