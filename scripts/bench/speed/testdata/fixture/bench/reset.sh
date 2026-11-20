#!/usr/bin/env bash
# reset.sh <commit> puts this checkout back to the commit the benchmark pinned,
# removes everything untracked (what check.sh left included) and empties the
# checkout's Go build cache, so the next run starts where the first one did.
main() {
	set -eu
	cd "$(dirname "$0")/.."
	. bench/lib.sh
	git reset -q --hard "$1"
	git clean -qfdx
	chmod -R u+w "$(cachedir)" 2>/dev/null || true
	rm -rf "$(cachedir)"
}
main "$@"
exit
