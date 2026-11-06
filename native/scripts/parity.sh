#!/usr/bin/env bash
# Pixel parity between the Electron app and the native one.
#
# Both apps run against the same fake-daemon (FAKE_STILL=1: fixtures that
# don't change on their own), each scenario of parity-scenarios.json is opened
# in both at 1440x900 and 1920x1080, dark and light, and the two screenshots
# are compared by parity_compare.py: a per-pixel similarity with a 1px
# neighbourhood and a ±24/255 channel tolerance for antialiasing (not SSIM;
# see that file). native/parity-out/ gets <scenario>-<theme>-<size>.png, the
# Electron screenshot, the native one and their diff side by side.
#
#   native/scripts/parity.sh [id-filter]
#
# PARITY_NO_BUILD=1 skips building; PARITY_SIZES / PARITY_THEMES narrow the
# matrix ("1440x900", "dark"). Exits 1 if any scenario scores under 0.99.
#
# Electron runs on its own Xvfb display, the native app on another whose
# screen is exactly the window, with no window manager, so a root-window grab
# is the window's contents. Both run with reduced motion and no caret.
set -euo pipefail

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
mkdir -p "$out" "$tmp"
export PATH=$HOME/.cargo/bin:$PATH
export CARGO_BUILD_JOBS=${CARGO_BUILD_JOBS:-2}

for tool in Xvfb xdotool import python3 node; do
  command -v $tool >/dev/null || { echo "parity: needs $tool (apt-get install xvfb xdotool imagemagick python3-numpy python3-pil)" >&2; exit 2; }
done

if [ -z "${PARITY_NO_BUILD:-}" ]; then
  (cd "$native" && cargo build -q -p agentbox-native -p fake-daemon)
  (cd "$root/desktop" && node scripts/build.mjs >/dev/null)
fi
bin=$native/target/debug

pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$tmp"
}
trap cleanup EXIT

# One display for Electron, as big as the biggest size.
Xvfb :91 -screen 0 1920x1080x24 -nolisten tcp >/dev/null 2>&1 &
pids+=($!)

# The scenarios, flattened for the shell: id, the native app's env, actions.
mapfile -t rows < <(python3 - "$scenarios" "$filter" <<'EOF'
import json, sys
for s in json.load(open(sys.argv[1])):
    if sys.argv[2] and sys.argv[2] not in s["id"]:
        continue
    env = " ".join(f"{k}={v}" for k, v in s.get("native", {}).items())
    acts = []
    for a in s.get("actions", []):
        if "hover" in a: acts.append(f"mousemove {a['hover'][0]} {a['hover'][1]}")
        if "click" in a: acts.append(f"mousemove {a['click'][0]} {a['click'][1]} click 1")
        if "press" in a: acts.append("key " + a["press"].replace("Control", "ctrl").replace("Escape", "Escape").lower().replace("escape", "Escape"))
        if "type" in a: acts.append(f"type {json.dumps(a['type'])}")
        acts.append(f"sleep {a.get('wait', 700) / 1000}")
    masks = " ".join("--mask " + ",".join(map(str, m)) for m in s.get("masks", []))
    print("\t".join([s["id"], env, ";".join(acts), str(s.get("wait", 1500) / 1000 + 1.5), masks]))
EOF
)

fails=0
report=$out/scores.txt
: >"$report"
for theme in $themes; do
  sock=$tmp/fake-$theme.sock
  FAKE_STILL=1 FAKE_APPEARANCE=$theme "$bin/fake-daemon" --listen "$sock" --history 12 >"$tmp/fake-$theme.log" 2>&1 &
  pids+=($!)
  for _ in $(seq 50); do [ -S "$sock" ] && break; sleep 0.1; done
  for size in $sizes; do
    w=${size%x*} h=${size#*x}
    edir=$tmp/electron-$theme-$size
    DISPLAY=:91 node "$here/parity-electron.mjs" "$sock" "$size" "$scenarios" "$edir" "$filter" >/dev/null
    # The native app's own display, exactly the window.
    Xvfb :92 -screen 0 "${w}x${h}x24" -nolisten tcp >/dev/null 2>&1 &
    xvfb=$!
    sleep 0.5
    for row in "${rows[@]}"; do
      IFS=$'\t' read -r id env acts wait masks <<<"$row"
      prefs=$tmp/prefs-$id.json
      echo '{}' >"$prefs"
      # shellcheck disable=SC2086
      env DISPLAY=:92 WAYLAND_DISPLAY= AGENTBOX_NATIVE_SIZE="$size" AGENTBOX_REDUCE_MOTION=1 \
        AGENTBOX_NATIVE_PREFS="$prefs" VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json \
        $env "$bin/agentbox-native" --socket "$sock" >"$tmp/native-$id.log" 2>&1 &
      app=$!
      sleep "$wait"
      IFS=';' read -ra steps <<<"$acts"
      for step in "${steps[@]}"; do
        # shellcheck disable=SC2086
        case $step in
          sleep*) sleep ${step#sleep } ;;
          type*) DISPLAY=:92 xdotool type --delay 20 "$(python3 -c 'import json,sys;print(json.loads(sys.argv[1]))' "${step#type }")" ;;
          *) DISPLAY=:92 xdotool $step ;;
        esac
      done
      DISPLAY=:92 import -window root "$tmp/native-$id.png"
      kill $app 2>/dev/null || true
      wait $app 2>/dev/null || true
      name=$id-$theme-$size
      # shellcheck disable=SC2086
      score=$(python3 "$here/parity_compare.py" "$edir/$id.png" "$tmp/native-$id.png" "$out/$name" $masks)
      verdict=ok
      if python3 -c "import sys; sys.exit(0 if float('$score') >= $threshold else 1)"; then :; else verdict=FAIL; fails=$((fails + 1)); fi
      printf '%-48s %s %s\n' "$name" "$score" "$verdict" | tee -a "$report"
    done
    kill $xvfb 2>/dev/null || true
    wait $xvfb 2>/dev/null || true
  done
done

echo "parity: $fails scenario(s) under $threshold; side-by-sides in $out"
[ "$fails" -eq 0 ]
