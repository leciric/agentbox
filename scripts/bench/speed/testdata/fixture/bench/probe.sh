#!/usr/bin/env bash
# probe.sh prints what a build in this checkout would see (bench/lib.sh's
# probe), as one line of JSON.
cd "$(dirname "$0")/.." || exit 1
. bench/lib.sh
probe
echo
