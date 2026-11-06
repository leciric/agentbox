#!/usr/bin/env bash
# disk-report.sh: what AgentBox takes on disk, and how long the host's disk
# takes to write and fsync, for before/after numbers. Read-only: it writes
# nothing but the probe's own temporary file, which it removes.
#
# Run it on the host, in AgentBox's VM, or both; each side reports what it can
# see. In the VM, pass --host-data with the host's data directory as the VM
# sees it (the shared home) to get the disk images too.
#
#   scripts/disk-report.sh                       # the space table
#   scripts/disk-report.sh --probe 60            # and 60 s of 4 KiB write+fdatasync
#   scripts/disk-report.sh --host-data /home/me/.local/share/agentbox --probe 60 --probe-dir /home/me
#   scripts/disk-report.sh --detail              # per-agent machines (slow on a big pool)
#
# Sizes are bytes allocated on disk (du), not apparent sizes, except where a
# row says "apparent". On btrfs, du counts a reflinked extent once per file
# that shares it; the pool's own total (btrfs) counts it once.
set -uo pipefail

host_data=""
probe=0
probe_dir=""
detail=0
while [ $# -gt 0 ]; do
	case "$1" in
	--host-data) host_data="$2"; shift 2 ;;
	--probe) probe="$2"; shift 2 ;;
	--probe-dir) probe_dir="$2"; shift 2 ;;
	--detail) detail=1; shift ;;
	-h | --help) sed -n '2,17p' "$0"; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done

SUDO=""
[ "$(id -u)" -ne 0 ] && sudo -n true 2>/dev/null && SUDO="sudo -n"

gib() { awk -v b="${1-}" 'BEGIN { if (b == "" || b < 0) print "-"; else printf "%.2f", b / 1073741824 }'; }
row() { printf '  %-44s %10s GiB  %s\n' "$1" "$(gib "$2")" "${3:-}"; }
alloc() { [ -e "$1" ] && timeout 300 $SUDO du -sxB1 "$1" 2>/dev/null | awk '{print $1}'; }
apparent() { [ -e "$1" ] && timeout 300 $SUDO du -sxB1 --apparent-size "$1" 2>/dev/null | awk '{print $1}'; }

data="${AGENTBOX_DATA:-$HOME/.local/share/agentbox}"
in_vm=0
[ -b /dev/disk/by-id/virtio-agentbox-pool ] && in_vm=1
[ -z "$host_data" ] && [ "$in_vm" -eq 0 ] && host_data="$data"

echo "disk-report $(date -u +%FT%TZ) on $(hostname) ($([ "$in_vm" -eq 1 ] && echo "AgentBox's VM" || echo host))"

pool_alloc=""
if [ -n "$host_data" ] && [ -d "$host_data/vm" ]; then
	echo
	echo "Host: the VM's files ($host_data/vm)"
	for d in "$host_data"/vm/*/; do
		[ -f "$d/pool.raw" ] || [ -f "$d/root.raw" ] || continue
		n=$(basename "$d")
		for img in pool.raw root.raw seed.img; do
			[ -f "$d/$img" ] || continue
			a=$(alloc "$d/$img")
			row "$n/$img" "$a" "apparent $(gib "$(apparent "$d/$img")") GiB"
			[ "$img" = pool.raw ] && pool_alloc=$a
		done
		row "$n/ (logs, ssh, the rest)" "$(( $(alloc "$d") - $(alloc "$d/pool.raw" || echo 0) - $(alloc "$d/root.raw" || echo 0) ))"
	done
	row "vm/cache (downloaded disks, Debian images)" "$(alloc "$host_data/vm/cache")"
	row "vm/bin" "$(alloc "$host_data/vm/bin")"
fi
if [ -n "$host_data" ] && [ -d "$host_data" ]; then
	echo
	echo "Host: the shared home ($host_data)"
	wt="${AGENTBOX_WORKTREES:-$host_data/worktrees}"
	row "worktrees" "$(alloc "$wt")"
	row "worktrees-trash" "$(alloc "$(dirname "$wt")/worktrees-trash")"
	row "media" "$(alloc "${AGENTBOX_MEDIA:-$host_data/media}")"
	# What builds leave in worktrees: target/, .next/, dist/ and the like.
	if [ -d "$wt" ]; then
		for out in target .next .turbo .gradle build dist .cache; do
			b=$(timeout 300 find "$wt" -mindepth 3 -maxdepth 6 -type d -name "$out" -prune -print0 2>/dev/null |
				xargs -0r du -sxB1 2>/dev/null | awk '{s += $1} END {print s + 0}')
			[ "${b:-0}" -gt 0 ] && row "  in worktrees: $out/" "$b"
		done
	fi
	if [ "$in_vm" -eq 0 ]; then
		row "state.db" "$(alloc "$host_data/state.db")"
		row "daemon.log" "$(alloc "$host_data/daemon.log")"
	fi
	hf=$(df -B1 --output=avail "$host_data" 2>/dev/null | tail -1)
	row "free on that disk" "$hf"
fi

if [ "$in_vm" -eq 1 ] || [ -d /var/lib/incus ]; then
	echo
	echo "VM: Incus's pool and AgentBox's data"
	mnt=$(findmnt -nro TARGET -S /dev/disk/by-id/virtio-agentbox-pool 2>/dev/null | head -1)
	[ -z "$mnt" ] && mnt=$(findmnt -nro TARGET -t btrfs 2>/dev/null | grep -m1 storage-pools)
	pool_used=""
	if [ -n "$mnt" ]; then
		pool_used=$($SUDO btrfs filesystem usage -b "$mnt" 2>/dev/null | awk '/^ *Used:/ {print $2; exit}')
		pool_size=$($SUDO btrfs filesystem usage -b "$mnt" 2>/dev/null | awk '/Device size:/ {print $3; exit}')
		row "pool used (btrfs, shared extents once)" "$pool_used" "of $(gib "$pool_size") GiB, $(findmnt -nro OPTIONS "$mnt" | tr ',' '\n' | grep -E '^(discard|nodiscard)' | tr '\n' ' ')"
		if [ -n "$pool_alloc" ] && [ -n "$pool_used" ]; then
			row "pool.raw beyond what the pool uses (untrimmed)" "$((pool_alloc - pool_used))"
		fi
	fi
	pd=/var/lib/incus/storage-pools/default
	for sub in containers images custom; do
		[ -d "$pd/$sub" ] || continue
		if [ "$sub" = containers ] && [ "$detail" -eq 0 ]; then
			row "incus $sub ($(ls "$pd/$sub" | wc -l), --detail for each)" ""
			continue
		fi
		if command -v btrfs >/dev/null; then
			$SUDO timeout 600 btrfs filesystem du -s --raw "$pd/$sub"/* 2>/dev/null | awk 'NR > 1 && $1 != "Total" {printf "  %-44s %10.2f GiB  exclusive %.2f GiB\n", "incus " $4, $1 / 1073741824, $2 / 1073741824}' | sed "s#$pd/##"
		fi
	done
	for item in package-cache node-modules image-cache tools projects machines; do
		row "agentbox $item" "$(alloc "/var/lib/agentbox/$item")"
	done
	vdata=$(readlink -f "$data")
	row "state.db" "$(alloc "$vdata/state.db")"
	row "daemon.log and logs" "$(( $(alloc "$vdata/daemon.log" || echo 0) + $(alloc "$vdata/logs" || echo 0) ))"
	row "VM system disk used (/)" "$(df -B1 --output=used / | tail -1)" "fstrim.timer: $(systemctl is-enabled fstrim.timer 2>/dev/null || echo none)"
	row "journald" "$(alloc /var/log/journal)"
	row "docker in the VM" "$(alloc /var/lib/docker)"
	if command -v incus >/dev/null; then
		echo "  last trims: $(journalctl -q -u fstrim.service -u agentbox-fstrim.service -n 3 --no-pager -o cat 2>/dev/null | tr '\n' ' ' | cut -c1-200)"
	fi
fi

if [ "$probe" -gt 0 ]; then
	dir="${probe_dir:-${host_data:-$HOME}}"
	[ -d "$dir" ] || dir=$HOME
	echo
	echo "Probe: 4 KiB write + fdatasync in $dir for ${probe}s"
	f=$(mktemp -p "$dir" .disk-report-probe.XXXXXX) || exit 1
	trap 'rm -f "$f"' EXIT
	if command -v python3 >/dev/null; then
		python3 -I - "$f" "$probe" <<-'PY'
			import os, sys, time
			path, secs = sys.argv[1], float(sys.argv[2])
			fd = os.open(path, os.O_WRONLY | os.O_CREAT)
			buf, lat, end = os.urandom(4096), [], time.monotonic() + secs
			i = 0
			while time.monotonic() < end:
			    t = time.monotonic()
			    os.pwrite(fd, buf, (i % 256) * 4096)
			    os.fdatasync(fd)
			    lat.append((time.monotonic() - t) * 1000)
			    i += 1
			    time.sleep(0.05)
			os.close(fd)
			lat.sort()
			q = lambda p: lat[min(len(lat) - 1, int(p * len(lat)))]
			slow = sum(1 for x in lat if x > 100)
			print(f"  n={len(lat)}  p50={q(.5):.1f} ms  p95={q(.95):.1f} ms  p99={q(.99):.1f} ms  max={lat[-1]:.1f} ms  over 100 ms: {slow}")
		PY
	else
		end=$((SECONDS + probe))
		while [ $SECONDS -lt $end ]; do
			s=$(date +%s%N)
			dd if=/dev/zero of="$f" bs=4k count=1 conv=fdatasync,notrunc status=none
			echo $(( ($(date +%s%N) - s) / 1000 ))
			sleep 0.05
		done | sort -n | awk '{a[NR] = $1} END {printf "  n=%d  p50=%.1f ms  p95=%.1f ms  p99=%.1f ms  max=%.1f ms (dd, includes a fork)\n", NR, a[int(NR*.5)+1]/1000, a[int(NR*.95)+1]/1000, a[int(NR*.99)+1]/1000, a[NR]/1000}'
	fi
fi
