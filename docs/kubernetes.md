# Agents in a Kubernetes cluster

[← back to the index](README.md)

An agent's machine can be a pod in a Kubernetes cluster instead of an Incus container here. The
daemon and `state.db` stay on this machine; each agent gets a pod and a claim, and dials the daemon,
so no Service, Ingress or inbound rule is needed.

## What works today

| Works | Not yet |
| --- | --- |
| `cluster add`, `list`, `set`, `remove`, `rotate`, `doctor`, `drain`, `undrain`, `upgrade` | a chat with a real login, which no pod has run yet |
| `registry add`, `list`, `test`, `rotate`, `remove` | the desktop tab, not tried against a pod |
| `image agent build`, `--push` | Cursor, whose adapter needs the Cursor SDK |
| the pod lifecycle: create, adopt, stop, pause, resume, destroy | file browsing through the app (it refuses a pod) |
| `move` between clusters, `cluster drain --move-to` | drain and upgrade clicked through the app |
| the gateway: commands, files, a terminal, ports, dial | file browsing through the app (it refuses a pod) |
| `create --cluster <name>`, and the clone on its claim | diff and files while the pod is stopped (no mirror) |
| the two git profiles: URL, login, clone and push | CSI volume snapshots, as a fast path |
| diff, file list and file read through the gateway | a fork from a checkpoint of a cluster agent |
| the desktop and its browser over the gateway, recordings included | placement and a queue, which is Stage 6 |
| the pod's size and claim, and a cluster's free capacity | a pod's disk use while it is stopped |
| snapshots, restore and fork, from an archive of the claim | zstd, because the image has no zstd yet |
| `base save` as a project image, and `recreate` | a project image built by the app, which builds in the CLI only |
| the in-agent API over the master: `whoami`, connectors, memory, heavy | the tailnet egress check, which needs an operator client |
| Claude Code, Codex, OpenCode and their adapters in the image | a connector that signs in, not yet called from a pod |
| | `control`, which agentd answers with exit 2 |

A daemon with no Incus runs its agents only in its clusters: that is the control plane
[`packaging/daemon/Dockerfile`](../packaging/daemon/Dockerfile) builds, and [Installing
AgentBox](install.md#a-control-plane-in-a-cluster) has the container and its commands. The app's
Setup page then lists each cluster's own checks instead of Incus's, and a project with no cluster set
places its new agents with `auto`, because there is no host to run them on.

`cluster add` (`internal/cli/cluster.go`, `internal/k8s`) keeps a namespace-scoped ServiceAccount
token sealed and never keeps the admin kubeconfig. `cluster doctor` makes a 1Gi canary claim and,
when an image is configured, a pod of it, and deletes both. Destroy deletes the pod, its token Secret
and its claim at once, with no retention window. An agent created against a cluster clones its
project's git profile on its claim, on its branch, from the base commit the master resolved; a claim
whose workspace another user owns is handed to the agent by an init container first, because git
refuses a repository its directory's owner doesn't match. The image carries Claude Code, Codex,
OpenCode and their adapters at the versions `internal/image/tools.txt` pins, and a chat runs its
adapter by name, since the image has no mise. Cursor is the one tool it cannot run yet: its adapter
needs the Cursor SDK, which the image does not carry.

The image also carries the desktop an Incus agent has: Xvnc, Chromium, openbox, the dock, a file
manager, a terminal and ffmpeg, with Chromium's flags in `/etc/chromium.d/agentbox-agent`, since the
pod, not Chromium's own sandbox, is what isolates it. A pod has no Incus proxy devices either, so the daemon
reaches the VNC server and Chromium's DevTools port from inside the pod, through the machine's
gateway connection. `browser.sh`, the wallpaper and the palette live under the agent's home there,
because that is the only place agentd lets the master write; a recording is made inside the pod and
copied out when it stops. `agentbox browser start demo/agent-01`, `browser open` and `media
screenshot --display` are what prove it on a pod.

## Prerequisites

- A cluster the daemon can reach. `scripts/k3d-up.sh` makes one here; any conformant cluster works,
  and its kubeconfig must point at an address the daemon reaches, not only your laptop.
- A storage class for the agent's claim. k3d and k3s have `local-path`; a cluster that must move a
  pod between nodes needs one that can, like Longhorn or NFS.
- A registry the cluster can pull from, or an image imported into it. The section below has three
  ways.
- Tailscale, when the daemon or the private git host is tailnet-only and the pods run elsewhere. A
  pod is not a tailnet peer; it reaches the tailnet through the operator's egress proxy.
- A daemon that may have no Incus at all: with none it is a control plane, every agent is a pod in
  its clusters, and Setup lists the clusters' checks instead of Incus's ([Installing
  AgentBox](install.md#a-control-plane-in-a-cluster)).

## A local cluster

```bash
scripts/k3d-up.sh                      # a cluster named agentbox, one worker
scripts/k3d-up.sh --with-registry      # and a registry:2 on its network
scripts/k3d-up.sh --delete             # remove both
```

The script checks for `k3d`, `kubectl` and Docker, creates the cluster only when it is missing, and
prints the commands to run next with its real name in them: the kubeconfig into `/tmp/<name>.yaml`,
the `cluster add`, and the daemon's gateway listener. It builds and pushes nothing. With
`--with-registry` it runs `registry:2` on the cluster's network and prints the reference it enables,
`<name>-registry.localhost:5000/agentbox-agent:dev`, a name that resolves here and inside the cluster
so push and pull use one reference.

`--operator` installs the Tailscale operator with Helm, a `ProxyGroup` named `agentbox-egress`, the
egress Services for this machine and the private git host, a `DNSConfig` and a CoreDNS stub for
`ts.net`. It reads `TAILSCALE_OAUTH_CLIENT_ID` and `TAILSCALE_OAUTH_CLIENT_SECRET` from the
environment, refuses to run without them, and never writes them to a file. The tailnet ACL (let
`tag:k8s` reach the master on 443 and the git host on 443 and 22) and the OAuth client's tags
(`tag:k8s-operator`, and ownership of `tag:k8s`) are still yours.

## The gateway

Nothing outside the pod reaches into it (`internal/gateway`, `internal/daemon/agentgateway.go`).
`agentd`, its only process, opens a WebSocket to the daemon and keeps it, and the daemon opens a
stream for each operation the table above lists. That is why a pod needs no Service, no port-forward
and no route from the cluster, and why the same code works on any cluster.

- **A local cluster:** `AGENTBOX_GATEWAY_ADDR=0.0.0.0:7782` opens a listener on port 7782 that
  serves the gateway alone; the master URL is `http://host.k3d.internal:7782`, how a pod reaches this
  machine on k3d. Plain HTTP is for a local test only, and `cluster doctor` says so.
- **A remote cluster:** `agentbox tailnet on --allow you@example.com` opens `127.0.0.1:7781` and
  publishes it with `tailscale serve` at `https://<machine>.<tailnet>.ts.net/agentbox`. That address
  is the master URL, the pod appends `/v1/agent-gateway` to it, and the pods reach it through the
  egress proxy with a real certificate.
- **Another machine:** that same address is what a command line elsewhere acts on. `agentbox profile
  add nas https://<machine>.<tailnet>.ts.net/agentbox` saves it once it answers,
  `agentbox use nas` makes every command act on it, and each command prints `profile nas <address>`
  on stderr first. `agentbox profile` lists them and says which answer, `agentbox use local` and
  `--local` go back to this machine's daemon, and `AGENTBOX_NO_PROFILE_BANNER=1` silences the line
  for a script. An installation with no profiles reads exactly as it did before.

The pod authenticates with a 32-byte bearer token minted for it. The token is a Secret mounted at
`/run/agentbox-secrets/token`; `state.db` holds only its SHA-256, so a token the daemon never issued
is refused without a plaintext ever being read back. A new pod gets a new token.

The same token is how a pod reaches the in-agent API, which an Incus agent reaches over its socket.
`agentbox whoami`, `connector list|tools|call`, `connector mcp` for every connector its AI tool
starts, `memory mcp` and `heavy` run inside the pod read `AGENTBOX_MASTER_URL` and
`AGENTBOX_TOKEN_FILE` and talk to `<master>/v1/self` with the token; there is no socket in a pod. The
route set is the socket's own, keyed to the agent the token names, so a pod reaches exactly what an
agent in an Incus machine does and nothing else of the daemon. On the tailnet listener that path is
gated by the token rather than by the Tailscale allowlist, because a pod has no login header to
offer; `agentbox cluster doctor` reports a master URL the pod cannot reach.

`agentbox shell` and `agentbox exec` reach a pod the same way. The shell attaches to the terminal
route the app's terminal pane uses, and the exec runs its command through the daemon on a WebSocket
of its own, which carries the command's two streams and the status it exited with. An agent in an
Incus container here is untouched: both still run the `incus` command directly, with no daemon in
between.

A pod does not wait for this machine's memory pressure: `heavy` starts at once there, since the
pod's memory is its own cgroup's and the cluster's, and there is no cgroup on this host to hold it.

## The registry

Which registry AgentBox pushes to and a cluster pulls from is configuration, not a choice AgentBox
makes for you (`internal/registry`, `internal/k8s/pullsecret.go`); a registry is private unless you
say `--public`, because the image is for your own cluster.

### Reuse a registry you already run

A Forgejo instance's container registry is the usual one here: the registry is the instance's own
host, images are named `<host>/<owner>/<image>`, and the instance's package pages are under
`https://git.example.com/api/packages/<owner>/container`. The prefix you give AgentBox is the host
and the owner, `git.example.com/<owner>`. Make a token with `write:package`, then:

```bash
docker login git.example.com
agentbox registry add forgejo --url git.example.com/h4ad --username h4ad
agentbox registry test forgejo
agentbox image agent build --tag dev --registry forgejo --push
agentbox cluster add k3d --kubeconfig /tmp/k3d.yaml --registry forgejo --image agentbox-agent:dev
```

`registry add` asks for the token without echoing it, or takes it on stdin with `--token-stdin`; it
is never a flag. The push is the container runtime's own login (`docker login <host>`), and
`agentbox image agent build` never takes a credential. The same token, sealed, becomes the
namespace's `imagePullSecret`, which is how the cluster pulls a private image. GHCR
(`--url ghcr.io/<owner>`) and Docker Hub (`--url docker.io/<user>`) are the same steps with another
URL and a token made there.

### A local registry with k3d

`scripts/k3d-up.sh --with-registry` runs a `registry:2` container on the cluster's network, over
plain HTTP, and configures the cluster's containerd to pull from it:

```bash
agentbox registry add local --url agentbox-registry.localhost:5000 --insecure
scripts/build-agent-image.sh --registry local --tag dev --push
```

`--insecure` is for this registry only. It is published on a `127.0.0.1` port and needs no login.

A registry whose certificate a private CA signed takes `--ca <pem>` with that certificate, and a
self-signed one `--insecure`, which the test and the pull secret accept instead of a CA. Both are
flags of `registry add`, and neither is ever a token.

### No registry at all

A local test can skip the registry and import the image:

```bash
scripts/build-agent-image.sh --tag dev
k3d image import agentbox-agent:dev -c agentbox
agentbox cluster add agentbox --kubeconfig /tmp/agentbox.yaml --storage-class local-path \
  --master-url http://host.k3d.internal:7782 --image agentbox-agent:dev
```

With no registry, the image name is the imported one and a pod uses `imagePullPolicy: IfNotPresent`.

## The image

`internal/agentimage/Dockerfile` builds on `debian:trixie-slim` for linux/amd64. Its system packages
are the list in [`base-packages.txt`](../internal/image/base-packages.txt), which every AgentBox
runtime installs (the Incus base image, this pod image, the `machines` desktop image and the daemon
container), then Go, Node, pnpm, Claude Code, Codex and OpenCode, their ACP adapters
(`claude-agent-acp` and `codex-acp`; OpenCode speaks ACP itself), the GitHub CLI, the Playwright MCP
server, the desktop (Xvnc, Chromium, openbox, tint2, pcmanfm, xfce4-terminal, xdotool, xwallpaper,
ffmpeg and the fonts `browser.sh` expects) and the `agentbox` binary. It runs as `agent`, UID and GID
1000, with `/workspace` and `/home/agent` on the claim, and its entrypoint is `agentbox agentd`;
there is no systemd. The desktop package list is the one `internal/image/system.sh` installs on an
Incus base image, because `browser.sh` is the same script in both runtimes; the two lists move
together. The pod image carries Codex and OpenCode itself, so a cluster agent isn't gated on the
Incus base image's optional components: it runs whichever tool its project or request names, and
the session says if the image can't.

Every tool is at the version `internal/image/tools.txt` pins, and the Dockerfile reads that file
during the build: moving one on is its line there and nothing else. The tools go outside
`/home/agent`, because a pod mounts its claim there over whatever the image has. What an agent
installs itself lands in its own home, and `/home/agent/.local/bin` is on its `PATH`.

It has the desktop, so an agent has a browser for the Playwright MCP server to drive and a screen to
look at, and its recordings have ffmpeg. Chromium's flags are in `/etc/chromium.d/agentbox-agent`:
a pod has no setuid sandbox and its seccomp profile refuses the user namespace the sandbox would
use, so the container is the sandbox, the same decision `internal/machines/image/chromium.conf`
records for `agentbox machines`. A chat runs its adapter by name, since the image has no mise;
Cursor's adapter is the exception, because it needs the Cursor SDK, which is not in the image yet.
The image is about 5.2 GB: the AI tools take 3.65 GB of that, and the desktop layer about 1.5 GB.

```bash
scripts/build-agent-image.sh --tag dev                            # this repository's binary, linux/amd64
agentbox image agent build --tag dev                              # the binary you are running
agentbox image agent build --tag dev --registry forgejo --push   # tag and push it
```

The private image must never go to a public registry. Claude Code and its adapter may not be
redistributed, no release workflow builds or publishes this image, and a variant for a public
registry, without Claude Code and the adapters, is not made yet.

## A first agent

```bash
scripts/k3d-up.sh --with-registry
k3d kubeconfig get agentbox > /tmp/agentbox.yaml
agentbox registry add local --url agentbox-registry.localhost:5000 --insecure
scripts/build-agent-image.sh --registry local --tag dev --push
agentbox cluster add agentbox --kubeconfig /tmp/agentbox.yaml --storage-class local-path \
  --master-url http://host.k3d.internal:7782 --registry local --image agentbox-agent:dev
agentbox cluster doctor agentbox
agentbox daemon stop
AGENTBOX_GATEWAY_ADDR=0.0.0.0:7782 agentbox daemon start
agentbox add ~/Projects/demo
agentbox project git set demo private https://hub.example/o/demo.git --auth token --from-file "$HOME/.config/forgejo/token"
agentbox create demo --cluster agentbox --ai none
```

The doctor says whether the API, the master URL, the namespace and its pod security, the nodes, the storage class, the
claim, the registry and the image are all right. A project added from a checkout prefills its private
profile from `hub` and its public one from `origin`, without a login, so set the private one as above
before the first cluster agent; the create refuses a project whose profile cannot clone. `agentbox
create` then waits up to five minutes for the pod's `agentd` to open its session, and the daemon
clones the profile inside the pod, onto its claim. `--ai none` is what has been run on a pod so far;
a tool is in the image, but no chat has run on a pod yet.

`--cluster` can be left out when the installation has no Incus: its projects place their new agents
with `auto`, which with one cluster is that cluster. On an installation that also runs agents on this
machine, a project with no cluster set starts them here, so name the cluster: `--cluster agentbox` for
one agent, or `agentbox project cluster demo agentbox` for every agent the project creates from then
on.

In the app, Settings, Clusters lists each cluster with its image, storage class, last error and a
Doctor button, and Registries lists each one with or without a login, and a Test button. While a pod
pulls, `kubectl` is what shows it: `kubectl -n agentbox get pods`, and `kubectl -n agentbox describe
pod ab-demo-agent-01`.

A cluster's Details opens what it holds right now: its pods with their phase, readiness, node, image,
restarts and the cluster's own reason (`FailedScheduling` on a node with no room, `ImagePullBackOff`
on a bad reference), the claims the agents' work lives on, the agents of the queue waiting for room
there with their place in line, and the namespace's last fifty events, newest first. A node's event is
in that list even though AgentBox did not make it: it is what explains a pod that stays `Pending`. A
cluster the daemon cannot reach still shows its card and its queue; the dialog says why its lists are
empty, and the cluster's own health stays what the last Doctor run found. Reading events needs `list`
on them: a cluster added by an earlier release does not have it, and `agentbox cluster rotate <name>
--kubeconfig <admin>` applies AgentBox's access again so the events appear.

## Sizes, disk and capacity

One agent's pod is sized by its cluster, so every agent of a cluster asks for the same thing and the
cluster's free capacity is a count of them:

```bash
agentbox cluster add k3d --kubeconfig /tmp/agentbox.yaml --image agentbox-agent:dev \
  --max-agents 4 --agent-cpu 2 --agent-memory 8Gi --workspace-size 40Gi --ephemeral-storage 10Gi
agentbox cluster set k3d --agent-memory 12Gi     # the next agent starts with more
```

| Flag | What it is | Default |
| --- | --- | --- |
| `--agent-cpu` | the CPU the pod asks for, and is capped at | none asked for |
| `--agent-memory` | the memory the pod asks for; its limit is a quarter more | none asked for |
| `--workspace-size` | the claim its worktree and home live on | 20Gi |
| `--ephemeral-storage` | the local disk it may fill outside the claim, `/tmp` included | 10Gi |
| `--max-agents` | the ceiling on how many run at once | the nodes' room |
| `--cpu`, `--memory` | the whole cluster's budget, which capacity also respects | the nodes' room |

The CPU and memory requests are what the scheduler places the pod by, so a cluster whose nodes have
4 CPU and 16Gi and whose agents ask for 2 CPU and 8Gi holds two agents. A pod's ephemeral storage is a
limit, not a request of the same size: Kubernetes counts its writable layer, `/tmp` and `/run`, and a
pod that crosses it is evicted instead of filling the node every other agent shares. That is what the
disk guard is on a host with Incus machines, where there is one disk to watch.

`agentbox cluster list` shows the agents each cluster has and how many more fit, from the daemon's
last look at it, and the app's new-agent dialog shows the same count beside each cluster. The
capacity is the smallest of the ceiling, the free CPU divided by `--agent-cpu` and the free memory
divided by `--agent-memory`, after the cluster's own budget. A cluster with no ceiling and no
per-agent size says `no limit`, because nothing there counts agents.

A claim is made once and kept, so `--workspace-size` reaches the next agent, not an existing one:
growing the claim of one that already runs is its storage class's job, and shrinking it means starting
the agent on a new claim from a snapshot. The ephemeral storage applies to the next pod start.

An agent's disk use is measured from inside its running pod, over the gateway, because Kubernetes says
how big a claim is rather than how full. A stopped pod's claim is not measured, and
`agentbox disk` leaves that agent out until it runs again.

## Snapshots, restore and fork

A cluster agent's machine is its claim, and the cluster keeps no copy of it: a snapshot is an archive
of the whole claim streamed to this machine, plus a git ref of the worktree
(`internal/agent/snapshot.go`). The claim holds the `workspace` and `home` subdirectories, so one
archive is the whole machine.

```bash
agentbox snapshot demo/agent-01 before-work     # stops the pod, archives its claim, starts it again
agentbox snapshots demo/agent-01                # the name, when, and the branch it was taken at
agentbox restore demo/agent-01 before-work
agentbox fork demo/agent-01@before-work --name agent-02
```

A snapshot stops the pod, runs a short-lived helper pod that mounts the claim, streams `tar -czf -`
out of it over the API server's exec, and starts the agent again when it was running. The pair lands
in `~/.local/share/agentbox/snapshots/<project>/<agent>/`: a `<name>.tar.gz` and a `<name>.json` with
the snapshot's commit, the branch head, the image and the digest the node reported. `--consistent`
asks for nothing more on a pod, because the archive is only ever taken with the pod stopped, and that
is what makes it consistent. Snapshotting a stopped agent starts it for the worktree's git half and
stops it again.

A restore does not write over the claim the agent has. It creates a new claim, unpacks the archive
into it with a helper pod, moves the agent's row onto it, and starts the pod on the image the snapshot
recorded; only after the new machine is up is the old claim deleted. A failure leaves the agent on
the work it had. The worktree is then reset to the snapshot's ref, and the state it had is kept on
`refs/agentbox/pre-restore/<agent>/<time>`.

A fork creates a new claim from the same archive and moves the new branch to the snapshot's commit,
so the copy starts with the home, the ignored files (`node_modules` included) and the uncommitted
work the source had. A fork of a cluster agent stays on its cluster, and starts from a snapshot
rather than a checkpoint: a checkpoint has only the worktree's half.

What the archive does not carry is the system packages in the image. A package installed into the
pod's root filesystem with `apt` is gone after a restore or a fork, and belongs in the project image
below. CSI volume snapshots would make the archive unnecessary where the storage class supports them;
they are not used yet, so every snapshot works on `local-path`, and the archive is gzipped because the
image does not carry `zstd`.

## Base images

On this host a project's base is one agent's machine, saved with `agentbox base save`. A cluster
agent has no machine on this host to photograph: a project's base is its own image. Write
`.agentbox/agent.Dockerfile` in the project, starting with a `FROM` line, and run:

```bash
agentbox base save demo/agent-01
```

The CLI builds it from the project's checkout, on top of the agent image the cluster runs: the first
`FROM` is replaced with that image, so the Dockerfile does not name a registry. It pushes the result
as `<registry>/<project>-agent:<time>` and records the digest the registry reported. The cluster needs
a registry for this (`agentbox registry add`, then `agentbox cluster set <name> --registry <name>`),
because a pod pulls its image from somewhere. The push signs in with the login Docker already has, so
`docker login <registry>` once; the command never takes a credential.

The image runs as the `agent` user, as the base does: a `RUN` that installs system packages starts
with `USER root` and gives the user back afterwards.

```dockerfile
FROM agentbox-agent
USER root
RUN apt-get update && apt-get install -y ripgrep
USER agent
```

`agentbox base show` prints the image and the digest, `agentbox base revert` goes back one save, and
`agentbox base rm` drops it. Every pod the project starts after a save runs that image by digest, so a
tag that moved cannot change what an agent runs. A pod that is already running keeps the image it was
made with; `agentbox recreate` replaces it, as below.

## Recreating a pod

`agentbox recreate demo/agent-01` replaces a cluster agent's pod and keeps its claim. The home and
the worktree are on the claim, so the chat sessions and the files stay; the pod's processes and its
container filesystem do not, and the new pod runs the image the cluster points at now. That is how an
agent picks up a new image without losing its work, while a restore pins the image its snapshot was
taken with. An agent on this host is a different case: its machine is the instance, so `recreate` is
only for one whose machine is gone, and it starts from the base image.

## Moving an agent to another cluster

```bash
agentbox move demo/agent-01 --cluster e2e
agentbox move demo/agent-01 --cluster auto     # the one with the most room
```

A move is a pod for a pod: an agent that runs on this host has an Incus instance, and one that is still
queued has no machine yet, so both are refused with the reason.

The move checkpoints the agent's worktree first and publishes it. If the rest fails, the branch still
carries the work, which is the one part that has to survive a machine that doesn't. Then it stops the
source pod, streams its claim out of the source cluster and into a new claim on the target, so nothing
of the workspace lands on this host, and writes the row onto that claim. The reconciler makes the
target's pod from the row, and the move is done when that pod answers the gateway and the agent's env,
git config, brief and tmux session are written into it, which is the same proof `create` and `recreate`
use.

Only then is the source pod, its token and its claim removed. A failure before the row moves leaves the
source as it was and deletes the claim the target made; a failure after it puts the row back on the
source, removes the target's pod and claim, and restarts the source pod. A leftover that can't be
removed is logged with its claim name and the agent is left stopped rather than on two machines at
once. The source is not kept until a first chat: two live pods on one branch, with one row to name
them, is worse than the guarantee that would buy.

The target runs its own image, not one a snapshot pinned on the source, because a pin names an image the
target may not be able to pull. A stopped agent moves too and starts on the target. `--cluster auto`
uses the same placement a create does, so a full or draining cluster is refused, and the target's room
is reserved before the source is touched.

## Draining a cluster

```bash
agentbox cluster drain k3d                    # stop every running agent there
agentbox cluster drain k3d --move-to e2e      # move each one instead, one at a time
agentbox cluster drain k3d --keep-running     # only refuse new agents
agentbox cluster undrain k3d
```

A drain marks the cluster first, so admission refuses it from that moment: a create that names it fails
with `cluster k3d is draining: it takes no new agents`, and `auto` skips it, naming it among the
clusters that have no room. The agents it already has are not refused: a drain stops new agents, not
the ones there.

With no flag, every running agent is stopped, one at a time, and keeps its claim and its branch, so
starting it again brings it back. `--move-to` moves each running agent to another cluster instead, with
the same work as `agentbox move`, and refuses a target that is itself draining. `--keep-running`
only marks the cluster and leaves its agents alone. Each one is logged as it is stopped, moved or
skipped, and the command ends with the cluster's state.

A drain that fails partway leaves the cluster draining and the agents it had not reached as they were.
Running it again continues, and `agentbox cluster undrain` lets the cluster take agents again without
touching the ones it has. `agentbox cluster list` marks the cluster `DRAINING` and shows its `FREE` as
`0`, whatever room the nodes still have.

## Upgrading a cluster

```bash
agentbox cluster upgrade k3d --digest sha256:4f4fb70000000000000000000000000000000000000000000000000000000000
agentbox cluster upgrade k3d --rollback
```

An upgrade runs the same image at a new digest. The digest is a full `sha256:` one that a registry
reported, and the image keeps the tag the cluster was added with, so `agentbox-agent:dev` becomes
`agentbox-agent:dev@sha256:4f4fb700...`, and only the digest changes from one upgrade to the next. The
cluster's image, not an agent's, is what its pods run, which is why this is one change rather than one
per agent.

The new image is recorded first, so a pod that starts while the rollout is running is at the new image
rather than the old one. Then every running agent's pod is recreated at it, one at a time, each keeping
its claim: the worktree and the home come back, and only the container filesystem is replaced. The
other agents keep running while one changes over. A stopped agent has no pod to replace and runs the
new image the next time it starts.

A failure stops at the agent it names and leaves the cluster at the new image, with the pods already
recreated on it. Fix what it says and run `agentbox cluster upgrade k3d --rollback`: that puts the
image the cluster ran before the upgrade back, and records the one it is leaving as the new previous
image, so a second `--rollback` goes forward again. `agentbox cluster list` shows the image name and the
first eight characters of its digest.

A pod restored from a snapshot keeps the image the snapshot was taken with, which its row pins, until
it is recreated or upgraded. `agentbox recreate` drops that pin and starts the pod at the cluster's
current image.

The cluster detail page reads the cluster's events, which a cluster added before this release does not
have permission for yet. `agentbox cluster rotate <name> --kubeconfig <admin>` applies AgentBox's access
again, and the events appear after it.

## Troubleshooting

`agentbox cluster doctor <name>` runs every check, not just the first failure:

| Check | What a failure means |
| --- | --- |
| `api` | The daemon's ServiceAccount cannot reach the API server. Its kubeconfig must point at an address the daemon reaches. |
| `master url` | Not set, or plain `http`. Set it with `agentbox cluster set <name> --master-url <url>`. The `http` note is right for a local cluster only. |
| `namespace` | The token is refused, or the Role cannot read pods there. `cluster rotate <name>` makes a new token; `cluster remove` and `add` again reapplies the Role and binding. |
| `pod security` | The namespace enforces no Pod Security Admission level, so the credential could start a privileged pod. `cluster rotate <name>` labels a namespace AgentBox created; one it did not create you label yourself with `kubectl label namespace <ns> pod-security.kubernetes.io/enforce=baseline`. A cluster added before the check cannot read its namespace yet: `cluster rotate <name>` grants it. |
| `network policy` | The namespace has no `agentbox-agents` NetworkPolicy, or has one that no longer selects agent pods or that lets traffic in. `cluster rotate <name>` applies it, and the Role rule it needs. |
| `nodes` | No node is ready, or the cluster has none. |
| `storage class` | The named class does not exist. Empty asks for the cluster's default; k3d and k3s have `local-path`. |
| `workspace claim` | A 1Gi claim could not be made, or a class that binds at once never bound, which usually means no provisioner. A class that binds on first use passes. |
| `registry` | Skipped when there is no login to apply (no registry, or one that needs none); otherwise the pull secret is missing from the namespace or is not a docker config. Add the cluster again, or rotate the registry. |
| `image` | Skipped when no image is configured; otherwise the canary pod could not run, and the reason is in the detail, such as `ImagePullBackOff` or a pod that exited at once. |

The app's Setup page has a check per cluster too, and it reads configuration only: the image the
cluster is set to, its master URL, and whether the daemon's last attempt to reach it failed. It makes
nothing in the cluster. `cluster doctor` is the deep one, with a canary claim and a canary pod, so
when Setup calls a cluster fine and an agent still does not start there, the doctor is what says why.

`kubectl -n agentbox logs ab-<project>-<agent>` shows `agentd`'s log, and `describe` shows the pod's
events. A pod that stays `Pending` has nowhere to run or a claim that never bound; a pod in
`ImagePullBackOff` has the wrong image reference or no usable pull secret. A pod pulls the cluster's
registry URL plus the image name, so `--registry local --image agentbox-agent:dev` pulls
`<registry>/agentbox-agent:dev`.

The `http` note on `master url` is not a failure, and it is right for a local test only. The doctor
does not check the Tailscale operator, the egress Services or the `DNSConfig` yet; those builders
are in `internal/k8s/tailnet.go`. With `--tailnet operator`, look at `kubectl -n <namespace> get
svc,proxygroup`, `kubectl get dnsconfig`, and the operator's pods in `tailscale`.

## Security

- The pod's token is a Secret, and `state.db` holds only its SHA-256. A token dies with the pod it
  was minted for; a start mints a new one.
- The cluster credential is a namespace-scoped ServiceAccount token Secret, sealed with the same key
  as the agent secrets; the admin kubeconfig is used once by `cluster add` or `cluster rotate` and
  never stored. The registry's login is sealed here and written into the namespace as its pull
  secret, and no response from the daemon carries it.
- The credential can create pods, so what keeps it inside its namespace is Pod Security Admission:
  the namespace `cluster add` creates carries `pod-security.kubernetes.io/enforce=baseline`, which
  refuses privileged pods, host namespaces and `hostPath` volumes. Without it the credential could
  start a privileged pod and be root on a node. Baseline, not restricted, because the init container
  that hands a claim to the agent's user runs as root to `chown` it. `cluster rotate` labels a
  namespace AgentBox created before the label. AgentBox never relabels a namespace it did not
  create: `cluster add --namespace default` is refused unless that namespace already enforces
  baseline or restricted, and the error gives the `kubectl label` command.
- The pod connects out, so nothing in the cluster listens for the daemon. The listener on 7782 must
  not be exposed beyond a local test. A remote cluster reaches the daemon through the tailnet
  listener, which `tailscale serve` publishes and which checks who asks, except on
  `/v1/agent-gateway`, where the pod's own bearer token is the check.
- One NetworkPolicy, `agentbox-agents`, selects the namespace's agent pods and denies every
  connection in. Nothing in the cluster has a reason to reach a pod, so nothing loses a path it had,
  and a workload in another namespace or a compromised pod in this one cannot open a connection
  either. `cluster add`, `cluster set` and `cluster rotate` apply it; a cluster added before the
  policy existed gets it from `cluster rotate <name>`, which also applies the Role rule the policy
  needs.
- The policy does not restrict egress, and that is deliberate. A pod dials the public AI APIs, this
  machine and a git host, and none of those addresses is fixed enough to write into a policy that
  would still work tomorrow. The policy names the cluster's resolver and this machine, which is the
  shape a narrow list would have, and then allows every other address. What makes a narrow list
  possible is a proxy the pod dials instead of the internet, and that is a change to the pod, not to
  the policy. Until then a list of exceptions would break a chat the day an API moved, without
  closing the hole it was meant to close.
- A NetworkPolicy is only as good as the plugin behind it. Calico, Cilium, Antrea and k3s's own
  kube-router enforce one; a CNI without a policy controller accepts the object and silently ignores
  it, and `kubectl get networkpolicy` shows the object either way. `cluster doctor` checks that the
  policy exists and denies ingress, not whether the cluster acts on it.

## Testing

`scripts/k8s-e2e.sh` proves the pod path against a real API server, with nothing left behind. It
needs Docker, k3d, kubectl and Go on PATH and says how to install each when one is missing. It makes
a throwaway k3d cluster (`scripts/k3d-up.sh --name k8s-e2e --agents 0`), builds `bin/agentbox` and
the small test image in `scripts/k8s-e2e.Dockerfile`, and runs the daemon and a `git daemon` in
containers on the cluster's network, with the kubeconfig pointed at the server node the daemon
reaches. Then it adds the cluster, requires `cluster doctor` to pass every check, `network policy`
included, adds a fixture project whose private profile is that git daemon, and creates
`demo/agent-01` there. What it checks through the command line:

- `agentbox exec` prints the pod's own hostname from inside the pod, and a command that exits 7 makes
  `exec` exit 7.
- `agentbox stop` makes `exec` refuse with the stopped sentence, `agentbox start` brings the pod back
  on its claim, and `exec` works again.
- `agentbox destroy` removes the agent, and `agentbox list` no longer has it.

It does not prove the production image (`internal/agentimage/Dockerfile` takes more than ten minutes
and several GiB), a chat with a real login, a pull from a registry, the tailnet, or the desktop in a
pod. Those stay manual, on the image and cluster a real one runs.

CI runs it in the `k8s-e2e` job when a PR touches a package the pod path is made of (`internal/k8s`,
`internal/machine`, `internal/gateway`, `internal/agentd`, `internal/workspace`,
`internal/agentimage` or `internal/gitcred`), `desktop/src/main/connection.ts` or the script itself,
and weekly on schedule. The cluster and both containers are removed on success and on failure, so a
rerun after a failure is clean.
