#!/usr/bin/env bash
# memwatch.sh PID [SECONDS]: the native app's memory every SECONDS (5), one
# line each, until it exits:
#   vram_mb gtt_mb  the GPU's memory it holds: drm-memory-vram/-gtt (or
#                   drm-total-*) in /proc/PID/fdinfo, each drm-client-id once
#                   (a client opened through several fds is counted once)
#   pss_mb swap_mb  its own memory, Pss and SwapPss from /proc/PID/smaps_rollup
#                   (on a software renderer, lavapipe, its textures are in
#                   here too; under memory pressure part of it is in swap)
#   webkit webkit_mb  WebKitWebProcess processes under it, and their Pss
#   threads         its threads named ReceiveQueue (WebKit's, one per view)
set -euo pipefail
pid=${1:?usage: memwatch.sh PID [SECONDS]}
every=${2:-5}

# gpu sums drm-memory-vram/gtt over the app's DRM clients, in MiB.
gpu() {
  for f in /proc/"$pid"/fdinfo/*; do
    awk '
      /^drm-client-id:/ { id = $2 }
      /^drm-(memory|total)-(vram|gtt):/ {
        split($1, k, "-"); kind = k[3]; sub(":", "", kind)
        v = $2; u = $3
        if (u == "MiB") v *= 1024; else if (u == "GiB") v *= 1048576; else if (u == "") v /= 1024
        # drm-memory-* is the older name of drm-total-*; take either once.
        if (!(kind in seen)) { seen[kind] = 1; val[kind] = v }
      }
      END { if (id != "") printf "%s %d %d\n", id, val["vram"], val["gtt"] }
    ' "$f" 2>/dev/null || true
  done | sort -u -k1,1 | awk '{ v += $2; g += $3 } END { printf "%.0f %.0f", v / 1024, g / 1024 }'
}

# descendants lists the processes under the app.
descendants() {
  ps -eo pid=,ppid= | awk -v root="$pid" '
    { parent[$1] = $2 }
    END {
      for (p in parent) {
        q = parent[p]
        while (q != "" && q != 0 && q != 1) {
          if (q == root) { print p; break }
          q = parent[q]
        }
      }
    }'
}

pss() { awk '/^Pss:/ { s += $2 } END { printf "%.0f", s / 1024 }' "$@" 2>/dev/null || echo 0; }
swap() { awk '/^SwapPss:/ { s += $2 } END { printf "%.0f", s / 1024 }' "$@" 2>/dev/null || echo 0; }

echo "time vram_mb gtt_mb pss_mb swap_mb webkit webkit_mb threads"
while kill -0 "$pid" 2>/dev/null; do
  webkit=()
  for p in $(descendants); do
    [[ $(cat /proc/"$p"/comm 2>/dev/null) == WebKitWebProces* ]] && webkit+=("$p")
  done
  wk_pss=0
  if ((${#webkit[@]})); then
    wk_pss=$(pss $(printf '/proc/%s/smaps_rollup ' "${webkit[@]}"))
  fi
  threads=$(cat /proc/"$pid"/task/*/comm 2>/dev/null | grep -c '^ReceiveQueue' || true)
  own=/proc/"$pid"/smaps_rollup
  echo "$(date +%T) $(gpu) $(pss "$own") $(swap "$own") ${#webkit[@]} $wk_pss $threads"
  sleep "$every"
done
