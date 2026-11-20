# lib.sh is what the benchmark's scripts share. It is sourced, from the
# repository's root, by check.sh and the scripts beside it, and only uses what
# a Debian agent machine, a Linux host and a Mac all have.

# cachedir is this checkout's Go build cache: one per checkout, in the
# temporary directory (an agent's /t, the host's /tmp), so that reset.sh can
# empty it and every run of a task starts from a cold cache.
cachedir() {
	printf '%s/benchfix-gocache-%s\n' "${TMPDIR:-/tmp}" "$(pwd -P | cksum | cut -d' ' -f1)"
}

# now_ms is the wall clock in milliseconds: bash 5's EPOCHREALTIME, or whole
# seconds where there is none (macOS's bash 3.2).
now_ms() {
	if [ -n "${EPOCHREALTIME:-}" ]; then
		local us=${EPOCHREALTIME/[.,]/}
		echo $((us / 1000))
	else
		echo $(($(date +%s) * 1000))
	fi
}

# js prints its argument as a JSON string.
js() {
	local s=$1
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\r'/\\r}
	s=${s//$'\t'/\\t}
	printf '"%s"' "$s"
}

# readfirst prints the first line of a file, or nothing.
readfirst() {
	[ -r "$1" ] && head -n 1 "$1" 2>/dev/null
}

# meminfo prints one /proc/meminfo field in KiB, or nothing.
meminfo() {
	[ -r /proc/meminfo ] && awk -v k="$1:" '$1 == k { print $2; exit }' /proc/meminfo
}

# fstype prints the type of the filesystem a directory is on.
fstype() {
	stat -f -c %T "$1" 2>/dev/null || df -T "$1" 2>/dev/null | awk 'NR == 2 { print $2 }'
}

# probe prints, as a JSON object, what a build here sees: the CPUs it may use,
# the cgroup limits on them, the worker counts in its environment (the ones an
# agent's env file sets), the load, the memory and the filesystems.
probe() {
	local cpus cgdir
	cpus=$(nproc 2>/dev/null || sysctl -n hw.logicalcpu 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null)
	cgdir=/sys/fs/cgroup
	printf '{'
	printf '"at":%s,' "$(now_ms)"
	printf '"host":%s,' "$(js "$(uname -n)")"
	printf '"os":%s,' "$(js "$(uname -s) $(uname -r)")"
	printf '"nproc":%s,' "$(js "$cpus")"
	printf '"cpuMax":%s,' "$(js "$(readfirst $cgdir/cpu.max)")"
	printf '"cpuWeight":%s,' "$(js "$(readfirst $cgdir/cpu.weight)")"
	printf '"memoryMax":%s,' "$(js "$(readfirst $cgdir/memory.max)")"
	printf '"memoryHigh":%s,' "$(js "$(readfirst $cgdir/memory.high)")"
	printf '"loadavg":%s,' "$(js "$(cut -d' ' -f1-3 /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null)")"
	printf '"memTotalKiB":%s,' "$(js "$(meminfo MemTotal)")"
	printf '"memAvailableKiB":%s,' "$(js "$(meminfo MemAvailable)")"
	printf '"memoryShort":%s,' "$(js "$(readfirst "${AGENTBOX_MEMORY_SHORT_FILE:-/run/agentbox/memory-short}")")"
	printf '"inAgent":%s,' "$([ -e /etc/agentbox-image ] && echo true || echo false)"
	printf '"worktreeFs":%s,' "$(js "$(fstype .)")"
	printf '"tmpFs":%s,' "$(js "$(fstype "${TMPDIR:-/tmp}")")"
	printf '"goVersion":%s,' "$(js "$(go env GOVERSION 2>/dev/null)")"
	printf '"env":{'
	local v first=1
	for v in GOMAXPROCS GOFLAGS MAKEFLAGS CARGO_BUILD_JOBS VITEST_MAX_WORKERS AGENTBOX_CPUS GOCACHE TMPDIR BASH_ENV; do
		[ $first = 1 ] || printf ','
		first=0
		printf '%s:%s' "$(js "$v")" "$(js "${!v-}")"
	done
	printf '}}'
}
