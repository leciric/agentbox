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
(see [macOS](#macos) and [Windows](#windows)).

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

## macOS

Download `AgentBox-<version>-mac-arm64.dmg` (Apple Silicon) or `-mac-x64.dmg` (Intel) and drag
AgentBox to Applications. It isn't signed yet: right-click it and choose **Open** the first time.

AgentBox runs in a Linux VM made with [Lima](https://lima-vm.io). You don't need to install Lima:
when your Mac doesn't have it, Setup downloads Lima's release (about 40 MB), checks it against the
version this AgentBox was tested with, and keeps it in `~/.local/share/agentbox/lima`. A Lima you
installed yourself, with Homebrew or otherwise, is used instead when there is one.

On Apple Silicon, installing [krunkit](https://lima-vm.io/docs/config/vmtype/krunkit/) before
Setup makes a VM that gives the memory its agents stop using back to your Mac:

```bash
brew tap slp/krun && brew trust slp/krun && brew install krunkit
```

## Windows

Download `AgentBox-<version>-x64-setup.exe` (an installer) or `-x64-portable.exe`. AgentBox runs
in a WSL2 distro of its own, which Setup makes; WSL itself must be installed first
(`wsl --install`, in an administrator's terminal). It isn't signed yet, so the installer and
antivirus may need an exception.
