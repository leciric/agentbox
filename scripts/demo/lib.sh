# Helpers shared by the demo scripts. Source it after setting AB (the
# agentbox binary) and work (the throwaway directory).

results=()

# The CLI starts a daemon for the throwaway state; stop it before deleting that state.
finish() {
  "$AB" daemon stop >/dev/null 2>&1
  rm -rf "$work"
}
trap finish EXIT

section() { printf '\n\n######## %s\n' "$*"; }

# ab runs agentbox, echoing the command and hiding the throwaway path.
ab() {
  printf '\n$ agentbox %s\n' "$*"
  "$AB" "$@" 2>&1 | sed "s#$work#\$TMP#g"
  local rc=${PIPESTATUS[0]}
  [[ $rc -eq 0 ]] || echo "(exit $rc)"
  return 0
}

# q runs agentbox quietly, for checks.
q() { "$AB" "$@" 2>/dev/null; }

host() {
  printf '\n$ %s\n' "$*" | sed "s#$work#\$TMP#g"
  eval "$*" 2>&1 | sed "s#$work#\$TMP#g"
}

expect() { if [[ "$2" == "$3" ]]; then results+=("PASS  $1"); else results+=("FAIL  $1 (got: '$2', want: '$3')"); fi; }
expect_match() { if [[ "$2" =~ $3 ]]; then results+=("PASS  $1"); else results+=("FAIL  $1 (got: '$2')"); fi; }

# seconds runs a command and prints how long it took.
seconds() {
  local start=$EPOCHREALTIME
  "$@" >/dev/null 2>&1
  awk -v s="$start" -v e="$EPOCHREALTIME" 'BEGIN { printf "%.1f", e - s }'
}

ip_of() { incus query "/1.0/instances/$1/state" | jq -r '.network.eth0.addresses[] | select(.family=="inet") | .address'; }
wait_http() { for _ in $(seq 1 60); do curl -sf --max-time 2 "$1" >/dev/null && return; sleep 1; done; }

# fixture_repo copies testdata/fixtures/<name> into a new git repository at <dir>.
fixture_repo() {
  mkdir -p "$(dirname "$2")"
  cp -r "testdata/fixtures/$1" "$2"
  git -C "$2" init -q -b main
  git -C "$2" add -A
  git -C "$2" -c commit.gpgsign=false commit -qm "$1 fixture"
}

summary() {
  section "Summary"
  printf '%s\n' "${results[@]}"
  [[ ! " ${results[*]} " =~ " FAIL " ]] && ! printf '%s\n' "${results[@]}" | grep -q '^FAIL'
}
