#!/usr/bin/env bash
# Signs a macOS agentbox with the entitlement Apple's Virtualization framework
# asks of any process that runs a VM (com.apple.security.virtualization,
# scripts/agentbox.entitlements), which the experimental vz driver needs: it
# runs AgentBox's VM in the agentbox process itself (internal/hostvm/chv/vz.go).
# Lima's VM doesn't, so a binary without it only can't use --driver vz.
#
#   scripts/mac-sign.sh [--identity <codesign identity>] <agentbox>...
#
# Ad hoc by default, which is all a build of your own needs to run on your own
# Mac: `go build -o bin/agentbox ./cmd/agentbox && scripts/mac-sign.sh bin/agentbox`.
# With --identity (a Developer ID Application certificate's, in a keychain),
# it signs the way the release does (.github/workflows/release.yml): with the
# hardened runtime and a secure timestamp, which notarization asks for.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
identity=-
while [ $# -gt 0 ]; do
  case $1 in
    --identity) identity=${2:-}; [ -n "$identity" ] || { echo "--identity needs an identity" >&2; exit 2; }; shift 2 ;;
    -h | --help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 0 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) break ;;
  esac
done
[ $# -gt 0 ] || { echo "usage: scripts/mac-sign.sh [--identity <identity>] <agentbox>..." >&2; exit 2; }
[ "$(uname -s)" = Darwin ] || { echo "codesign is a Mac's: run this on a Mac" >&2; exit 1; }

entitlements=$root/scripts/agentbox.entitlements
for bin; do
  if [ "$identity" = - ]; then
    codesign --force --sign - --entitlements "$entitlements" "$bin"
  else
    codesign --force --sign "$identity" --options runtime --timestamp --entitlements "$entitlements" "$bin"
  fi
  codesign --verify --strict "$bin"
  codesign -d --entitlements - --xml "$bin" 2>/dev/null | grep -q com.apple.security.virtualization ||
    { echo "$bin wasn't signed with com.apple.security.virtualization" >&2; exit 1; }
  echo "signed $bin ($([ "$identity" = - ] && echo "ad hoc" || echo "$identity"))"
done
