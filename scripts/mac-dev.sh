#!/usr/bin/env bash
# Builds AgentBox for this Mac from the checkout it's in, and sets it up to run:
# the app in /Applications, the agentbox command in ~/.local/bin, and the
# AgentBox inside the Linux VM (README.md, "On a Mac") at the same build. It's for
# working on a branch: run it again after pulling, and the app, the command and
# the VM all run the new code.
#
#   scripts/mac-dev.sh [options]
#
#   --no-pull              build what's checked out, without pulling first
#   --no-vm                build and install, but leave the VM alone (it stays
#                          stopped if it's stopped)
#   --image                build the base image after the VM is up (a local
#                          build downloads about 1 GB and takes a while)
#   --debian-mirror <url>  the Debian mirror a local image build downloads from,
#                          instead of deb.debian.org (AGENTBOX_DEBIAN_MIRROR)
#   --no-open              don't open the app at the end
#   --apps <dir>           where the app goes; /Applications by default
#
# Why each step is here:
# - The version in desktop/package.json doesn't change between two builds of a
#   branch, and the app only restarts the VM's daemon for another version, so
#   the VM gets the new agentbox through `agentbox vm upgrade`, which installs
#   it and restarts the daemon on it.
# - electron-builder leaves an unsigned build with a broken signature, which
#   macOS on Apple silicon won't run, so it's signed for this machine (ad hoc).
# - VS Code's terminal sets ELECTRON_RUN_AS_NODE, and an Electron app started
#   with it runs as plain Node and quits at once, so it's unset here.
# - The command is linked the way the app's Settings → Command-line tool does
#   it: ~/.local/bin/agentbox → the app's own copy in AgentBox's data
#   directory, with agentbox-linux beside it, where the macOS front end looks.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)

usage() { sed -n '/^#   scripts/,/^#   --apps/p' "$0" | sed 's/^# \{0,1\}//' >&2; }

pull=true vm=true image=false open_app=true apps=/Applications
while [ $# -gt 0 ]; do
  case $1 in
    --no-pull) pull=false; shift ;;
    --no-vm) vm=false; shift ;;
    --image) image=true; shift ;;
    --debian-mirror)
      [ $# -ge 2 ] || { echo "--debian-mirror needs a URL" >&2; exit 2; }
      export AGENTBOX_DEBIAN_MIRROR=$2; shift 2 ;;
    --no-open) open_app=false; shift ;;
    --apps)
      [ $# -ge 2 ] || { echo "--apps needs a directory" >&2; exit 2; }
      apps=$2; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

step() { printf '\n==> %s\n' "$*"; }
die() { echo "error: $*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] || die "this is for a Mac; on Linux, npm --prefix desktop run dist builds the packages"
if $image && ! $vm; then die "--image needs the VM: leave out --no-vm"; fi
for tool in go node npm git codesign ditto; do
  command -v "$tool" >/dev/null || die "$tool isn't installed (Go and Node come from mise.toml: mise install)"
done
if $vm && ! command -v limactl >/dev/null; then
  die "Lima isn't installed: brew install lima (or run with --no-vm)"
fi

case $(uname -m) in
  arm64) arch=arm64 ;;
  x86_64) arch=x64 ;;
  *) die "no Mac build for $(uname -m)" ;;
esac
built=$root/desktop/dist/mac-$arch/AgentBox.app
[ "$arch" = x64 ] && built=$root/desktop/dist/mac/AgentBox.app
app=$apps/AgentBox.app
data=${XDG_DATA_HOME:-$HOME/.local/share}/agentbox
cli=$data/bin/agentbox

if $pull; then
  step "Pulling $(git -C "$root" branch --show-current)"
  if git -C "$root" rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
    git -C "$root" pull --ff-only
  else
    echo "this branch has no upstream, so there's nothing to pull: building what's checked out"
  fi
fi

# npm writes node_modules/.package-lock.json on every install, so a lock file
# newer than it is one that changed since.
if [ ! -f "$root/desktop/node_modules/.package-lock.json" ] ||
  [ "$root/desktop/package-lock.json" -nt "$root/desktop/node_modules/.package-lock.json" ]; then
  step "Installing the desktop app's dependencies"
  npm --prefix "$root/desktop" ci
fi

step "Building the macOS agentbox, the Linux agentbox for the VM, and the app"
env -u ELECTRON_RUN_AS_NODE CSC_IDENTITY_AUTO_DISCOVERY=false \
  npm --prefix "$root/desktop" run dist -- --mac "--$arch"
[ -d "$built" ] || die "the build left no app at $built"

if ! codesign -v "$built" 2>/dev/null; then
  step "Signing the app for this Mac"
  codesign --force --deep --sign - "$built"
fi

if pgrep -xq AgentBox; then
  step "Quitting AgentBox"
  osascript -e 'quit app "AgentBox"' >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do pgrep -xq AgentBox || break; sleep 0.5; done
  pgrep -xq AgentBox && die "AgentBox is still running: quit it and run this again"
fi

step "Installing the app as $app"
mkdir -p "$apps"
rm -rf "$app"
ditto "$built" "$app"

step "Linking the agentbox command into ~/.local/bin"
mkdir -p "$data/bin" "$HOME/.local/bin"
for name in agentbox agentbox-linux; do
  cp "$app/Contents/Resources/bin/$name" "$data/bin/$name.new"
  chmod 0755 "$data/bin/$name.new"
  mv "$data/bin/$name.new" "$data/bin/$name"
done
link=$HOME/.local/bin/agentbox
if [ -L "$link" ] || [ ! -e "$link" ]; then
  ln -sfn "$cli" "$link"
else
  echo "warning: $link is a file this script didn't make, so it's left as it is" >&2
fi
case ":$PATH:" in
  *":$HOME/.local/bin:"*) ;;
  *) echo "note: ~/.local/bin isn't on your PATH, so run it as $link" ;;
esac

if $vm; then
  if "$cli" vm status --json 2>/dev/null | grep -q '"exists":true'; then
    step "Updating the AgentBox in the VM, and restarting its daemon"
    "$cli" vm upgrade
  else
    step "Making AgentBox's VM (the first time, this takes a few minutes)"
    "$cli" vm init
  fi
  if $image; then
    step "Building the base image"
    "$cli" image build
  fi
  step "What's left to set up"
  "$cli" host check || true
fi

if $open_app; then
  step "Opening AgentBox"
  env -u ELECTRON_RUN_AS_NODE open "$app"
fi

step "Done: $("$cli" --version) from $(git -C "$root" rev-parse --short HEAD) on $(git -C "$root" branch --show-current)"
$vm || echo "The VM wasn't touched: the app (or scripts/mac-dev.sh without --no-vm) brings it up to date."
