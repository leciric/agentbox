#!/usr/bin/env bash
# Pixel parity between the Electron app and the native one.
#
# Both apps run against the same fake-daemon (FAKE_STILL=1: fixtures that
# don't change on their own, dated from one fixed instant), each scenario of
# parity-scenarios.json is opened in both at 1440x900 and 1920x1080 (or the
# scenario's own "sizes"), dark and light, and the two screenshots are
# compared by parity_compare.py: a per-pixel similarity with a 1px
# neighbourhood and a ±24/255 channel tolerance for antialiasing (not SSIM;
# see that file). native/parity-out/ gets <scenario>-<theme>-<size>.png, the
# Electron screenshot, the native one and their diff side by side, and
# scores.txt.
#
#   native/scripts/parity.sh [id-filter]
#
# PARITY_NO_BUILD=1 skips building; PARITY_SIZES / PARITY_THEMES narrow the
# matrix ("1440x900", "dark"). Exits 1 if any scenario scores under 0.99.
#
# Electron (the real app, desktop/out) stays open on its own Xvfb display and
# plays each scenario when told to (parity-electron.mjs), in step with the
# native app, which is launched per scenario on a display exactly its window
# (no window manager: a root-window grab is the window), so both screenshots
# are taken within a second or two: the apps' own clocks run. Both run with
# reduced motion and no caret.
# Read whole before it runs: bash reads a script as it goes, so an edit to
# this file during a run would otherwise break the run.
{
set -euo pipefail

# One run at a time: a second waits for the first rather than kill its
# displays (each run clears leftovers of a stopped one).
exec 9>"${TMPDIR:-/tmp}/parity.lock"
flock 9

here=$(cd "$(dirname "$0")" && pwd)
native=$(dirname "$here")
root=$(dirname "$native")
out=$native/parity-out
filter=${1:-}
threshold=0.99
sizes=${PARITY_SIZES:-"1440x900 1920x1080"}
themes=${PARITY_THEMES:-"dark light"}
scenarios=$here/parity-scenarios.json
tmp=${TMPDIR:-/tmp}/parity.$$
mkdir -p "$out/logs" "$tmp"
: >"$out/logs/retries.log"
export PATH=$HOME/.cargo/bin:$PATH
export CARGO_BUILD_JOBS=${CARGO_BUILD_JOBS:-2}

for tool in Xvfb xdotool import convert python3 node; do
  command -v $tool >/dev/null || { echo "parity: needs $tool (apt-get install xvfb xdotool imagemagick python3-numpy python3-pil)" >&2; exit 2; }
done

if [ -z "${PARITY_NO_BUILD:-}" ]; then
  (cd "$native" && cargo build -q --profile parity -p agentbox-native -p fake-daemon)
  (cd "$root/desktop" && node scripts/build.mjs >/dev/null)
fi
bin=$native/target/parity

pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  # The passes' displays, apps and fakes.
  pkill -f 'Xvfb :9[1-4] ' 2>/dev/null || true
  # The apps only: a rustc building into the same directory is left alone.
  pkill -f "$bin/agentbox-native" 2>/dev/null || true
  pkill -f "$bin/fake-daemon" 2>/dev/null || true
  pkill -f "$here/parity-electron.mjs" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

# Leftovers of a run that was stopped: its displays and apps.
pkill -f 'Xvfb :9[1-4] ' 2>/dev/null || true
pkill -f 'target/parity/agentbox-native' 2>/dev/null || true
rm -f /tmp/.X9[1-4]-lock
sleep 0.3


# The scenarios, flattened for the shell (\x1f between fields): id, its own
# sizes, the native app's env, its actions for xdotool, its wait, its masks.
rows() {
  python3 - "$scenarios" "$filter" <<'EOF'
import json, sys
for s in json.load(open(sys.argv[1])):
    if sys.argv[2] and sys.argv[2] not in s["id"]:
        continue
    env = " ".join(f"{k}={v}" for k, v in s.get("native", {}).items())
    acts = []
    for a in s.get("actions", []):
        if "hover" in a: acts.append(f"mousemove {a['hover'][0]} {a['hover'][1]}")
        if "click" in a: acts.append(f"mousemove {a['click'][0]} {a['click'][1]} click 1")
        if "press" in a: acts.append("key " + a["press"].replace("Control", "ctrl").replace("Shift", "shift").replace("+/", "+slash"))
        if "type" in a: acts.append("type " + json.dumps(a["type"]))
        if "wheel" in a: acts.append(f"mousemove {a['wheel'][0]} {a['wheel'][1]} click --repeat {a['wheel'][2]} --delay 10 5")
        acts.append(f"sleep {a.get('wait', 700) / 1000}")
    # masks: [[x,y,w,h]...] at every size, or {"1440x900": [...]} by size.
    ms = s.get("masks", [])
    if isinstance(ms, dict):
        ms = [(m, size) for size, l in ms.items() for m in l]
    else:
        ms = [(m, "") for m in ms]
    masks = " ".join("--mask " + ",".join(map(str, m)) + (f"@{size}" if size else "") for m, size in ms)
    print("\x1f".join([s["id"], " ".join(s.get("sizes", [])), env, ";".join(acts), str(s.get("wait", 1500) / 1000), masks]))
EOF
}
mapfile -t rows < <(rows)

# Every size some scenario is drawn at.
all_sizes=$(printf '%s\n' "${rows[@]}" | python3 -c '
import sys
default = sys.argv[1].split()
seen = []
for line in sys.stdin:
    own = line.split("\x1f")[1].split()
    for s in own or default:
        if s not in seen: seen.append(s)
print(" ".join(seen))' "$sizes")

# pass runs every scenario in one theme, on its own pair of displays (one for
# Electron, one for the native app), so the themes run side by side.
pass() {
  local theme=$1 edisp=$2 ndisp=$3
  local sock=$tmp/fake-$theme.sock report=$tmp/scores-$theme.txt
  : >"$report"
  FAKE_STILL=1 FAKE_APPEARANCE=$theme "$bin/fake-daemon" --listen "$sock" --history 12 >"$out/logs/fake-$theme.log" 2>&1 &
  local fake=$!
  for _ in $(seq 50); do [ -S "$sock" ] && break; sleep 0.1; done
  Xvfb ":$edisp" -screen 0 2560x1440x24 -nolisten tcp >/dev/null 2>&1 &
  local exvfb=$!
  for size in $all_sizes; do
    local w=${size%x*} h=${size#*x}
    local edir=$tmp/electron-$theme-$size
    mkdir -p "$edir"
    coproc ELECTRON { DISPLAY=":$edisp" exec node "$here/parity-electron.mjs" "$sock" "$size" "$scenarios" "$edir" "$filter" 2>"$out/logs/electron-$theme-$size.log"; }
    local epid=$ELECTRON_PID
    local line
    read -r line <&"${ELECTRON[0]}" # ready
    Xvfb ":$ndisp" -screen 0 "${w}x${h}x24" -nolisten tcp >/dev/null 2>&1 &
    local xvfb=$!
    sleep 0.5
    local row id own env acts wait masks
    for row in "${rows[@]}"; do
      IFS=$'\x1f' read -r id own env acts wait masks <<<"$row"
      if [ -n "$own" ]; then
        case " $own " in *" $size "*) ;; *) continue ;; esac
      else
        case " $sizes " in *" $size "*) ;; *) continue ;; esac
      fi
      local attempt score
      for attempt in 1 2; do
        local name=$id-$theme-$size
        local prefs=$tmp/prefs-$theme-$id.json
        echo '{"tour":"dismissed"}' >"$prefs"
        # shellcheck disable=SC2086
        env DISPLAY=":$ndisp" WAYLAND_DISPLAY= AGENTBOX_NATIVE_SIZE="$size" AGENTBOX_REDUCE_MOTION=1 \
          AGENTBOX_NATIVE_PREFS="$prefs" VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json \
          $env "$bin/agentbox-native" --socket "$sock" >"$out/logs/native-$name.log" 2>&1 &
        local app=$!
        # Until the window has drawn something (the first launch on a fresh
        # display compiles lavapipe's shaders), and its data has come.
        for _ in $(seq 80); do
          DISPLAY=":$ndisp" import -window root "$tmp/probe-$theme.png" 2>/dev/null &&
            [ "$(convert "$tmp/probe-$theme.png" -format '%[fx:standard_deviation>0.01]' info: 2>/dev/null)" = 1 ] && break
          sleep 0.25
        done
        # gpui maps the window at x=2 with no window manager to place it:
        # put it where Electron's is, at the origin.
        DISPLAY=":$ndisp" xdotool search --name '^AgentBox$' windowmove 0 0 2>/dev/null || true
        # The pointer where Electron's is (parity-electron.mjs moves it to the
        # bottom-right corner), not Xvfb's centre, which hovers whatever is there.
        DISPLAY=":$ndisp" xdotool mousemove $((${size%x*} - 2)) $((${size#*x} - 2)) 2>/dev/null || true
        # A scenario that clicks or types waits longer for its data first.
        case $acts in *click*|*key*|*type*) sleep 3 ;; *) sleep 1 ;; esac
        # Now both: Electron opens the scenario as the native app plays it.
        echo "$id" >&"${ELECTRON[1]}"
        sleep "$wait"
        local steps step
        IFS=';' read -ra steps <<<"$acts"
        for step in "${steps[@]}"; do
          # shellcheck disable=SC2086
          case $step in
            sleep*) sleep ${step#sleep } ;;
            type*) DISPLAY=":$ndisp" xdotool type --delay 20 "$(python3 -c 'import json,sys;print(json.loads(sys.argv[1]))' "${step#type }")" ;;
            *) DISPLAY=":$ndisp" xdotool $step ;;
          esac
        done
        DISPLAY=":$ndisp" import -window root "$tmp/native-$name.png"
        read -r line <&"${ELECTRON[0]}" # done <id>
        kill $app 2>/dev/null || true
        wait $app 2>/dev/null || true
        # shellcheck disable=SC2086
        score=$(python3 "$here/parity_compare.py" "$edir/$id.png" "$tmp/native-$name.png" "$out/$name" $masks 2>>"$out/logs/compare.log" || echo 0)
        # Once more if it failed: a launch can lose a race with its own data
        # under load (a click that lands before the page is there). Retries
        # are logged in logs/retries.log.
        python3 -c "import sys; sys.exit(0 if float('$score') >= $threshold else 1)" && break
        [ "$attempt" = 1 ] && echo "$name $score" >>"$out/logs/retries.log"
      done
      printf '%-48s %s\n' "$name" "$score" >>"$report"
      [ -f "$edir/$id.dom.json" ] && cp "$edir/$id.dom.json" "$out/$name.dom.json"
    done
    exec {ELECTRON[1]}>&-
    wait "$epid" 2>/dev/null || true
    kill $xvfb 2>/dev/null || true
    wait $xvfb 2>/dev/null || true
  done
  kill $exvfb $fake 2>/dev/null || true
  wait
}

i=0
jobs_=()
for theme in $themes; do
  pass "$theme" $((91 + 2 * i)) $((92 + 2 * i)) &
  jobs_+=($!)
  i=$((i + 1))
done
for j in "${jobs_[@]}"; do wait "$j" || true; done

fails=0
report=$out/scores.txt
: >"$report"
for theme in $themes; do
  while read -r name score; do
    verdict=ok
    if ! python3 -c "import sys; sys.exit(0 if float('$score') >= $threshold else 1)"; then
      verdict=FAIL
      fails=$((fails + 1))
    fi
    printf '%-48s %s %s\n' "$name" "$score" "$verdict" | tee -a "$report"
  done < <(sort "$tmp/scores-$theme.txt")
done

echo "parity: $fails scenario(s) under $threshold; side-by-sides in $out"
[ "$fails" -eq 0 ]
exit
}
