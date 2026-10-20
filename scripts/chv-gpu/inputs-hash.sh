#!/usr/bin/env bash
# Prints the hash that names a Cloud Hypervisor build: 12 hex digits of the
# SHA-256 over every file the build reads (build.sh, the Dockerfile, its
# scripts, versions.env and patches/), names and contents. The same inputs
# give the same release, chv-<CHV_VERSION>-<hash>, which is how the Cloud
# Hypervisor workflow knows whether main's build is published already.
set -euo pipefail
cd "$(dirname "$0")"
{
	printf '%s\n' build.sh Dockerfile fetch-git.sh package.sh versions.env test/render-node-path.sh
	find patches -type f
} | LC_ALL=C sort | while read -r f; do
	printf '%s\0' "$f"
	cat "$f"
done | sha256sum | cut -c1-12
