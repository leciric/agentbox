#!/usr/bin/env bash
# check.sh vets, builds and tests this repository. Its Go build cache is this
# checkout's own and starts empty, so the first run compiles everything, the
# standard library included, and a later one only what changed since; every
# test runs every time (-count=1).
#
# Each run also leaves what it measured, and what the machine it ran on looked
# like, in .bench/check-<n>.json, for the benchmark that this repository is the
# fixture of.
main() {
	set -uo pipefail
	cd "$(dirname "$0")" || exit 1
	. bench/lib.sh
	mkdir -p .bench out
	local n env t0 t1 t2 t3 vet build test status
	n=$(($(ls .bench/check-*.json 2>/dev/null | wc -l) + 1))
	env=$(probe)
	export GOCACHE
	GOCACHE=$(cachedir)

	t0=$(now_ms)
	echo "== go vet ./..."
	go vet ./...
	vet=$?
	t1=$(now_ms)
	echo "== go build -o out/ ./cmd/..."
	go build -o out/ ./cmd/...
	build=$?
	t2=$(now_ms)
	echo "== go test -count=1 ./..."
	go test -count=1 ./...
	test=$?
	t3=$(now_ms)

	status=0
	[ $vet = 0 ] && [ $build = 0 ] && [ $test = 0 ] || status=1
	printf '{"n":%s,"start":%s,"vetMs":%s,"buildMs":%s,"testMs":%s,"totalMs":%s,"vetExit":%s,"buildExit":%s,"testExit":%s,"exit":%s,"env":%s}\n' \
		"$n" "$t0" $((t1 - t0)) $((t2 - t1)) $((t3 - t2)) $((t3 - t0)) "$vet" "$build" "$test" "$status" "$env" >".bench/check-$n.json"
	if [ $status = 0 ]; then
		echo "check.sh: PASS (vet $((t1 - t0)) ms, build $((t2 - t1)) ms, test $((t3 - t2)) ms)"
	else
		echo "check.sh: FAIL (vet exit $vet, build exit $build, test exit $test)"
	fi
	return $status
}
main "$@"
exit
