#!/usr/bin/env bash
# Installs, removes or checks the base image's pinned agent tools with mise, as
# the user. Runs as root inside the image: provision.sh runs it on a new build,
# and UpdateTools on an existing base, to move the tools on without a rebuild.
#
# Usage: tools.sh install|remove|verify <user> <list>
#
# The list is what internal/image wrote from tools.txt, for the image's
# components: one tool a line, mise's name for it with its version, a tab, and
# the command that proves it works.
set -euo pipefail
MODE=$1 USER_NAME=$2 LIST=$3
as_user() { runuser -l "$USER_NAME" -c "$*"; }

specs=()
checks=()
while IFS=$'\t' read -r spec check; do
  [[ -n $spec ]] || continue
  specs+=("$spec")
  checks+=("$check")
done <"$LIST"
[[ ${#specs[@]} -gt 0 ]] || exit 0

case $MODE in
  install)
    as_user "export MISE_YES=1 && mise use -g ${specs[*]}"
    # A version this replaced stays installed until pruned; nothing but the
    # global config refers to one in a base image.
    as_user 'mise prune --yes' || true
    ;;
  remove)
    names=()
    for spec in "${specs[@]}"; do names+=("${spec%@*}"); done
    as_user "mise unuse -g ${names[*]}"
    as_user 'mise prune --yes' || true
    ;;
  verify)
    for i in "${!specs[@]}"; do
      printf '%s: ' "${specs[$i]}"
      as_user "${checks[$i]}" || { echo "${specs[$i]} doesn't work: ${checks[$i]} failed" >&2; exit 1; }
      echo
    done
    ;;
  *)
    echo "tools.sh: unknown mode $MODE" >&2
    exit 2
    ;;
esac
