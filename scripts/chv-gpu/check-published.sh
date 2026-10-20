#!/usr/bin/env bash
# Checks that the Cloud Hypervisor build internal/hostvm/chv/tools.go pins
# (chvBundle) is published where it says: its release asset is there, and its
# published SHA256 is the pin. The nightly and release workflows run it before
# they build, since every install, nightly or release, downloads that one
# build on its first VM start and can't run a VM without it.
#
#   scripts/chv-gpu/check-published.sh
#
# With --download it fetches the tarball itself and checks its sum too.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
bundle=$(sed -n '/^var chvBundle = bundle{/,/^}/p' "$root/internal/hostvm/chv/tools.go")
url=$(sed -n 's/^\turl: *"\(.*\)",$/\1/p' <<<"$bundle")
pin=$(sed -n 's/^\tsha256: *"\([0-9a-f]*\)",$/\1/p' <<<"$bundle")
[ -n "$url" ] && [ ${#pin} = 64 ] || { echo "::error::can't read chvBundle's url and sha256 from tools.go" >&2; exit 1; }

fail() {
	echo "::error::$1. The Cloud Hypervisor workflow (.github/workflows/chv.yml) publishes a build when its inputs change on main and opens the pull request that pins it: wait for it, or merge that pull request." >&2
	exit 1
}
published=$(curl -fsSL --retry 3 "$url.sha256" 2>/dev/null | cut -d' ' -f1) || fail "the pinned Cloud Hypervisor build isn't published: $url.sha256 isn't there"
[ "$published" = "$pin" ] || fail "$url is published as $published, but tools.go pins $pin"
if [ "${1:-}" = --download ]; then
	got=$(curl -fsSL --retry 3 "$url" | sha256sum | cut -d' ' -f1) || fail "$url doesn't download"
	[ "$got" = "$pin" ] || fail "$url downloads as $got, but tools.go pins $pin"
fi
echo "The pinned Cloud Hypervisor build is published: $url ($pin)"
