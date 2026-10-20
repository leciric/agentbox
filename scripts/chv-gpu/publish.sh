#!/usr/bin/env bash
# Publishes AgentBox's Cloud Hypervisor (build.sh's output) as a release of
# this repository, which internal/hostvm/chv/tools.go (chvBundle) downloads:
#
#   release chv-<CHV_VERSION>-agentbox<CHV_BUILD>, a prerelease, never latest, with
#   chv-<...>-x86_64.tar.gz            the tarball
#   chv-<...>-x86_64.tar.gz.sha256     its sum
#   VERSIONS, HOST-LIBRARIES           what's in it, what the host must have
#
#   scripts/chv-gpu/publish.sh OUT_DIR
#
# A published build is never replaced, and never deleted: every AgentBox
# release pins one by its SHA256, so an older AgentBox keeps finding its own.
# When the release already has the tarball, its sum must be the one just
# built (the build reproduces), or this fails: change anything, and
# versions.env's CHV_BUILD goes up with it. The Cloud Hypervisor workflow
# runs it; it needs gh (GH_TOKEN, with contents: write) and GITHUB_SHA (or
# TARGET_COMMITISH) for a new release's tag.
set -euo pipefail

out=${1:?usage: $0 OUT_DIR}
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=versions.env
. "$here/versions.env"
name=chv-$CHV_VERSION-agentbox$CHV_BUILD
tag=$name
tarball=$out/$name-x86_64.tar.gz
[ -f "$tarball" ] || { echo "no $tarball: did build.sh run with this versions.env?" >&2; exit 1; }
(cd "$out" && sha256sum --quiet -c "$(basename "$tarball").sha256")
sum=$(cut -d' ' -f1 "$tarball.sha256")

# The pin AgentBox downloads must be this build.
pin=$(sed -n '/^var chvBundle = bundle{/,/^}/s/^\tsha256: *"\([0-9a-f]*\)",$/\1/p' "$here/../../internal/hostvm/chv/tools.go")
if [ "$pin" != "$sum" ]; then
	echo "tools.go pins $name as ${pin:-nothing}, but this build is $sum: pin it, with the files' sums from SHA256SUMS" >&2
	exit 1
fi

if gh release view "$tag" >/dev/null 2>&1; then
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	if gh release download "$tag" -p "$(basename "$tarball").sha256" -D "$tmp" 2>/dev/null; then
		have=$(cut -d' ' -f1 "$tmp/$(basename "$tarball").sha256")
		if [ "$have" = "$sum" ]; then
			echo "==> $tag already has this build ($sum)"
			exit 0
		fi
		echo "$tag has another build ($have), and published builds are never replaced: bump CHV_BUILD in versions.env" >&2
		exit 1
	fi
	echo "==> $tag exists without the tarball: uploading it"
else
	echo "==> Creating $tag"
	notes=$(mktemp)
	cat >"$notes" <<NOTES
Cloud Hypervisor $CHV_VERSION as AgentBox's Linux VM runs it: upstream's v$CHV_VERSION with Spectrum OS's virtio-gpu patchset rebased onto it, and crosvm's GPU device with virglrenderer (Venus), which give agents the host's GPU when "GPU for agents" is on. AgentBox downloads it by itself; nothing to install by hand.

Built by the Cloud Hypervisor workflow from \`${GITHUB_SHA:-main}\` with \`scripts/chv-gpu/build.sh\`, reproducibly. \`VERSIONS\` says what's in it; \`HOST-LIBRARIES\` what crosvm needs from the host; the tarball's \`licenses/\` holds each project's licence and the patches as applied.
NOTES
	gh release create "$tag" --title "Cloud Hypervisor $CHV_VERSION (AgentBox build $CHV_BUILD)" --notes-file "$notes" \
		--prerelease --latest=false --target "${GITHUB_SHA:-${TARGET_COMMITISH:-main}}"
fi
# The sum goes last: a release with it has the whole build.
gh release upload "$tag" "$tarball" "$out/VERSIONS" "$out/HOST-LIBRARIES" --clobber
gh release upload "$tag" "$tarball.sha256" --clobber
echo "==> Published $tag ($sum)"
