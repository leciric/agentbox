---
name: troubleshooting-setup
description: Fix a failing AgentBox setup check (agentbox doctor) handed over from Setup. Use when a message says a doctor check failed, or the user asks why AgentBox's setup doesn't work.
---

# Troubleshooting AgentBox's setup

Setup sent you a check of `agentbox doctor` that failed, with what it found, its suggested fix
and the end of the daemon's log. Find out why it fails, fix what you can, and say plainly what's
left for the user.

## Where you are

You run where the AgentBox daemon runs. On Linux and macOS that is AgentBox's VM, not the user's
own machine: the user's home directory is shared with it at the same path, but their desktop,
their BIOS, their groups and their package manager are out of your reach. On a machine from
before the VM (host mode) or under WSL you are on that machine itself, without root unless
`sudo -n true` works.

## What you can do from here

- `agentbox doctor` (add `--json` to read it exactly) runs every check again; `agentbox doctor
  --fix` retries what fixes itself: the base image download, the chat's AI tool and the GitHub
  CLI. Run it before anything else, and again at the end to show the fix worked.
- Read the daemon's log: `~/.local/share/agentbox/daemon.log` (`tail -n 200`, `grep -i error`).
- Look at the disks (`df -h`, `du -sh ~/.local/share/agentbox/*`), the network (`curl -sSI
  https://downloads.agentbox.linting.dev/images`, `curl -sSI https://api.github.com`), DNS,
  proxies (`env | grep -i proxy`).
- Incus: `incus info`, `incus storage list`, `incus image list`, `systemctl status incus`.
- `agentbox image build` makes the base image; `agentbox jobs` follows it.
- Ask before removing anything of the user's (agents, worktrees, images, files) to free space.

## What only the user can do

Give these as commands or steps for them to run on their own machine, and say why. Never claim
you ran them.

- Turning on virtualization (Intel VT-x or AMD-V, "SVM") in the BIOS or UEFI settings.
- Joining the `kvm` group (`sudo usermod -aG kvm "$USER"`, then logging out and back in), or
  `sudo agentbox host setup`, which does it and the rest of host setup.
- Installing Lima on a Mac (`brew install lima`), or updating macOS.
- `agentbox vm start`, `agentbox vm stop` and `agentbox vm init` on their machine, for a VM that
  is stopped, stuck or was never made.
- Logging in: `agentbox auth claude` (or `codex`, `opencode`, `cursor`) and `agentbox auth github`
  open a browser, and only the user can approve them.
- Freeing space on their own disk outside AgentBox's folders.

## How to answer

Say in a sentence what was wrong, what you changed, and what the user has to run, if anything, in
a code block each. If you can't tell why it fails, say what you checked and what you'd look at
next; don't guess a fix.
