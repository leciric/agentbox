#!/usr/bin/env bash
# A repeatable end-to-end proof of the Kubernetes path: a throwaway k3d
# cluster, a daemon driving it from a container, and one agent's pod.
#
# What it proves, all of it through the command line:
#   - `cluster add` applies AgentBox's access to a real API server, and
#     `cluster doctor` passes every check, the NetworkPolicy included;
#   - `create --cluster` makes a pod from an imported image, and the pod clones
#     the project's private git profile from a git daemon on the same network;
#   - `exec` runs inside the pod and returns the command's own exit code;
#   - `stop` refuses `exec` with the stopped sentence, `start` brings the pod
#     back on its claim, and `exec` works again;
#   - `destroy` removes the agent, and `list` no longer has it.
#
# What it does not prove: the production agent image (internal/agentimage takes
# more than ten minutes and several GiB, and this test image carries no AI
# tool), a chat with a real login, a pull from a registry, the tailnet, or the
# desktop in a pod. The cluster is k3d on this machine's Docker, not a real
# cluster, and the git daemon is plain `git://` on the k3d network.
#
# It needs docker, k3d, kubectl and Go on PATH, on a Linux host: the daemon and
# the kubeconfig reach the server node by its address on the Docker network. It
# owns the cluster k8s-e2e and the containers e2e-gitd and e2e-daemon, which it
# removes before it starts and again on exit, so a rerun after a failure is
# clean. Everything else it writes is in a mktemp directory. It never prints the
# kubeconfig or a token: the kubeconfig is pointed at the cluster's own node and
# the daemon gets it over its socket.
set -euo pipefail

cluster=k8s-e2e
namespace=agentbox
image=agentbox-e2e:k8s
gitd=e2e-gitd
daemon=e2e-daemon
network=k3d-$cluster
node=k3d-$cluster-server-0

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

say() { printf '\n== %s\n' "$*"; }
die() {
  printf 'k8s-e2e: %s\n' "$*" >&2
  exit 1
}

missing=()
for tool in docker k3d kubectl go; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
if ((${#missing[@]} > 0)); then
  printf 'k8s-e2e: missing %s on PATH.\n\n' "${missing[*]}" >&2
  cat >&2 <<'EOF'
This runs a real cluster, so it needs each of these:
  Docker   https://docs.docker.com/engine/install/
  k3d      curl -sfL https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | TAG=v5.9.0 bash
  kubectl  https://kubernetes.io/docs/tasks/tools/
  Go       https://mise.jdx.dev (mise use go), to build bin/agentbox
EOF
  exit 1
fi

docker info >/dev/null 2>&1 ||
  die "Docker isn't answering: is the daemon running, and is this user in the docker group?"

tmp=${TMPDIR:-/tmp}
[[ -d $tmp ]] || tmp=/tmp
work=$(mktemp -d "$tmp/k8s-e2e.XXXXXX")
home=$work/home
kubeconfig=$work/kubeconfig.yaml
fixtures=$work/fixtures
mkdir -p "$home" "$fixtures/git"

# bin/agentbox is this build's, and the CLI and the daemon in the container are
# the same file. HOME and the XDG directories are the mktemp tree on both
# sides, with an explicit XDG_DATA_HOME so a setting in this shell cannot move
# the socket away from the daemon's. AGENTBOX_NO_AUTOSTART keeps a CLI call
# from starting a daemon of its own when the container's isn't up yet.
bin=$root/bin/agentbox
ab() {
  env \
    HOME="$home" \
    XDG_DATA_HOME="$home/.local/share" \
    XDG_CONFIG_HOME="$home/.config" \
    AGENTBOX_FRONT_END=host \
    AGENTBOX_NO_AUTOSTART=1 \
    DO_NOT_TRACK=1 \
    "$bin" "$@"
}

status=0
cleanup() {
  status=$?
  trap - EXIT
  set +e
  if ((status != 0)); then
    printf '\n== What the daemon and the cluster said\n' >&2
    if docker inspect "$daemon" >/dev/null 2>&1; then
      printf -- '-- docker logs %s\n' "$daemon" >&2
      docker logs "$daemon" 2>&1 >&2
    fi
    if [[ -s $kubeconfig ]]; then
      printf -- '-- pods, from kubectl --kubeconfig <the test kubeconfig>\n' >&2
      kubectl --kubeconfig "$kubeconfig" -n "$namespace" get pods -o wide >&2
    fi
  fi
  docker rm -f "$daemon" "$gitd" >/dev/null 2>&1
  k3d cluster delete "$cluster" >/dev/null 2>&1
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

# A cluster or a container of these names from an earlier run is this script's,
# and is removed rather than reused: the cluster would have the wrong
# kubeconfig and the containers the wrong addresses.
remove_leftovers() {
  local leftovers=() c
  for c in "$daemon" "$gitd"; do
    docker inspect "$c" >/dev/null 2>&1 && leftovers+=("$c")
  done
  if ((${#leftovers[@]} > 0)); then
    say "Removing the leftover container(s) ${leftovers[*]}."
  fi
  docker rm -f "$daemon" "$gitd" >/dev/null 2>&1 || true
  if k3d cluster list --no-headers 2>/dev/null | awk '{print $1}' | grep -Fxq "$cluster"; then
    say "Removing the leftover cluster $cluster."
    k3d cluster delete "$cluster" >/dev/null 2>&1 || true
  fi
  # k3d makes the network itself and refuses one that is already there.
  docker network rm "$network" >/dev/null 2>&1 || true
}

wait_for() { # what, seconds, command...
  local what=$1 seconds=$2 i
  shift 2
  for ((i = 0; i < seconds; i++)); do
    "$@" >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "$what did not answer in ${seconds}s"
}

say "Removing anything left from an earlier run."
remove_leftovers

say "Creating cluster $cluster, with no worker nodes."
scripts/k3d-up.sh --name "$cluster" --agents 0

# The kubeconfig k3d writes names the load balancer at 0.0.0.0:<random port>,
# which a container cannot dial and whose certificate isn't for it. The server
# node's own address on the k3d network is covered by its certificate, and that
# is what the daemon uses; this is the only line of the config that changes.
say "Pointing the kubeconfig at the server node on the k3d network."
k3d kubeconfig get "$cluster" >"$kubeconfig"
chmod 600 "$kubeconfig"
node_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$node")
[[ -n $node_ip ]] || die "$node has no address on the k3d network"
sed -E "s#^( *server: https://)[^ ]+\$#\1${node_ip}:6443#" "$kubeconfig" >"$kubeconfig.new"
mv "$kubeconfig.new" "$kubeconfig"
chmod 600 "$kubeconfig"
grep -qF "server: https://$node_ip:6443" "$kubeconfig" ||
  die "could not point the kubeconfig at $node_ip:6443"

say "Building bin/agentbox for linux/amd64."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/agentbox ./cmd/agentbox

say "Building the test image $image."
docker build \
  --build-arg "DAEMON_UID=$(id -u)" \
  --build-arg "DAEMON_GID=$(id -g)" \
  -f scripts/k8s-e2e.Dockerfile \
  -t "$image" \
  .

say "Importing $image into the cluster."
k3d image import "$image" -c "$cluster"

# One commit on main is what an agent branches from, and the bare clone is what
# the git daemon serves. The working clone's origin is the git:// URL, so the
# daemon's own fetch of the base branch reaches the git daemon, and the
# project's prefilled public profile names a URL a pod can use.
say "Making the fixture project, served at git://$gitd/demo.git."
git init -q --initial-branch=main "$work/demo-src"
printf '# demo\n\nA fixture for scripts/k8s-e2e.sh.\n' >"$work/demo-src/README.md"
git -C "$work/demo-src" add README.md
git -C "$work/demo-src" -c user.name='AgentBox E2E' -c user.email='e2e@example.invalid' \
  commit -q -m 'The fixture project'
git clone -q --bare "$work/demo-src" "$fixtures/git/demo.git"
git clone -q "$fixtures/git/demo.git" "$fixtures/demo"
git -C "$fixtures/demo" remote set-url origin "git://$gitd/demo.git"

say "Starting the git daemon."
docker run -d --name "$gitd" \
  --network "$network" \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -v "$fixtures/git:/srv/git:ro" \
  --entrypoint git \
  "$image" daemon --base-path=/srv/git --export-all --reuseaddr --informative-errors >/dev/null

# The daemon is the same image, with the same uid as this shell: the socket
# under HOME is only reachable by its owner, and os/user refuses a uid the
# image has no passwd entry for.
say "Starting the daemon, with its state in the test HOME."
docker run -d --name "$daemon" \
  --network "$network" \
  --user "$(id -u):$(id -g)" \
  --entrypoint /usr/local/bin/agentbox \
  -e HOME=/home/agentbox \
  -e AGENTBOX_FRONT_END=host \
  -e AGENTBOX_GATEWAY_ADDR=0.0.0.0:7782 \
  -e DO_NOT_TRACK=1 \
  -v "$home:/home/agentbox" \
  -v "$fixtures:/fixtures" \
  "$image" daemon >/dev/null

daemon_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$daemon")
[[ -n $daemon_ip ]] || die "$daemon has no address on the k3d network, so no pod could reach it"

wait_for "the daemon's API" 60 ab cluster list
wait_for "the git daemon, as $daemon sees it" 60 \
  docker exec "$daemon" git ls-remote "git://$gitd/demo.git"
say "The daemon is on $daemon_ip:7782 and the git daemon answers at git://$gitd/demo.git."

say "Adding the cluster, with this daemon's address and the imported image."
ab cluster add "$cluster" \
  --kubeconfig "$kubeconfig" \
  --namespace "$namespace" \
  --storage-class local-path \
  --master-url "http://$daemon_ip:7782" \
  --image "$image"

say "Running the doctor, which makes and deletes its own canaries."
doctor=""
if ! doctor=$(ab cluster doctor "$cluster" 2>&1); then
  printf '%s\n' "$doctor"
  die "cluster doctor failed: the first failing check is above"
fi
printf '%s\n' "$doctor"
grep -qE '^  network policy +ok' <<<"$doctor" ||
  die "the doctor did not report the network policy check as ok"
grep -qF "$cluster can run agents." <<<"$doctor" ||
  die "the doctor did not say $cluster can run agents"

say "Adding the fixture as a project, with the git daemon as its private profile."
ab add /fixtures/demo
# git:// asks for no login, but a token profile is what a pod's clone is given,
# so the profile carries one: a placeholder, never printed, that git never
# sends to a git:// remote.
printf '%s\n' 'e2e-no-login' |
  ab project git set demo private "git://$gitd/demo.git" \
    --auth token --credential e2e --from-file -
ab project git use demo private

say "Creating demo/agent-01, which waits for the pod's session."
ab create demo --cluster "$cluster" --ai none

# A create returns when the pod's agentd has connected, and the very next
# command has to work: a just-created agent used to read as stopped until the
# reconciler's next round wrote the pod into its row, and this is the assertion
# that would catch it coming back. Nothing below is retried.
# exec's words are joined into one shell command, as ssh does, so the quoting
# the pod's sh needs has to survive this shell: hence the one quoted word.
say "Proving exec runs inside the pod."
pod=ab-demo-agent-01
name=""
if ! name=$(ab exec demo/agent-01 -- "sh -c 'test -d .git && hostname'"); then
  die "exec in demo/agent-01 failed: 'test -d .git' did not pass, so the worktree is not in the pod"
fi
[[ $name == "$pod" ]] ||
  die "exec printed \"$name\", and the pod's own hostname is $pod"
printf "  ok: exec printed %s, the pod's own hostname, and exited 0\n" "$name"

say "Proving exec returns the command's own exit code."
code=0
ab exec demo/agent-01 -- "sh -c 'exit 7'" || code=$?
[[ $code == 7 ]] || die "exec of a command that exits 7 exited $code instead"
printf '  ok: a command that exits 7 made exec exit 7\n'

say "Proving stop refuses exec, and start brings the pod back."
ab stop demo/agent-01
code=0
out=$(ab exec demo/agent-01 -- "sh -c 'true'" 2>&1) || code=$?
[[ $code != 0 ]] || die "exec was accepted while demo/agent-01 was stopped"
[[ $out == *"demo/agent-01 is stopped: run agentbox start demo/agent-01"* ]] ||
  die "exec on the stopped agent was refused with: $out"
printf '  ok: exec was refused with "demo/agent-01 is stopped: run agentbox start demo/agent-01"\n'
ab start demo/agent-01
name=""
if ! name=$(ab exec demo/agent-01 -- "sh -c 'test -d .git && hostname'"); then
  die "exec did not work again after start"
fi
[[ $name == "$pod" ]] ||
  die "after start, exec printed \"$name\" instead of the pod's hostname $pod"
printf '  ok: start brought the pod back on its claim, and exec printed %s again\n' "$name"

say "Proving destroy removes the agent."
ab destroy demo/agent-01
if ab list | grep -Fq 'demo/agent-01'; then
  die "agentbox list still shows demo/agent-01 after destroy"
fi
printf '  ok: agentbox list no longer has demo/agent-01\n'

say "All of it held. Removing the cluster and the containers."
