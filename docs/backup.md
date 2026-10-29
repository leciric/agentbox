# Backups and restoring

AgentBox keeps its own state in two directories, resolved by
[`internal/paths`](../internal/paths/paths.go):

- the data directory, `~/.local/share/agentbox` (`$XDG_DATA_HOME/agentbox`), holding `state.db`:
  projects, agents, chats, memory, tasks, tokens and settings, including the clusters and
  registries agents run in and their sealed credentials;
- the config directory, `~/.config/agentbox` (`$XDG_CONFIG_HOME/agentbox`), holding `secrets.key`
  and `credentials/`: the Claude Code, Codex, OpenCode, Cursor and GitHub logins, and each
  project's git credentials.

`agentbox backup` copies both into one directory you can carry to another machine.

## What a backup holds

```
agentbox-backup-20261027-033000/
  state.db        the database, one whole file
  secrets.key     the AES key the secrets are sealed under, when this machine has one
  credentials/    the logins, when this machine has any
  manifest.json   the format, time, AgentBox version, sqlite user_version, and a SHA-256 per file
```

The database is copied with SQLite's `VACUUM INTO`, not with `cp`. A plain copy of a live database
is not a database: its latest transactions can be in the `-wal` file beside it, and a copy taken
mid-write can mix pages from before and after a transaction. `VACUUM INTO` reads a consistent
snapshot through the WAL from a connection opened read-only, and writes it as one file with no
`-wal` beside it. A daemon may keep running, and the backup changes nothing it reads.

The directory is mode 0700 and every file in it is 0600: it is as sensitive as the installation it
came from, and its `secrets.key` opens every secret in its `state.db`. Treat a copy of it like the
machine's own logins.

A machine that never created a key or a login has neither file. That is not an error, and
`manifest.json` lists what was absent under `absent`, so a restore can tell "there was never a key
here" from "the copy lost it".

## Taking one

```bash
agentbox backup --to ~/agentbox-backups
agentbox backup verify ~/agentbox-backups/agentbox-backup-20261027-033000
```

`--to` is required and has no default: a backup on the same disk as the original is not a backup.
The command reads the files itself, so it works while the daemon is down, and it does not need one.

On an installation that runs in AgentBox's VM, the command runs inside that VM, where the state is.
Your home directory is shared with the VM at the same path (`internal/hostvm`), so `--to` a
directory in your home works; a mount outside it, such as `/mnt/nas`, must be mounted inside the VM
too, or the backup must be carried there afterwards.

`backup verify` writes nothing. It checks every file's SHA-256 and size against the manifest, opens
the copied database read-only and runs SQLite's `integrity_check`, and checks that `secrets.key`
decodes to a 32-byte key. Run it after copying a backup somewhere, and before you rely on one.

## Restoring

Restoring is [`scripts/restore-agentbox.sh`](../scripts/restore-agentbox.sh), not a subcommand. It
needs bash and the tools every Unix has and nothing else, so it works on a machine where AgentBox is
not installed yet, which is the machine a restore is usually for.

```bash
scripts/restore-agentbox.sh agentbox-backup-20261027-033000
scripts/restore-agentbox.sh agentbox-backup-20261027-033000 --config /tmp/agentbox --data /tmp/agentbox-data
```

Without `--config` and `--data` it writes to the same paths AgentBox uses:
`$XDG_CONFIG_HOME/agentbox` and `$XDG_DATA_HOME/agentbox`, falling back to `~/.config/agentbox`
and `~/.local/share/agentbox`. `--config` is the `agentbox` directory itself, not the XDG base.

What it does:

1. Refuses when the daemon's socket is at `<data>/run/agentbox.sock`, because a running daemon
   keeps writing to the database being replaced. Stop it first (`agentbox daemon stop`), or pass
   `--force`.
2. Verifies every checksum in `manifest.json` when `sha256sum` is installed, and stops on a
   mismatch.
3. Copies the current `state.db` (with any `-wal` and `-shm`), `secrets.key` and `credentials/` to
   `agentbox.before-restore-<timestamp>/` beside them, so the wrong backup is undone by copying
   back.
4. Installs `state.db` on its own, without the old `-wal` and `-shm`, and sets mode 0600 on it, on
   `secrets.key` and on every file under `credentials/`.

On a fresh machine, restore before AgentBox is started for the first time: install it if you like,
but do not open the app, and run the script before `agentbox daemon` ever runs. If the daemon has
already started, stop it first (`agentbox daemon stop`). The rest of an installation (the VM, the
base image, the caches) is made on its own.

## What is not in a backup

These are separate, they are much bigger, and most of them are rebuildable:

- the agents' worktrees (`paths.Worktrees`, `~/.local/share/agentbox/worktrees`, or
  `$AGENTBOX_WORKTREES`), which hold the agents' checkouts;
- the agents' machines: an Incus container on this host, or a cluster agent's claim, which holds its
  `/workspace` and `/home/agent` in the cluster and is not copied here;
- the cluster's own objects (its namespace, ServiceAccount, token Secret, pull secret and
  NetworkPolicy), which are applied there and are not in `state.db`;
- media (`paths.Media`, `$AGENTBOX_MEDIA`) and the pictures sent in chats (`paths.ChatImages`);
- the cluster agents' machine snapshots (`paths.Snapshots`, `$AGENTBOX_SNAPSHOTS`), the archives a
  restore and a fork read back into a claim;
- the Home chat's working directory (`paths.HomeChat`, `~/.agentbox`);
- the VM and its disk (`paths.VM`), the image and package caches, the agents' `node_modules`, the AI
  tools installed for leads (`paths.Tools`), each lead's home directory, and `daemon.log`.

So a restored database brings back the projects, chats, memory and settings, and names worktrees and
machines that are not there. The work itself is in each project's git remote, and in the branches
that were pushed; give an agent on this host its machine back with `agentbox recreate
<project/agent>`, and check its branch out again in the project when its worktree is gone. A cluster
agent's work comes back with `recreate` only while its claim is still in the cluster; if the claim is
gone, its snapshot archive (which a backup does not hold) is the only way back, or else it starts
again from its branch.

## Why the key matters

The values of secrets are sealed with AES-256-GCM under `secrets.key` before they are written to
`state.db`. That is by design: a copy of `state.db` on its own is ciphertext, so it can travel or be
synced without carrying the API keys in it.

It also means the key is not a convenience. Restore a `state.db` without the key it was sealed under
and every project and agent comes back, and every secret in it is unreadable: agents get no keys, and
no command shows them again. Restoring an older key over a newer database breaks the same way. The
key cannot be recovered and it cannot be rotated: the only copy that works is the one from the backup
that sealed the database. Keep each `secrets.key` with its own backup, and do not delete a key to
"start over" unless you mean to lose every secret.

## A nightly backup

On Linux, a systemd user timer. This one writes to `~/agentbox-backups`, a directory Syncthing can
carry to the other machines; for a NAS, use the path it is mounted at (see [Taking
one](#taking-one), or add `RequiresMountsFor=/mnt/nas` so the run waits for the mount). `Persistent`
catches up after a machine that was off.

`~/.config/systemd/user/agentbox-backup.service`:

```ini
[Unit]
Description=Back up AgentBox's state and secrets

[Service]
Type=oneshot
ExecStart=/usr/bin/agentbox backup --to %h/agentbox-backups
```

`~/.config/systemd/user/agentbox-backup.timer`:

```ini
[Unit]
Description=Nightly AgentBox backup

[Timer]
OnCalendar=*-*-* 03:30
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now agentbox-backup.timer
systemctl --user list-timers agentbox-backup.timer
```

Use the path `command -v agentbox` prints if it is not `/usr/bin/agentbox`. When the destination is
a Syncthing folder, an incomplete copy can reach the other machine: `state.db` is written before
`manifest.json`, and a restore refuses a directory without a manifest or with a checksum that does
not match, so a half-synced backup is caught rather than restored. The same applies to a NAS that
was unmounted: check that the directory exists, and run `agentbox backup verify` on the newest one
now and then.

To list and check the newest backup:

```bash
ls -dt ~/agentbox-backups/agentbox-backup-* | head -1
agentbox backup verify "$(ls -dt ~/agentbox-backups/agentbox-backup-* | head -1)"
```
