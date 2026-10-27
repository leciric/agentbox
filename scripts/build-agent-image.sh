#!/usr/bin/env bash
# Builds the container image agents run as in a Kubernetes cluster.
#
# The image copies the agentbox binary out of bin/agentbox in the build
# context, so the binary is built here first, for linux/amd64, which is the
# platform the image is pinned to. Everything after that is the Dockerfile's
# business and "agentbox image agent build"'s.
#
#   scripts/build-agent-image.sh --tag dev
#   scripts/build-agent-image.sh --tag 0.3.0 --registry forgejo --push
#
# It needs Docker, or Podman when Docker is not there, and a "docker login" for
# a registry a push goes to. Nothing here asks for a credential.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/agentbox ./cmd/agentbox
exec ./bin/agentbox image agent build "$@"
