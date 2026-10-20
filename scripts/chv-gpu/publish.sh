#!/usr/bin/env bash
# Publishes AgentBox's Cloud Hypervisor (build.sh's output) as a release of
# this repository, which internal/hostvm/chv/tools.go (chvBundle) downloads,
# nightlies and releases alike:
#
#   release chv-<CHV_VERSION>-<hash>, a prerelease, never latest, with
#   chv-<...>-x86_64.tar.gz            the tarball
#   chv-<...>-x86_64.tar.gz.sha256     its sum, uploaded last
#   VERSIONS, HOST-LIBRARIES           what's in it, what the host must have
#
#   scripts/chv-gpu/publish.sh OUT_DIR
#
# <hash> is the hash of the build's inputs (inputs-hash.sh). A published build
# is never replaced, nor deleted: every AgentBox pins one by its SHA256, so an
# older one keeps finding its own. When this hash is published already, this
# uploads nothing and leaves the published tarball in OUT_DIR/published/, as
# it does a new one: that's what pin.sh pins (crosvm doesn't build byte for
# byte alike twice, so a rebuild's sums aren't the published ones). It prints
# the tarball's path last. The Cloud Hypervisor workflow runs it on main; it
# needs gh (GH_TOKEN, with contents: write) and GITHUB_SHA for a new tag.
set -euo pipefail

out=${1:?usage: $0 OUT_DIR}
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=versions.env
. "$here/versions.env"
tag=chv-$CHV_VERSION-$("$here/inputs-hash.sh")
file=$tag-x86_64.tar.gz
pub=$out/published
mkdir -p "$pub"

if gh release view "$tag" >/dev/null 2>&1 &&
	gh release download "$tag" -p "$file.sha256" -p "$file" -D "$pub" --clobber 2>/dev/null &&
	(cd "$pub" && sha256sum --quiet -c "$file.sha256"); then
	echo "==> $tag is published already ($(cut -d' ' -f1 "$pub/$file.sha256")): keeping it" >&2
	echo "$pub/$file"
	exit 0
fi

[ -f "$out/$file" ] || { echo "no $out/$file: did build.sh run with these inputs?" >&2; exit 1; }
(cd "$out" && sha256sum --quiet -c "$file.sha256")
if ! gh release view "$tag" >/dev/null 2>&1; then
	echo "==> Creating $tag" >&2
	notes=$(mktemp)
	cat >"$notes" <<NOTES
Cloud Hypervisor $CHV_VERSION as AgentBox's Linux VM runs it: upstream's v$CHV_VERSION with Spectrum OS's virtio-gpu patchset rebased onto it, and crosvm's GPU device with virglrenderer (Venus), which give agents the host's GPU when "GPU for agents" is on. AgentBox downloads it by itself, nightly or release; nothing to install by hand.

Built by the Cloud Hypervisor workflow from \`${GITHUB_SHA:-main}\` with \`scripts/chv-gpu/build.sh\`; the tag's hash is its inputs'. \`VERSIONS\` says what's in it, \`HOST-LIBRARIES\` what crosvm needs from the host; the tarball's \`licenses/\` holds each project's licence and the patches as applied.
NOTES
	gh release create "$tag" --title "Cloud Hypervisor $CHV_VERSION ($tag)" --notes-file "$notes" \
		--prerelease --latest=false --target "${GITHUB_SHA:-${TARGET_COMMITISH:-main}}" >&2
fi
# The sum goes last: a release with it has the whole build.
gh release upload "$tag" "$out/$file" "$out/VERSIONS" "$out/HOST-LIBRARIES" --clobber >&2
gh release upload "$tag" "$out/$file.sha256" --clobber >&2
cp "$out/$file" "$out/$file.sha256" "$pub/"
echo "==> Published $tag" >&2
echo "$pub/$file"
