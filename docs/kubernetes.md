# Agents in a Kubernetes cluster

[← back to the index](README.md)

An agent's machine can be a pod in a Kubernetes cluster instead of an Incus container here. The
daemon and `state.db` stay on this machine; each agent gets a pod and a claim, and dials the daemon,
so no Service, Ingress or inbound rule is needed.

## What works today

| Works | Not yet |
| --- | --- |
| `cluster add`, `list`, `set`, `remove`, `rotate`, `doctor` | the clone onto the claim, the project git profiles |
| `registry add`, `list`, `test`, `rotate`, `remove` | Claude Code, Codex, OpenCode, their adapters, a desktop |
| `image agent build`, `--push` | a browser, screenshots, snapshots, restore, fork |
| the pod lifecycle: create, adopt, stop, destroy | disk usage, placement, capacity, a queue |
| the gateway: commands, files, a terminal, ports, control | an agent's cluster or pod in the app |
| `create --cluster <name>` | reading a pod's files, its diff, its editor |

`cluster add` (`internal/cli/cluster.go`, `internal/k8s`) keeps a namespace-scoped ServiceAccount
token sealed and never keeps the admin kubeconfig. `cluster doctor` makes a 1Gi canary claim and,
when an image is configured, a pod of it, and deletes both. Destroy deletes the pod, its token Secret
and its claim at once, with no retention window. An agent created against a cluster today stops at
"the workspace has no git repository", and `--ai none` is the tool the image can start.

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
ca-certificates, git, tmux, curl, jq, less, openssh-client, procps, python3, unzip, xz-utils, Go, Node
and the `agentbox` binary. It runs as `agent`, UID and GID 1000, with `/workspace` and `/home/agent`
on the claim, and its entrypoint is `agentbox agentd`; there is no systemd.

It does not have Claude Code, Codex, OpenCode, their ACP adapters, pnpm, the GitHub CLI, the
Playwright MCP server or a desktop (Chromium, Xvnc, openbox, ffmpeg). An agent whose AI tool is one
of those has nothing to start in this image yet.

```bash
scripts/build-agent-image.sh --tag dev                            # this repository's binary, linux/amd64
agentbox image agent build --tag dev                              # the binary you are running
agentbox image agent build --tag dev --registry forgejo --push   # tag and push it
```

The private image must never go to a public registry. A variant for a public registry, without
Claude Code and the adapters, is not made yet.

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
agentbox create demo --cluster agentbox --ai none
```

The doctor says whether the API, the master URL, the namespace, the nodes, the storage class, the
claim, the registry and the image are all right. `agentbox create` then waits up to five minutes for
the pod's `agentd` to open its session; with no repository on the claim it stops first, which is the
missing clone above.

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
