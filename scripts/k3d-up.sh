#!/usr/bin/env bash
# A local k3d cluster to run agents as pods in, and optionally the registry
# their image is pushed to and pulled from.
#
#   scripts/k3d-up.sh
#   scripts/k3d-up.sh --with-registry
#   scripts/k3d-up.sh --operator --master-host monster.tailnet.ts.net \
#       --git-host hub.tailnet.ts.net
#   scripts/k3d-up.sh --delete
#
# Running it again changes nothing: a cluster, a registry and the operator's
# objects that are already there are left as they are.
#
# It never builds or pushes an image. It prints those commands instead, because
# the image is the repository's binary and the push is the runtime's own login.
# It never takes a token as an argument: --operator reads the Tailscale OAuth
# client from the environment, and nothing here writes it to a file, this
# repository or this script.
set -euo pipefail

# The name the registry is reached by, on this machine and inside the cluster:
# a .localhost name resolves to the loopback address here, and Docker's DNS
# resolves the container of that name on the k3d network. One name for both
# sides is what lets the daemon push and a pod pull the same reference.
registry_port=5000

usage() {
  cat <<'EOF'
Usage: scripts/k3d-up.sh [flags]

A local k3d cluster for agents that run as pods, and the registry to match.
Run it again with the same flags and it changes nothing.

  --name NAME          the cluster's name (default: agentbox)
  --agents N           worker nodes besides the server (default: 1)
  --with-registry      run registry:2 on the cluster's network, over plain HTTP
  --operator           install the Tailscale operator with Helm, and create the
                       egress Services, ProxyGroup, DNSConfig and CoreDNS stub
  --namespace NAME     the namespace agents run in (default: agentbox)
  --master-host HOST   this machine's tailnet name, for --operator
  --git-host HOST      the private git host, for --operator
  --delete             remove the cluster and the registry
  -h, --help           print this and exit

--operator reads TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET
from the environment, refuses to run without them, and never writes them to a
file.

It needs k3d, kubectl and Docker on PATH, and Helm with --operator. It does not
build, push or import an image: it prints the commands to run next, with this
cluster's name in them.
EOF
}

say() { printf '%s\n' "$*"; }
die() {
  printf 'k3d-up: %s\n' "$*" >&2
  exit 1
}

name=agentbox
agents=1
with_registry=0
operator=0
namespace=agentbox
master_host=""
git_host=""
delete_cluster=0

while (($# > 0)); do
  case "$1" in
    --name) [[ $# -ge 2 ]] || die "--name needs a value"; name=$2; shift 2 ;;
    --name=*) name=${1#*=}; shift ;;
    --agents) [[ $# -ge 2 ]] || die "--agents needs a value"; agents=$2; shift 2 ;;
    --agents=*) agents=${1#*=}; shift ;;
    --with-registry) with_registry=1; shift ;;
    --operator) operator=1; shift ;;
    --namespace) [[ $# -ge 2 ]] || die "--namespace needs a value"; namespace=$2; shift 2 ;;
    --namespace=*) namespace=${1#*=}; shift ;;
    --master-host) [[ $# -ge 2 ]] || die "--master-host needs a value"; master_host=$2; shift 2 ;;
    --master-host=*) master_host=${1#*=}; shift ;;
    --git-host) [[ $# -ge 2 ]] || die "--git-host needs a value"; git_host=$2; shift 2 ;;
    --git-host=*) git_host=${1#*=}; shift ;;
    --delete) delete_cluster=1; shift ;;
    -h | --help) usage; exit 0 ;;
    *) die "unknown flag: $1 (see --help)" ;;
  esac
done

[[ $name =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || die "--name takes lowercase letters, digits and hyphens, not $name"
[[ $agents =~ ^[0-9]+$ ]] || die "--agents takes a number, not $agents"
[[ $namespace =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || die "--namespace takes a Kubernetes namespace name, not $namespace"

registry_container="${name}-registry.localhost"
registry_host=$registry_container

namespace_flag=""
if [[ $namespace != agentbox ]]; then
  namespace_flag=" --namespace $namespace"
fi
tailnet_flag=""
if [[ $operator == 1 ]]; then
  tailnet_flag=" --tailnet operator"
fi

if [[ $operator == 1 ]]; then
  [[ -n ${TAILSCALE_OAUTH_CLIENT_ID:-} && -n ${TAILSCALE_OAUTH_CLIENT_SECRET:-} ]] ||
    die "--operator needs TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET in the environment"
  [[ -n $master_host ]] || die "--operator needs --master-host: this machine's name in the tailnet, like monster.tailnet.ts.net"
  [[ -n $git_host ]] || die "--operator needs --git-host: the private git host the pods reach, like hub.tailnet.ts.net"
fi

require() {
  command -v "$1" >/dev/null 2>&1 || die "$1 isn't on PATH, and it is needed for $2"
}
require k3d "the cluster"
require docker "the cluster's nodes and the registry"
require kubectl "the kubeconfig k3d writes"
if [[ $operator == 1 ]]; then
  require helm "the Tailscale operator's chart"
fi

cluster_exists() {
  k3d cluster list --no-headers 2>/dev/null | awk '{print $1}' | grep -Fx "$name" >/dev/null
}

container_exists() {
  docker inspect "$1" >/dev/null 2>&1
}

tmpdir=$(mktemp -d)
kubeconfig="$tmpdir/kubeconfig.yaml"
cleanup() { rm -rf "$tmpdir"; }
trap cleanup EXIT

# --delete removes the registry first: it is attached to the cluster's Docker
# network, and Docker refuses to remove a network that still has endpoints.
if [[ $delete_cluster == 1 ]]; then
  if container_exists "$registry_container"; then
    docker rm -f "$registry_container" >/dev/null
    say "Removed the registry container $registry_container."
  fi
  if cluster_exists; then
    k3d cluster delete "$name"
  else
    say "No cluster named $name: nothing to delete."
  fi
  exit 0
fi

if cluster_exists; then
  say "Cluster $name already exists: leaving it alone."
else
  create=(cluster create "$name" --agents "$agents")
  if [[ $with_registry == 1 ]]; then
    # The cluster's containerd has to be told the registry is plain HTTP, or a
    # pod's pull fails with "server gave HTTP response to HTTPS client".
    cat >"$tmpdir/registries.yaml" <<YAML
mirrors:
  "${registry_host}:${registry_port}":
    endpoint:
      - "http://${registry_host}:${registry_port}"
YAML
    create+=(--registry-config "$tmpdir/registries.yaml")
  fi
  say "Creating cluster $name with $agents worker node(s); this takes a minute."
  k3d "${create[@]}"
fi

if [[ $with_registry == 1 ]]; then
  if cluster_exists && ! docker exec "k3d-${name}-server-0" cat /etc/rancher/k3s/registries.yaml 2>/dev/null | grep -F "$registry_host" >/dev/null; then
    say ""
    say "Cluster $name already existed without the registry mirror, so its pods can't pull"
    say "over HTTP from $registry_host. Either recreate the cluster with --delete and"
    say "this flag, or skip the registry and import the image instead."
  fi
  if container_exists "$registry_container"; then
    say "Registry $registry_host:$registry_port is already running: leaving it alone."
  else
    say "Starting registry:2 as $registry_container on $registry_host:$registry_port."
    docker run -d --name "$registry_container" --network "k3d-${name}" \
      --restart unless-stopped -p "127.0.0.1:${registry_port}:5000" registry:2 >/dev/null
  fi
fi

if [[ $operator == 1 ]]; then
  say "Writing the cluster's kubeconfig for kubectl and Helm."
  k3d kubeconfig get "$name" >"$kubeconfig"
  chmod 600 "$kubeconfig"

  # The egress Services live in the namespace agents run in, which `cluster add`
  # makes later; the operator's own objects stay in its own namespace.
  kubectl --kubeconfig "$kubeconfig" create namespace "$namespace" --dry-run=client -o yaml |
    kubectl --kubeconfig "$kubeconfig" apply -f - >/dev/null

  say "Installing the Tailscale operator with the OAuth client from the environment."
  helm upgrade --install tailscale-operator tailscale-operator \
    --repo https://pkgs.tailscale.com/helmcharts \
    --kubeconfig "$kubeconfig" --namespace tailscale --create-namespace \
    --set-string "oauth.clientId=${TAILSCALE_OAUTH_CLIENT_ID}" \
    --set-string "oauth.clientSecret=${TAILSCALE_OAUTH_CLIENT_SECRET}" \
    --wait --timeout 5m

  kubectl --kubeconfig "$kubeconfig" wait --for condition=established \
    --timeout=120s crd/proxygroups.tailscale.com crd/dnsconfigs.tailscale.com >/dev/null

  # One ProxyGroup serves every agent pod in the namespace: the proxies are the
  # pods that dial the tailnet, and the Services below only declare the names.
  kubectl --kubeconfig "$kubeconfig" apply -f - <<YAML
apiVersion: tailscale.com/v1alpha1
kind: ProxyGroup
metadata:
  name: agentbox-egress
  namespace: ${namespace}
spec:
  type: egress
  replicas: 2
---
apiVersion: v1
kind: Service
metadata:
  name: agentbox-egress-master
  namespace: ${namespace}
  labels:
    agentbox/managed: "true"
    agentbox/tailnet-egress: "true"
  annotations:
    tailscale.com/tailnet-fqdn: ${master_host}
    tailscale.com/proxy-group: agentbox-egress
spec:
  type: ExternalName
  externalName: ${master_host}
  ports:
    - name: https
      port: 443
      protocol: TCP
---
apiVersion: v1
kind: Service
metadata:
  name: agentbox-egress-git
  namespace: ${namespace}
  labels:
    agentbox/managed: "true"
    agentbox/tailnet-egress: "true"
  annotations:
    tailscale.com/tailnet-fqdn: ${git_host}
    tailscale.com/proxy-group: agentbox-egress
spec:
  type: ExternalName
  externalName: ${git_host}
  ports:
    - name: https
      port: 443
      protocol: TCP
    - name: ssh
      port: 22
      protocol: TCP
YAML

  # The in-cluster nameserver the operator runs, and the split DNS that sends
  # tailnet names to it. The CoreDNS stub is what makes a pod's lookup of
  # monster.tailnet.ts.net reach that nameserver.
  kubectl --kubeconfig "$kubeconfig" apply -f - <<'YAML'
apiVersion: tailscale.com/v1alpha1
kind: DNSConfig
metadata:
  name: ts-dns
spec:
  nameserver:
    image:
      repo: tailscale/k8s-nameserver
      tag: unstable
YAML

  say "Waiting for the operator's nameserver."
  nameserver=""
  for _ in $(seq 1 60); do
    nameserver=$(kubectl --kubeconfig "$kubeconfig" get dnsconfig ts-dns \
      -o jsonpath='{.status.nameserver.ip}' 2>/dev/null || true)
    [[ -n $nameserver ]] && break
    sleep 2
  done
  [[ -n $nameserver ]] || die "the DNSConfig ts-dns never got a nameserver IP: check kubectl --kubeconfig $kubeconfig -n tailscale get pods"

  corefile=$(kubectl --kubeconfig "$kubeconfig" -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}')
  if grep -Fq 'ts.net:53' <<<"$corefile"; then
    say "CoreDNS already forwards ts.net to the tailnet nameserver."
  else
    corefile="${corefile}
ts.net:53 {
    errors
    cache 30
    forward . ${nameserver}
}
"
    # create+apply, so kubectl does the YAML quoting of the Corefile.
    kubectl --kubeconfig "$kubeconfig" -n kube-system create configmap coredns \
      --from-literal=Corefile="$corefile" --dry-run=client -o yaml |
      kubectl --kubeconfig "$kubeconfig" -n kube-system apply -f - >/dev/null
    kubectl --kubeconfig "$kubeconfig" -n kube-system rollout restart deployment coredns >/dev/null
    kubectl --kubeconfig "$kubeconfig" -n kube-system rollout status deployment coredns --timeout=120s >/dev/null
    say "CoreDNS now forwards ts.net to $nameserver."
  fi

  say "The operator, the egress Services, the ProxyGroup and the DNSConfig are in place."
  say "Two things are yours: the tailnet ACL has to let tag:k8s reach $master_host on 443"
  say "and $git_host on 443 and 22, and the OAuth client has to own tag:k8s and tag:k8s-operator."
fi

say ""
say "Cluster $name is ready. Next, in this order:"
say ""
say "  k3d kubeconfig get $name > /tmp/$name.yaml"
say ""
if [[ $with_registry == 1 ]]; then
  say "  # the registry, which is plain HTTP and needs no login"
  say "  agentbox registry add local --url $registry_host:$registry_port --insecure"
  say "  scripts/build-agent-image.sh --registry local --tag dev --push"
  say ""
  say "  # the image pods pull: $registry_host:$registry_port/agentbox-agent:dev"
  say "  agentbox cluster add $name --kubeconfig /tmp/$name.yaml \\"
  say "    --storage-class local-path --master-url http://host.k3d.internal:7782 \\"
  say "    --registry local --image agentbox-agent:dev${namespace_flag}${tailnet_flag}"
else
  say "  # the image, built here and imported into the cluster, with no registry"
  say "  scripts/build-agent-image.sh --tag dev"
  say "  k3d image import agentbox-agent:dev -c $name"
  say ""
  say "  agentbox cluster add $name --kubeconfig /tmp/$name.yaml \\"
  say "    --storage-class local-path --master-url http://host.k3d.internal:7782 \\"
  say "    --image agentbox-agent:dev${namespace_flag}${tailnet_flag}"
fi
say ""
say "  # the gateway pods dial; the daemon has to be started with it"
say "  agentbox daemon stop"
say "  AGENTBOX_GATEWAY_ADDR=0.0.0.0:7782 agentbox daemon start"
say ""
say "  agentbox cluster doctor $name"
say ""
say "host.k3d.internal is how a pod reaches this machine. The listener on 7782 is"
say "plain HTTP and serves the gateway alone: it is for a local cluster only."
say "This script builds and pushes nothing."
