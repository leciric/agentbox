#!/bin/sh
# Runs as root of a new user and mount namespace (try.sh starts it with
# `unshare --user --map-root-user --mount`): replaces DRI_DIR (/dev/dri) by a
# tmpfs holding only NODE, says so on stderr (crosvm.log), then runs COMMAND
# as UID:GID again, in a user namespace of its own.
#
#   gpu-ns.sh NODE DRI_DIR UID GID COMMAND...
#
# Why: crosvm's GL (virglrenderer) takes the first renderD* in /dev/dri, and
# can't be told otherwise (rutabaga 0.1.75's get_drm_fd never accepts a render
# node: it checks Path::starts_with("/dev/dri/renderD"), which compares whole
# components). With one node there, GL, EGL and Venus's Vulkan in
# virgl_render_server (crosvm's child) all see only that GPU.
set -eu
node=$1 dri=$2 uid=$3 gid=$4
shift 4
name=$(basename "$node")

d=$(mktemp -d)
# -n: in a user namespace, util-linux's mount (2.39, Ubuntu 24.04) mounts but
# fails writing /run/mount/utab, and says so with a non-zero exit.
mount -n -t tmpfs -o mode=755 tmpfs "$d"
touch "$d/$name"
mount -n --bind "$node" "$d/$name"
mount -n --move "$d" "$dri"
rmdir "$d"
echo "chv-gpu: $dri in crosvm's namespace: $(ls "$dri" | tr '\n' ' ')(device $(stat -L -c '%t:%T' "$dri/$name"))" >&2

if unshare --help 2>&1 | grep -q -- '--map-user'; then
	exec unshare --user --map-user="$uid" --map-group="$gid" -- "$@"
fi
# util-linux before 2.38: crosvm stays root of the namespace (still the user outside).
exec "$@"
