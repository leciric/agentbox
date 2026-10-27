# Agents in a Kubernetes cluster

[← back to the index](README.md)

An agent's machine can be a pod in a Kubernetes cluster instead of an Incus container here. The
daemon and `state.db` stay on this machine; each agent gets a pod and a claim, and dials the daemon,
so no Service, Ingress or inbound rule is needed.

## What works today

| Works | Not yet |
| --- | --- |
| `cluster add`, `list`, `set`, `remove`, `rotate`, `doctor` | a chat with a real login, which no pod has run yet |
| `registry add`, `list`, `test`, `rotate`, `remove` | the desktop tab in the app, not tried against a pod |
| `image agent build`, `--push` | Cursor, whose adapter needs the Cursor SDK |
| the pod lifecycle: create, adopt, stop, destroy | an agent's cluster or pod in the app |
| the gateway: commands, files, a terminal, ports, dial | file browsing through the app (it refuses a pod) |
| `create --cluster <name>`, and the clone on its claim | diff and files while the pod is stopped (no mirror) |
| the two git profiles: URL, login, clone and push | snapshots, restore and fork (Stage 5) |
| diff, file list and file read through the gateway | placement, capacity and a queue |
| the desktop and its browser over the gateway, recordings included | the tailnet egress check, which needs an operator client |
| the in-agent API over the master: `whoami`, connectors, memory, heavy | a connector that signs in, not yet called from a pod |
| Claude Code, Codex, OpenCode and their adapters in the image | `control`, which agentd answers with exit 2 |

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

`internal/agentimage/Dockerfile` builds on `debian:trixie-slim` for linux/amd64, with
ca-certificates, git, tmux, curl, jq, less, openssh-client, procps, python3, unzip, xz-utils, Go,
Node, pnpm, Claude Code, Codex and OpenCode, their ACP adapters (`claude-agent-acp` and `codex-acp`;
OpenCode speaks ACP itself), the GitHub CLI, the Playwright MCP server, the desktop (Xvnc, Chromium,
openbox, tint2, pcmanfm, xfce4-terminal, xdotool, xwallpaper, ffmpeg and the fonts `browser.sh`
expects) and the `agentbox` binary. It runs as `agent`, UID and GID 1000, with `/workspace` and
`/home/agent` on the claim, and its entrypoint is `agentbox agentd`; there is no systemd. The desktop
package list is the one `internal/image/system.sh` installs on an Incus base image, because
`browser.sh` is the same script in both runtimes; the two lists move together.

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

The doctor says whether the API, the master URL, the namespace, the nodes, the storage class, the
claim, the registry and the image are all right. A project added from a checkout prefills its private
profile from `hub` and its public one from `origin`, without a login, so set the private one as above
before the first cluster agent; the create refuses a project whose profile cannot clone. `agentbox
create` then waits up to five minutes for the pod's `agentd` to open its session, and the pod clones
the profile onto its claim. `--ai none` is what has been run on a pod so far; a tool is in the image,
but no chat has run on a pod yet.

In the app, Settings, Clusters lists each cluster with its image, storage class, last error and a
Doctor button, and Registries lists each one with or without a login, and a Test button. While a pod
pulls, `kubectl` is what shows it: `kubectl -n agentbox get pods`, and `kubectl -n agentbox describe
pod ab-demo-agent-01`.

## Troubleshooting

`agentbox cluster doctor <name>` runs every check, not just the first failure:

| Check | What a failure means |
| --- | --- |
| `api` | The daemon's ServiceAccount cannot reach the API server. Its kubeconfig must point at an address the daemon reaches. |
| `master url` | Not set, or plain `http`. Set it with `agentbox cluster set <name> --master-url <url>`. The `http` note is right for a local cluster only. |
| `namespace` | The token is refused, or the Role cannot read pods there. `cluster rotate <name>` makes a new token; `cluster remove` and `add` again reapplies the Role and binding. |
| `nodes` | No node is ready, or the cluster has none. |
| `storage class` | The named class does not exist. Empty asks for the cluster's default; k3d and k3s have `local-path`. |
| `workspace claim` | A 1Gi claim could not be made, or a class that binds at once never bound, which usually means no provisioner. A class that binds on first use passes. |
| `registry` | Skipped when there is no login to apply (no registry, or one that needs none); otherwise the pull secret is missing from the namespace or is not a docker config. Add the cluster again, or rotate the registry. |
| `image` | Skipped when no image is configured; otherwise the canary pod could not run, and the reason is in the detail, such as `ImagePullBackOff` or a pod that exited at once. |

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
- The pod connects out, so nothing in the cluster listens for the daemon. The listener on 7782 must
  not be exposed beyond a local test. A remote cluster reaches the daemon through the tailnet
  listener, which `tailscale serve` publishes and which checks who asks, except on
  `/v1/agent-gateway`, where the pod's own bearer token is the check.
