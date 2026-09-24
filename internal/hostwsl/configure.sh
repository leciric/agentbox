#!/bin/sh
# Makes a fresh Ubuntu WSL distro AgentBox's: run as root by `agentbox wsl init`
# (setup.go), through stdin, with the Linux user to make as $1. Safe to run
# again. What Incus itself needs is `agentbox host setup`'s, run after this.
set -eu
user="$1"

# The user AgentBox runs as, named after the Windows user, with the sudo a
# Linux user of AgentBox has: host setup and the app's Setup page use it.
if ! id "$user" >/dev/null 2>&1; then
	useradd --create-home --shell /bin/bash --groups sudo "$user"
fi
echo "$user ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/agentbox-wsl
chmod 0440 /etc/sudoers.d/agentbox-wsl

# systemd, which Incus runs under (the image has it on already); the user as
# the distro's default, so wsl.exe without --user is that user; and Windows's
# PATH kept out of Linux's, where its hundreds of /mnt/c entries slow every
# command lookup and can shadow Linux tools with Windows ones (git.exe).
cat >/etc/wsl.conf <<CONF
# Written by agentbox wsl init.
[boot]
systemd=true

[user]
default=$user

[interop]
appendWindowsPath=false
CONF

echo "configured $user"
