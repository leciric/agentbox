# Benchmarks: agents in containers vs. one Cloud Hypervisor VM

`scripts/bench/` runs the same plan against each way of running AgentBox's agents on
this host, samples the host itself while it does, and writes one markdown table
(`results.md`) and the raw JSON (`results.json`), with every command's output in
`logs/<mode>.log`.

```bash
git fetch origin agentbox/feat-cloud-hypervisor-vm       # what vm mode builds its agentbox from
scripts/bench/run.sh                                  # containers, then the Cloud Hypervisor VM (agentbox vm)
scripts/bench/run.sh --modes containers,vm,chproto    # and the prototype that drives Cloud Hypervisor directly
scripts/bench/run.sh --quick                          # a short run, to check it works on a machine
scripts/bench/run.sh --cleanup --modes containers,vm  # remove what a killed run left
```

Results go to `bench-results/<time>/` in the current directory (`--out` to change it).
On a 16-core host, a full run took about 10 minutes for `containers` and 17 for `vm`
(5 of them its fresh install); allow 45 minutes for both on a slower disk. `run.sh -h`
lists every option.

## The modes

- **`containers`**: today's AgentBox. Agents are Incus containers on the host, made with
  the `agentbox` on your `PATH` (`--agentbox` for another) and its running daemon.
- **`vm`**: AgentBox in one Cloud Hypervisor VM, through agentbox's own front end
  (`agentbox vm init`, from `agentbox/feat-cloud-hypervisor-vm`). The harness builds that
  `agentbox` from `--vm-ref` (default `origin/agentbox/feat-cloud-hypervisor-vm`, so
  fetch it first) with `git archive`, leaving your checkout alone, and runs it with a
  data directory of its own: `XDG_DATA_HOME` and `XDG_CONFIG_HOME` under
  `~/.cache/agentbox-bench/vm`. That's where it keeps the VM, what runs it, the
  forwarded daemon socket and the agents' worktrees, so your own AgentBox, and a VM of
  your own, are never touched. Its fresh install is `agentbox vm init` then `agentbox
  image build` (`--vm-setup` to change them, like `--vm-setup '"$AGENTBOX" vm init
  --memory-cap 16GiB' --vm-setup '"$AGENTBOX" image build'`), its lifecycle `vm
  start|stop|pause|resume`, what it holds the resident memory of its Cloud Hypervisor,
  virtiofsd and passt, and its memory as `vm status --json` reports it. At the end,
  `agentbox stop --all` (Free resources) stops its agents, the VM is shut down, and
  `vm delete --yes` removes it with the directory.
- **`chproto`**: the prototype, for until `vm` runs. The harness drives Cloud Hypervisor
  directly, in the same shape: one VM (Debian 13, 16 GiB and every core by default), with
  Incus inside on a btrfs pool on the VM's second disk, and every agent an Incus
  container copied from a Debian base with Go and Node. The VM's disks are raw files on
  the host's filesystem, opened with `O_DIRECT` (`--ch-disk direct=on`), so its writes
  don't go through the host's page cache. Its memory has a balloon with free page
  reporting (`--ch-balloon`). Its network is a tap on the Incus bridge, whose DHCP and
  NAT it shares with today's agents.

## What it needs

For every mode:

- Linux, run as the user who runs AgentBox, not root. Go, to build the harness (the
  repository's `mise.toml` has it).
- Stop your other agents first: they load the same disk, and their IO shows up in the
  host's numbers.
- About 30 GiB free on the filesystem of `~/.cache` (40 for `chproto`), for the
  agents' builds and the 10 GiB write.

`containers`: AgentBox installed and working, with its base image built:
`agentbox host check` shows no ✗ for Incus, the storage pool and the base image. No
Claude Code login is needed: the harness's agents run no AI tool (`--ai none`).

`vm`: `/dev/kvm` (the `kvm` group), `ssh`, the branch fetched (`git fetch origin
agentbox/feat-cloud-hypervisor-vm`), the internet (the VM fetches Cloud Hypervisor,
passt, virtiofsd and Debian's cloud image, then builds its base image), and the cache in
your home folder, which the VM shares. No sudo, no Incus on the host, and no Claude login.
It runs beside your own AgentBox: stop your agents for clean numbers, but the daemon can
stay. Run it from a shell whose group is your own, not under `sg` or `newgrp`: virtiofsd
maps only the group it starts with, and the VM then can't create files in your home
folder. `--vm-agentbox path/to/agentbox` runs a build of your own instead.

`chproto`:

- `/dev/kvm`, which your user can open (the `kvm` group).
- The Incus bridge (`incusbr0`, which `agentbox host setup` makes), or another bridge
  with DHCP and NAT given with `--ch-bridge`.
- `sudo`, once, for the VM's tap: `run.sh` asks for your password before it starts, and
  deletes the tap at the end. Without sudo, make the tap yourself first, and the harness
  uses it and leaves it:
  `sudo ip tuntap add dev abbench0 mode tap user $USER && sudo ip link set abbench0 master incusbr0 up`
- `tar`, `xz`, `ssh`, `ssh-keygen`, `git` and `ip`, and 16 GiB of free memory for the VM
  (`--ch-memory` for less).
- The internet: it downloads Cloud Hypervisor v53.0 and its firmware (13 MB) and the
  Debian 13 cloud image (230 MB) into `~/.cache/agentbox-bench/dl/`, kept between runs,
  and the guest installs Incus, the Debian container image, Go and Node.

## What it measures

In this order, for each mode:

1. **Setup** from a fresh install: every step and its time. `containers` doesn't rebuild
   the base image, which would replace the one your agents are copied from: its time
   is the last image build in the daemon's job history. `vm` times `vm init` and `image
   build`, from nothing, every run.
   `chproto` times download (0 when cached), disks, first boot with cloud-init, Incus,
   and the agents' base.
2. **The VM**: pause and resume (until it answers), and shutdown and boot (until agents
   can be made), three and two rounds, keeping the median.
3. **Memory idle**, and an idle window of host sampling.
4. **One agent**: create, then three rounds each of stop and start, and pause and
   resume.
5. **Memory with 1, 3 and 5 agents**, idle and running.
6. **One agent's build**, cold then warm: `go test ./...`, then `go test -count=1 ./...`
   (with the build cache warm: without `-count=1` the tests' results come from the cache
   and nothing runs), then `npm --prefix desktop ci && npm --prefix desktop run build`
   twice. Cold means the agent's Go and npm caches and `node_modules` are emptied
   first, whatever the base image came with.
7. **Three agents building at once**, cold, while the host is sampled.
8. **A sustained 10 GiB write** in one agent (`dd` from `/dev/urandom`, so btrfs can't
   compress it away, with `conv=fsync`), while the host is sampled, and for a minute
   after, for the writeback.
9. **Memory after every agent stops** (in `vm`, all at once with `agentbox stop --all`,
   timed), and again a minute later: does it come back? In `chproto`, again after the
   guest drops its caches; in both VM modes, after the VM shuts down.

The host sampler runs in the harness, on the host, for the whole of each mode:

- an **fsync probe** writes 4 KiB and fsyncs it five times a second, in
  `~/.cache/agentbox-bench/probe` (`--probe-dir`), on the host's own filesystem: what an
  editor saving a file, or a browser's database, waits for. p50, p95, max, and how many
  took over 100 ms and over 1 s;
- once a second, **`/proc/pressure/io` and `/proc/pressure/memory`**, as the share of that
  second some or all tasks were stalled (mean and worst second);
- **btrfs commits** from `/sys/fs/btrfs/<uuid>/commit_stats` for the probe's filesystem:
  how many ended in the window, their mean, and the longest seen (a commit still running
  counts by its age);
- memory in use, disk throughput, and what the agents hold: their containers' cgroups
  (`memory.current`), or the VM process's resident memory;
- for each window, **page cache thrash**: file pages read again after being evicted
  (`workingset_refault_file` in `/proc/vmstat`) on the host and, for a VM, inside its
  guest, with the guest's own memory pressure.

## What it does to the host, and Ctrl-C

It makes a project named `agentbox-bench`, from a clone of this repository in
`~/.cache/agentbox-bench/<mode>/repo`, and agents `bench-1` to `bench-5` in it, with no AI
tool. `vm` makes its VM, with its own daemon, projects and agents, in
`~/.cache/agentbox-bench/vm/`; `chproto` makes its VM in `~/.cache/agentbox-bench/chproto/`
and the tap `abbench0`. Nothing else: it never touches your projects, agents, base image or
VM, and never runs `agentbox host setup`, or `image build` on your own AgentBox. It refuses
to start over what an earlier run left, and says to run `--cleanup`.

Ctrl-C stops the run, destroys the bench agents and project, shuts the VM down, deletes
the tap and the VM's disks, and still writes the results so far. A second Ctrl-C abandons
that cleanup; `run.sh --cleanup --modes …` finishes it. `--keep` leaves everything in
place at the end, to look at.

## Reading the numbers

- The prototype's setup, agent create and builds aren't AgentBox's: its base has Go, Node
  and build tools, not the agent tools, and an agent is a container copy and a `git
  clone`, not AgentBox's create. What it answers is what the VM changes: boot, pause and
  resume, what the host holds, and how the host feels while agents work.
- Memory in use is `MemTotal − MemAvailable` over the mode's own baseline, read before it
  set anything up, so anything else on the host moves it too; what the agents or the VM
  hold, in brackets, is theirs alone.
- The fsync probe and pressure are the host's, whoever causes them.
- A VM's size decides how the host feels. Its disks bypass the host's page cache, so a
  guest short of memory re-reads its own cache from the SSD, and the host stalls behind
  it. Measured here on the prototype (a single run each, on a host shared with other
  agents): three agents building at once in a 12 GiB VM read 138 GiB from the disk, and
  host fsyncs took up to 36 s; in a 20 GiB VM they read 3.5 GiB, and the worst fsync took
  0.9 s (4.5 s with today's containers). Try `--ch-memory` sizes against your agents.
