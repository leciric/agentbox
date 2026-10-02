#!/usr/bin/env bash
# bench-package-cache.sh times what a fresh agent's installs cost with the
# shared package caches cold (empty, the first agent) and warm (filled by an
# earlier agent), the way agents use them: the environment
# agent/pkgcache.go writes, the caches on a mount of their own as the Incus
# disk device puts them, so pnpm copies from its store rather than
# hard-linking, and a new HOME and project copy for every run.
#
#   sudo true && scripts/bench-package-cache.sh [project-with-package.json]
#
# It installs the project's dependencies with pnpm and npm (scripts off, so
# only fetching and linking is timed), Playwright's Chromium when the project
# uses Playwright, and downloads this repository's Go modules. Needs sudo for the bind mount, pnpm, npm and go on PATH.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
project=${1:-$repo/desktop}
work=$(mktemp -d "${TMPDIR:-/tmp}/bench-pkgcache.XXXXXX")
cache=/var/cache/agentbox/packages
backing=$work/backing
mkdir -p "$backing"
sudo mkdir -p "$cache"
if mountpoint -q "$cache"; then
	echo "$cache is mounted already: run this outside an agent with the caches on" >&2
	exit 1
fi
sudo mount --bind "$backing" "$cache"
cleanup() {
	sudo umount "$cache" || true
	chmod -R u+w "$work" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

# The same variables an agent's env file exports.
export npm_config_cache=$cache/npm pnpm_config_store_dir=$cache/pnpm/store \
	pnpm_config_cache_dir=$cache/pnpm/cache COREPACK_HOME=$cache/corepack \
	GOMODCACHE=$cache/go/mod GOCACHE=$cache/go/build GOFLAGS=-modcacherw \
	PLAYWRIGHT_BROWSERS_PATH=$cache/ms-playwright PLAYWRIGHT_SKIP_BROWSER_GC=1
export npm_config_audit=false npm_config_fund=false npm_config_update_notifier=false

# fresh gives a run a new HOME and a copy of the project in $p, as a new
# agent has.
fresh() {
	local dir
	dir=$(mktemp -d "$work/run.XXXXXX")
	mkdir -p "$dir/home" "$dir/project"
	cp "$project/package.json" "$dir/project/"
	if [ -f "$project/package-lock.json" ]; then cp "$project/package-lock.json" "$dir/project/"; fi
	export HOME=$dir/home npm_config_logs_dir=$dir/home/.npm/_logs
	p=$dir/project
}

seconds() {
	local start end
	start=$(date +%s.%N)
	"$@" >"$work/last.log" 2>&1 || {
		tail -20 "$work/last.log" >&2
		return 1
	}
	end=$(date +%s.%N)
	awk -v a="$start" -v b="$end" 'BEGIN { print b - a }'
}

# pnpm needs its own lockfile: made once, from npm's, outside the timing.
fresh
(cd "$p" && pnpm import >/dev/null 2>&1 && cp pnpm-lock.yaml "$work/pnpm-lock.yaml")
sudo find "$backing" -mindepth 1 -delete

pnpm_install() {
	fresh
	cp "$work/pnpm-lock.yaml" "$p/"
	cd "$p" && seconds pnpm install --frozen-lockfile --ignore-scripts
}
npm_ci() {
	fresh
	cd "$p" && seconds npm ci --ignore-scripts
}
playwright_install() {
	fresh
	cp "$work/pnpm-lock.yaml" "$p/"
	cd "$p" && pnpm install --frozen-lockfile --ignore-scripts >/dev/null 2>&1 &&
		seconds ./node_modules/.bin/playwright install chromium
}
go_download() {
	fresh
	cd "$repo" && seconds go mod download
}

report() {
	printf '%-40s cold %6.1fs   warm %6.1fs\n' "$1" "$2" "$3"
}

cold=$(pnpm_install)
warm=$(pnpm_install)
report "pnpm install ($(basename "$project"))" "$cold" "$warm"
cold=$(npm_ci)
warm=$(npm_ci)
report "npm ci ($(basename "$project"))" "$cold" "$warm"
if grep -q '"playwright"' "$project/package.json"; then
	cold=$(playwright_install)
	warm=$(playwright_install)
	report "playwright install chromium" "$cold" "$warm"
fi
cold=$(go_download)
warm=$(go_download)
report "go mod download (agentbox)" "$cold" "$warm"
echo "caches hold $(du -sh "$backing" | cut -f1)"
