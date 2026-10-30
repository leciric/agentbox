#!/usr/bin/env bash
# Builds the hub's npm packages for every platform (hub/scripts/npm-build.sh),
# installs this platform's with the launcher from their tarballs, as npm would
# from the registry, and runs `npx agentbox-hub --version`: CI's Hub job.
#
#   hub/scripts/npm-check.sh [workdir]
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="${1:-$(mktemp -d)}"
mkdir -p "$work"
work="$(cd "$work" && pwd)"
version=0.0.0-check

"$root/scripts/npm-build.sh" "$version" "$work/dist"

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) platform=linux-x64 ;;
  Linux/aarch64 | Linux/arm64) platform=linux-arm64 ;;
  Darwin/x86_64) platform=darwin-x64 ;;
  Darwin/arm64) platform=darwin-arm64 ;;
  *) echo "no npm package for $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$work/packs" "$work/try"
(cd "$work/packs" && npm pack --silent "../dist/$platform" ../dist/agentbox-hub >/dev/null)
cd "$work/try"
npm init -y >/dev/null
npm install --silent --no-audit --no-fund \
  "../packs/agentbox-hub-$platform-$version.tgz" "../packs/agentbox-hub-$version.tgz"
got=$(npx --no-install agentbox-hub --version)
echo "$got"
[ "$got" = "agentbox-hub version $version" ] || { echo "npx agentbox-hub --version said: $got" >&2; exit 1; }
