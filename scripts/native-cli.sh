#!/usr/bin/env bash
# Builds the agentbox command-line tool the native app has built in
# (native/crates/app/build.rs, src/cli.rs), the way the Electron app's build
# does for its own (desktop/scripts/dist.mjs), so the two apps of one version
# carry the same tool:
#
#   scripts/native-cli.sh linux|windows|mac <version> <dir>
#
# It leaves <dir>/agentbox (agentbox.exe on Windows) and, on a Mac and on
# Windows, <dir>/agentbox-linux, the Linux tool their front end installs in
# AgentBox's VM or WSL distro. Build the app with them:
#
#   AGENTBOX_BUNDLED_CLI=<dir>/agentbox AGENTBOX_BUNDLED_CLI_LINUX=<dir>/agentbox-linux \
#     AGENTBOX_VERSION=<version> cargo build --release -p agentbox-native
#
# On a Mac the tool is signed here, before it goes into the app
# (scripts/mac-sign.sh, with $MAC_SIGN_IDENTITY when set): the app writes it
# out as it is, and macOS runs no unsigned binary on Apple silicon.
set -euo pipefail

os=${1:?usage: $0 linux|windows|mac <version> <dir>}
version=${2:?usage: $0 linux|windows|mac <version> <dir>}
dir=${3:?usage: $0 linux|windows|mac <version> <dir>}
root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$dir"
dir=$(cd "$dir" && pwd)
# main.version is agentbox.exe's: the Windows build has no internal/cli.
ldflags="-s -w -X agentbox/internal/cli.version=$version -X agentbox/internal/daemon.Version=$version -X main.version=$version"
build() { # <file> [VAR=value...]
  local out=$1
  shift
  (cd "$root" && env "$@" go build -trimpath -ldflags "$ldflags" -o "$dir/$out" ./cmd/agentbox)
}

case $os in
  linux)
    build agentbox
    ;;
  windows)
    build agentbox.exe GOOS=windows GOARCH=amd64
    build agentbox-linux GOOS=linux GOARCH=amd64 CGO_ENABLED=0
    ;;
  mac)
    # With cgo, for the vz driver (dist.mjs).
    arch=$(go env GOARCH)
    build agentbox GOOS=darwin GOARCH="$arch" CGO_ENABLED=1
    build agentbox-linux GOOS=linux GOARCH="$arch" CGO_ENABLED=0
    "$root/scripts/mac-sign.sh" ${MAC_SIGN_IDENTITY:+--identity "$MAC_SIGN_IDENTITY"} "$dir/agentbox"
    ;;
  *)
    echo "usage: $0 linux|windows|mac <version> <dir>" >&2
    exit 1
    ;;
esac
ls -l "$dir"
