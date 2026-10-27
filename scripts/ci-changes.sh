#!/usr/bin/env bash
# Which of CI's jobs a change can break, from the files it changes: the
# `changes` job in .github/workflows/ci.yml runs it, and prints its answer as
# key=value lines for $GITHUB_OUTPUT. Anything it can't place runs.
#
#   scripts/ci-changes.sh <file listing the changed paths, one per line>
#
# PLATFORMS=true (push to main, a PR labelled ci:full) also works out whether
# the macOS and Windows jobs can be affected, which needs Go for `go list`;
# otherwise they're false, as CI only runs them there.
set -euo pipefail

files=$1
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

has() { grep -qE "$1" "$files"; }
out() { echo "$1=$2"; }

# A change to how CI decides, or to the tools' versions, runs everything.
if has '^(\.github/workflows/ci\.yml|scripts/ci-changes\.sh)$'; then
  for k in go desktop mobile hub vuln macos windows; do out "$k" true; done
  exit 0
fi

# Go is everything the Go build, its tests, vet and lint can read: anything
# but the paths below, which nothing in Go embeds, opens or runs. Each Go
# package's own directory is Go whatever it holds (go:embed takes .md, .sh,
# .tmpl and .txt files, and tests read testdata/). desktop/src/shared/api.ts
# is Go's too: TestTypeScriptTypesAreUpToDate reads it.
not_go='^(desktop/|mobile/|docs/|promo/|packaging/|scripts/|\.github/|[^/]+\.md$|LICENSE$|\.gitignore$|\.release-please-manifest\.json$|hub/(npm/|scripts/|[^/]+\.md$))'
go_too='^(desktop/src/shared/api\.ts|scripts/check-coverage\.sh|\.github/coverage-floor\.txt)$'
go=false
if grep -qvE "$not_go" "$files" || has "$go_too"; then go=true; fi

# The desktop app, and the vnc.js its build writes into the Go tree.
desktop=false
if has '^(desktop/|internal/machinesweb/static/vnc\.js$|mise\.toml$)'; then desktop=true; fi

# The phone app, and what it imports of the desktop's: the API's types and
# the text's catalogs.
mobile=false
if has '^(mobile/|desktop/src/shared/(api\.ts|i18n/)|mise\.toml$)'; then mobile=true; fi

# The hub's npm packages (npx agentbox-hub): its Go, the protocol it imports,
# its launcher and build script, and the toolchain that builds them. Its Go
# tests run with everyone's, in the Go jobs.
hub=false
if has '^(hub/(cmd|internal|npm|scripts)/|hubapi/|go\.(mod|sum)$|mise\.toml$)'; then hub=true; fi

vuln=false
if has '^go\.(mod|sum)$'; then vuln=true; fi

# macOS and Windows run a few packages' tests, build cmd/agentbox and run
# it. Go vet, lint, tidy builds and vets for both on Linux, so what only they
# can catch is in those packages, everything those packages import, the
# binary's own package, and the files only that OS builds: a change elsewhere
# can't reach them.
macos=false windows=false
if [ "${PLATFORMS:-false}" = true ] && $go; then
  always='^(go\.(mod|sum)|mise\.toml|\.gitattributes|cmd/.*)$'
  if has "$always"; then
    macos=true windows=true
  else
    # Every directory that holds a Go package, so that a file is placed in
    # the package it belongs to (its embedded files, its testdata/).
    pkgdirs=$(git ls-files '*.go' | sed -E 's#/[^/]+$##; /\.go$/s#.*#.#' | sort -u)
    deps() { go list -deps -test -f '{{if not .Standard}}{{.Dir}}{{end}}' "$@" | sed "s#^$root/##; s#^$root\$#.#" | sort -u; }
    if ! mac_deps=$(GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 deps ./internal/hostvm/... ./internal/paths ./internal/api) \
      || ! win_deps=$(GOOS=windows GOARCH=amd64 deps ./internal/hostos ./internal/hostwsl/...); then
      echo "go list failed: running macOS and Windows" >&2
      macos=true windows=true
    else
      while IFS= read -r f; do
        [ -n "$f" ] || continue
        case "$f" in
          scripts/mac-sign.sh | scripts/agentbox.entitlements) macos=true; continue ;;
        esac
        case "$f" in
          desktop/src/shared/api.ts) f=internal/api/api.ts ;; # read by internal/api's test
          scripts/check-coverage.sh | .github/coverage-floor.txt) continue ;;
        esac
        grep -qE "$not_go" <<<"$f" && continue
        # Files only one OS builds, wherever they are.
        if [[ "$f" == *.go ]]; then
          if [[ "$f" =~ _(darwin|ios|bsd|unix)(_test)?\.go$ ]] || { [ -f "$f" ] && grep -qE '^//go:build .*(darwin|unix)|^import "C"' "$f"; }; then macos=true; fi
          if [[ "$f" =~ _windows(_test)?\.go$ ]] || { [ -f "$f" ] && grep -qE '^//go:build .*windows' "$f"; }; then windows=true; fi
        fi
        # The package the file belongs to: its own directory or the nearest
        # above it with Go in it. None (a deleted package, testdata/ at the
        # root) counts as both.
        dir=$(dirname "$f")
        while [ "$dir" != . ] && ! grep -qxF "$dir" <<<"$pkgdirs"; do dir=$(dirname "$dir"); done
        if [ "$dir" = . ] && ! grep -qxF . <<<"$pkgdirs"; then
          [[ "$f" == */* ]] || continue # .golangci.yml and the like, at the root
          macos=true windows=true
          continue
        fi
        grep -qxF "$dir" <<<"$mac_deps" && macos=true
        grep -qxF "$dir" <<<"$win_deps" && windows=true
      done <"$files"
    fi
  fi
fi

out go "$go"
out desktop "$desktop"
out mobile "$mobile"
out hub "$hub"
out vuln "$vuln"
out macos "$macos"
out windows "$windows"
