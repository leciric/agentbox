#!/usr/bin/env bash
# Builds Cloud Hypervisor with Spectrum OS's GPU patchset, crosvm's GPU device
# and virglrenderer (with Venus), for linux/amd64, in Docker, from the pins in
# versions.env. Writes the tarball to the output directory (default: out/
# next to this script).
#
#   scripts/chv-gpu/build.sh [OUTPUT_DIR]
#
# CARGO_BUILD_JOBS (default 4) bounds the Rust builds' parallelism: Cloud
# Hypervisor's release profile links with LTO, which takes a GB or two a job.
# DOCKER_BUILD_FLAGS goes to docker build (--no-cache: does it reproduce?).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$here/out}

case "$(uname -m)" in
x86_64) ;;
*)
	# arm64 would build the same way on an arm64 host, with
	# aarch64-unknown-linux-musl for Cloud Hypervisor, but nobody has tried.
	echo "build.sh builds for x86_64 only (this is $(uname -m))" >&2
	exit 1
	;;
esac
command -v docker >/dev/null || { echo "build.sh needs Docker" >&2; exit 1; }

args=()
while IFS='=' read -r key value; do
	[[ $key =~ ^[A-Z_0-9]+$ ]] || continue
	args+=(--build-arg "$key=$value")
done < "$here/versions.env"
args+=(--build-arg "CARGO_BUILD_JOBS=${CARGO_BUILD_JOBS:-4}")
# The build is named for its inputs: chv-<CHV_VERSION>-<hash>.
args+=(--build-arg "CHV_BUILD=$("$here/inputs-hash.sh")")

# The Dockerfile's default base image is a copy of versions.env's.
rust_image=$(sed -n 's/^RUST_IMAGE=//p' "$here/versions.env")
grep -qxF "ARG RUST_IMAGE=$rust_image" "$here/Dockerfile" || {
	echo "build.sh: the Dockerfile's ARG RUST_IMAGE default isn't versions.env's RUST_IMAGE ($rust_image)" >&2
	exit 1
}

mkdir -p "$out"
# buildx where there is one, which can cache layers elsewhere (the Cloud
# Hypervisor workflow: DOCKER_BUILD_FLAGS="--cache-from type=gha ...").
build=(build)
docker buildx version >/dev/null 2>&1 && build=(buildx build)
# shellcheck disable=SC2086 # flags, split on purpose
DOCKER_BUILDKIT=1 docker "${build[@]}" ${DOCKER_BUILD_FLAGS:-} "${args[@]}" \
	--output "type=local,dest=$out" \
	-f "$here/Dockerfile" "$here"

echo
cat "$out"/*.tar.gz.sha256
echo "Host libraries it needs (HOST-LIBRARIES):"
sed 's/^/  /' "$out/HOST-LIBRARIES"
