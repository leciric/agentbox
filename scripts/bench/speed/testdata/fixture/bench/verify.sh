#!/usr/bin/env bash
# verify.sh <task> prints, as one line of JSON, whether the task was done and
# what check.sh measured while it was: {"task", "ok", "reason", "changed",
# "checks": [every .bench/check-<n>.json, in order]}.
main() {
	set -u
	cd "$(dirname "$0")/.." || exit 1
	. bench/lib.sh
	local task=${1:-} ok=true reason="" f first=1
	case $task in
	edit)
		if ! grep -q 'func Title(' strutil/strutil.go; then
			ok=false reason="strutil.go has no func Title"
		elif ! grep -q 'Title(' strutil/strutil_test.go; then
			ok=false reason="strutil_test.go doesn't test Title"
		elif ! grep -q 'Title(' README.md; then
			ok=false reason="README.md doesn't list Title"
		fi
		;;
	build)
		if ! git diff --quiet HEAD -- '*_test.go'; then
			ok=false reason="a test file was changed"
		elif ! GOCACHE=$(cachedir) go test -count=1 ./stats >/dev/null 2>&1; then
			ok=false reason="go test ./stats still fails"
		fi
		;;
	*)
		ok=false reason="unknown task \"$task\""
		;;
	esac
	printf '{"task":%s,"ok":%s,"reason":%s,"changed":%s,"checks":[' \
		"$(js "$task")" "$ok" "$(js "$reason")" "$(js "$(git status --short 2>/dev/null)")"
	for f in $(ls .bench/check-*.json 2>/dev/null | sort -t- -k2 -n); do
		[ $first = 1 ] || printf ','
		first=0
		tr -d '\n' <"$f"
	done
	printf ']}\n'
}
main "$@"
exit
