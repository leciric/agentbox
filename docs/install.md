# Installing AgentBox

AgentBox is a desktop app with the `agentbox` command-line tool inside. On Linux, one command
checks your computer and installs it:

```bash
curl -fsSL https://downloads.agentbox.linting.dev/install.sh | sh
```

Then open **AgentBox**. Its **Setup** does the rest the first time it runs: it makes AgentBox's VM,
builds the machine your agents are copied from, and logs you in to Claude Code, Codex or OpenCode.

On a Mac or Windows, download the app from the
[downloads page](https://downloads.agentbox.linting.dev/index.html) instead
(see [macOS and Windows](#macos-and-windows-alpha)).

## What the install script does

Before it downloads anything, it checks that this computer can run AgentBox, and lists everything
that's missing at once, in plain words:

| Check | Needs | If it's missing |
|---|---|---|
| Processor | x86_64 (Linux on ARM isn't supported yet) | It stops. |
| Virtualization | `/dev/kvm`, for AgentBox's VM | No virtualization: it stops, and says to turn on Intel VT-x or AMD-V in the firmware's settings, or nested virtualization in a VM. A `/dev/kvm` you can't open yet, or KVM's module not loaded, is fine: Setup fixes both (see [below](#kvm)). |
| Memory | 8 GB | It stops. |
| Disk | 30 GB free in your home folder, where the VM's disk goes | It stops. |
| FUSE | `/dev/fuse` and libfuse2, only for the AppImage | It stops, and names the package to install. |

Then it downloads the latest release, checks it against the release's `SHA256SUMS`, and installs
the package that fits your distribution:

- **Debian and Ubuntu**: the `.deb`, with `apt-get` (asks for your password, through `sudo`).
- **Arch** and its derivatives: the `.pacman`, with `pacman` (asks for your password, through `sudo`).
- **Anything else**, Fedora included: the AppImage, in `~/.local/bin/AgentBox.AppImage`, with an
  entry in your applications menu. No password.

It runs only once it has downloaded completely: a connection cut half-way runs nothing.

| Option | What |
|---|---|
| `--check` | Only the checks: says what it would install, downloads nothing |
| `--appimage` | The AppImage, whatever your distribution |
| `AGENTBOX_CHANNEL=nightly` | The newest nightly instead of the newest release |

Options go after `sh -s --`:

```bash
curl -fsSL https://downloads.agentbox.linting.dev/install.sh | sh -s -- --check
```

To read the script before running it, download it first:

```bash
curl -fsSLO https://downloads.agentbox.linting.dev/install.sh
less install.sh
sh install.sh
```

### By hand

Every release's files are on the [downloads page](https://downloads.agentbox.linting.dev/index.html):

| File | What |
|---|---|
| `AgentBox-<version>-x86_64.AppImage` | Any distribution: `chmod +x` it and run it. Needs FUSE: `fuse2` (Arch), `fuse-libs` (Fedora), `libfuse2` (Debian 12, Ubuntu 22.04) or `libfuse2t64` (Ubuntu 24.04) |
| `AgentBox-<version>-amd64.deb` | Debian and Ubuntu: `sudo apt install ./AgentBox-<version>-amd64.deb` |
| `AgentBox-<version>-x64.pacman` | Arch: `sudo pacman -U AgentBox-<version>-x64.pacman` |
| `SHA256SUMS` | Checksums: `sha256sum -c --ignore-missing SHA256SUMS` |

## AgentBox's VM, on Linux

On Linux, AgentBox runs in a VM of its own: the daemon, Incus and every agent in one
[Cloud Hypervisor](https://www.cloudhypervisor.org) VM, with the CPUs and memory cap you give it
rather than all of your computer's, so heavy agent work can't freeze your desktop. Nothing is
installed on your system and no password is asked. It needs `/dev/kvm` (see [KVM](#kvm) below
if your user can't open it yet) and an `ssh` client. Setup makes it, at the size you pick; from the command line it's
`agentbox vm init` (`--cpus`, `--memory-cap`), which fetches Cloud Hypervisor, passt, virtiofsd and
the VM's disk into `~/.local/share/agentbox/vm` and sets the VM up (about a minute). Every
other `agentbox` command runs in the VM, and until it's made says to run `agentbox vm init`.

The VM's disk is a ready-made one AgentBox publishes on its GitHub releases (`vmdisk-<version>`,
about 230 MB, as big as Debian's own): Debian 13's cloud image with Incus and the other packages
the VM needs already installed, checked against the release's `SHA256SUMS`, so the VM's first boot
only sets up your user, your home's share and its keys. When it can't be downloaded, or this
version of AgentBox has none (only the newest is kept), `vm init` says so and uses Debian's own
cloud image instead, whose first boot installs those packages from Debian: a few minutes more, as
slow as your network. `AGENTBOX_VM_DISKS_URL=off` always does that. The release lists every
package in the disk, with where its sources are.

The VM starts with 4 GiB and takes more memory as its agents need it, up to its cap (three quarters
of your memory by default), and gives it back as they stop; the top bar's **Free resources** stops
every agent and turns the VM off. Settings → Storage and machine changes its CPUs and cap, as does
`agentbox vm resize`: a running VM changes at once, and its agents keep running. Your home folder is
shared with the VM at the same path, so projects and agents' worktrees stay where they are.
`agentbox vm delete --yes` removes it, and every agent's machine in it. Android emulators run in
the VM's agents when your CPU's KVM module has nested virtualization on (`nested=1`), and boot in
30–50 seconds rather than 20–25; GPUs aren't available in the VM.

Agents share the VM's memory, and every agent starts at once. Each agent's machine may use all of
the VM's memory cap but an eighth (at least 1 GiB): a process that outgrows that is stopped at
once, and its agent is told what was stopped and to run fewer test workers at once. When memory
runs short, it's the agents' tests and builds that give way: new ones wait to start (in the order
they asked), and if pressure stays high the newest one already running is paused, its memory moved
to swap, and resumed once it eases. For that the VM gets compressed swap in memory (zram, a quarter
of its cap) as AgentBox starts, used before any swapfile from `agentbox vm swap on`. The oldest one
always keeps running, so they all finish in turn. The agent shows **Paused for memory** or
**Waiting for memory** meanwhile. Claude Code agents do this around their tests and builds by
themselves; any other command can join in as `agentbox heavy -- <command>`.

## Moving an installation from before the VM

Earlier versions could also run agents directly on your computer's own Incus. An installation set
up that way keeps working exactly as it was until you move it, and nothing of it is deleted. After
updating, the app says once that AgentBox runs in a VM now and takes you to Settings → Setup →
**Move to a VM**; `agentbox vm migrate` does the same from the command line. It moves all of it in
one step: projects, settings, accounts, notes, memory, chats and media, and every agent with its
branch, worktree (uncommitted changes included), title and model. Each agent gets a new
machine in the VM, and its chat carries on; what was installed inside its old machine, and its home
folder outside the worktree, don't come along. It backs `state.db` up first, checks that everything
arrived, and can be run again if it stops half-way. Your system's Incus, and everything else in it,
stays as it was: the agents' old machines stay there, stopped, until you remove them with
`agentbox vm migrate --remove-old` (or the same place in Settings), which removes only AgentBox's
own. Until you remove them, `agentbox vm delete --yes` goes back to running on your system as before.

## KVM

AgentBox's VM needs `/dev/kvm`. Arch and Fedora give it to every user. Debian and Ubuntu give it
only to the `kvm` group, which counts from your next login, so a new install there can't open it
yet. When Setup finds that, it shows **Give access to KVM**: one password prompt, and the VM can be
made straight away, with no need to log out. From a terminal, the same is:

```bash
sudo agentbox host kvm
```

It gives your user `/dev/kvm` now (an access-control entry on the device, which counts for the
programs you're already running) and adds you to the `kvm` group for your later logins. On a
system with no `kvm` group, it adds a udev rule instead that gives `/dev/kvm` to whoever is using
the computer's screen. When there's no `/dev/kvm` at all but the processor can virtualize, it loads
KVM's module, now and at every start. It's safe to run again.

If the processor shows no virtualization at all, it's off in the firmware's settings (Intel VT-x or
AMD-V, often under CPU or Advanced), or, in a virtual machine, nested virtualization is off. Only
you can turn that on; Setup says so.

## macOS and Windows (alpha)

AgentBox also runs on a **Mac**, in a Linux VM it makes for you with [Lima](https://lima-vm.io)
(Setup downloads Lima when the Mac has none; a Lima you installed is used first), and on **Windows**, in a WSL2 distro it sets up the same way: the
daemon, Incus and every agent are the Linux ones, and the app and `agentbox` command on your machine
just talk to them there. Both are newer than the Linux build and ship unsigned for now — on a Mac,
right-click AgentBox in Applications and choose **Open** the first time; on Windows, the installer
and antivirus may need an exception. Download `AgentBox-<version>-mac-arm64.dmg` or
`-mac-x64.dmg`, or `AgentBox-<version>-x64-setup.exe` (installer) or `-x64-portable.exe`, from the
same [downloads](https://downloads.agentbox.linting.dev/index.html) page.

On a Mac with Apple Silicon, the VM is made with [krunkit](https://lima-vm.io/docs/config/vmtype/krunkit/)
when it's installed: it has a GPU for agents, and gives the memory its agents stop using back to
your Mac. The app's Setup installs it with Homebrew when it's missing (**Install krunkit and set up
the VM**), as does `agentbox vm krunkit`; by hand, before setting AgentBox up:

```bash
brew tap slp/krun && brew trust slp/krun && brew install krunkit
```

Lima's krunkit driver comes with Homebrew's Lima from 2.0 (`brew upgrade lima`), and with the Lima
that Setup downloads. Without krunkit,
the VM is made with Apple's Virtualization framework: no GPU, and it keeps whatever memory it has
used until it stops. A VM keeps the kind it was made with: to switch an existing one, Settings →
**GPU for agents** offers to install krunkit and remake the VM, or `agentbox vm krunkit --remake
--yes`. Either removes every agent's machine, the base image and AgentBox's projects and agents
(your worktrees stay on your disk: push what you want to keep first).

A krunkit VM also has a **GPU for agents**: Vulkan, run on your Mac's GPU through Metal, and
OpenGL on top of it. Turning on "GPU for agents" in Settings readies it, building a Vulkan driver
in the VM (a few minutes). A VM made before this needs Debian's generic kernel too, and one restart
for it (`agentbox vm stop`, then `agentbox vm start`); its old kernel stays until the new one has
started, and the VM goes back to it if the new one doesn't (restart the VM again if it hangs).
`agentbox vm gpu` says what the VM has, and `agentbox vm gpu --agent project/agent` what an agent
gets. Apple's Virtualization framework gives a Linux VM no 3D, so a
VM made without krunkit has none.

With Xcode installed, agents on a Mac can use the **iOS Simulator**, which runs on the Mac itself,
beside the VM: an agent's panel has an iOS view to pick a simulator, boot it, and watch and use its
screen, and agents drive it with `agentbox ios` (open, tap, swipe, type, screenshot, install a `.app`
built for the simulator, launch). The simulators are the Mac's, shared by every agent. A small helper,
`agentbox ios-helper`, runs on the Mac for it, started by any `agentbox` command there (the app runs
them); it listens on the Mac's `127.0.0.1` only, with a token in AgentBox's config, and the VM's
daemon reaches it there. It doesn't run with the experimental vz driver below, and hasn't been tried
on a real Mac yet.

On a Mac there is also an **experimental** way without Lima: `agentbox vm init --driver vz` (or
**Apple Virtualization, without Lima** in the app's Setup) has AgentBox run the VM itself with
Apple's Virtualization framework, on macOS 13 or later. It hasn't been tried on a real Mac yet; its
VM holds all of its memory while it runs, and `agentbox vm delete --yes` goes back to Lima's. It
boots the same ready-made disk as on Linux (Lima's VM doesn't: it starts from Lima's own Debian
image, and its first boot installs Incus). A build of your own needs signing for it:
`scripts/mac-sign.sh bin/agentbox`.
