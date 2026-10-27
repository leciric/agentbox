#!/usr/bin/env bash
# Restores an AgentBox backup into an installation's default paths. It needs
# nothing but this file, bash and the usual Unix tools, so it also works on a
# fresh machine where AgentBox is not installed yet.
#
#   scripts/restore-agentbox.sh <backup-dir> [--config <dir>] [--data <dir>] [--force]
#
#   --config <dir>  AgentBox's config directory, holding secrets.key and
#                   credentials. Default: $XDG_CONFIG_HOME/agentbox, else
#                   ~/.config/agentbox (internal/paths).
#   --data <dir>    AgentBox's data directory, holding state.db and the
#                   daemon's socket in run/. Default: $XDG_DATA_HOME/agentbox,
#                   else ~/.local/share/agentbox.
#   --force         restore even when the daemon's socket is there.
#
# Two things to know before running it.
#
#   1. Stop the daemon first: agentbox daemon stop. A daemon running while its
#      state.db is replaced keeps writing to a database that is no longer that
#      file, and its socket is what makes this script refuse unless --force.
#   2. Keep the secrets.key of every backup with that backup. The key is not in
#      state.db: a database restored without the key it was sealed under has
#      every secret in it unreadable, and a later state.db restored under an
#      older key is just as broken. A key cannot be recovered, and it cannot be
#      rotated: the only way back is the key from the backup that sealed it.
#
set -euo pipefail

usage() {
  sed -n '2,26p' "$0" | sed -e 's/^# \{0,1\}//'
}

die() {
  printf 'restore-agentbox: %s\n' "$*" >&2
  exit 1
}

# unique_dir is base, or base-2, base-3... so two restores in the same second
# never copy over each other's saved files.
unique_dir() {
  local base=$1 n=2
  while [ -e "$base" ]; do
    base="${1}-${n}"
    n=$((n + 1))
  done
  printf '%s\n' "$base"
}

backup=""
config=""
data=""
force=0

while [ $# -gt 0 ]; do
  case $1 in
    --config)
      [ $# -ge 2 ] || die "--config needs a directory"
      config=$2
      shift 2
      ;;
    --data)
      [ $# -ge 2 ] || die "--data needs a directory"
      data=$2
      shift 2
      ;;
    --force)
      force=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*)
      usage >&2
      exit 2
      ;;
    *)
      [ -z "$backup" ] || die "one backup directory at a time, and '$backup' is already given"
      backup=$1
      shift
      ;;
  esac
done

[ -n "$backup" ] || {
  usage >&2
  exit 2
}

# The defaults are internal/paths.Default's, without its requirement that
# XDG_CONFIG_HOME and XDG_DATA_HOME be absolute and its dependence on a Go
# build: a relative one is ignored there, and here too.
if [ -z "$config" ]; then
  if [ -n "${XDG_CONFIG_HOME:-}" ] && [ "${XDG_CONFIG_HOME#/}" != "$XDG_CONFIG_HOME" ]; then
    config=$XDG_CONFIG_HOME/agentbox
  else
    config=${HOME:?HOME is not set}/.config/agentbox
  fi
fi
if [ -z "$data" ]; then
  if [ -n "${XDG_DATA_HOME:-}" ] && [ "${XDG_DATA_HOME#/}" != "$XDG_DATA_HOME" ]; then
    data=$XDG_DATA_HOME/agentbox
  else
    data=${HOME:?HOME is not set}/.local/share/agentbox
  fi
fi

manifest=$backup/manifest.json
[ -d "$backup" ] || die "$backup is not a directory"
[ -f "$manifest" ] || die "$backup is not an AgentBox backup: it has no manifest.json"
[ -f "$backup/state.db" ] || die "$backup has no state.db"

format=$(sed -n 's/^[[:space:]]*"format": \([0-9][0-9]*\),*$/\1/p' "$manifest" | head -n 1)
[ "$format" = "1" ] || die "$backup is backup format ${format:-unknown}, and this script restores format 1"

socket=$data/run/agentbox.sock
if [ -S "$socket" ] && [ "$force" -eq 0 ]; then
  die "the AgentBox daemon is running ($socket): stop it with 'agentbox daemon stop', then run this again. A running daemon keeps writing to the database this replaces; --force restores anyway."
fi

# check_checksums reads manifest.json a line at a time. The manifest is written
# by internal/backup with one field per line, path before sha256, so no JSON
# parser is needed on a machine that only has bash and coreutils.
check_checksums() {
  local path="" sum="" line="" out="" actual=""
  if ! command -v sha256sum >/dev/null 2>&1; then
    printf 'restore-agentbox: sha256sum is not installed: the checksums in manifest.json were not checked\n' >&2
    return 0
  fi
  while IFS= read -r line; do
    case $line in
      *'"path":'*)
        path=${line#*'"path": "'}
        path=${path%%'"'*}
        ;;
      *'"sha256":'*)
        sum=${line#*'"sha256": "'}
        sum=${sum%%'"'*}
        [ -n "$path" ] || die "manifest.json has a checksum before any path"
        [ -n "$sum" ] || die "manifest.json has no checksum for $path"
        case $path in
          "" | /* | .. | ../* | */.. | */../*)
            die "manifest.json names '$path', which is not inside the backup"
            ;;
        esac
        [ -f "$backup/$path" ] || die "$backup/$path is missing, and manifest.json lists it"
        out=$(sha256sum -- "$backup/$path") || die "cannot read $backup/$path"
        actual=${out%% *}
        [ "$actual" = "$sum" ] || die "$path does not match manifest.json: the backup is incomplete or was changed"
        path=""
        ;;
    esac
  done <"$manifest"
}

check_checksums

# Everything that is about to be replaced is copied aside first, so a restore
# of the wrong backup is undone by copying it back.
stamp=$(date +%Y%m%d-%H%M%S)
saved_config=""
saved_data=""
mkdir -p "$config" "$data"

if [ -e "$config/secrets.key" ] || [ -e "$config/credentials" ]; then
  saved_config=$(unique_dir "$config/agentbox.before-restore-$stamp")
  mkdir -p "$saved_config"
  [ ! -e "$config/secrets.key" ] || cp -p "$config/secrets.key" "$saved_config/"
  [ ! -e "$config/credentials" ] || cp -R -p "$config/credentials" "$saved_config/"
fi

if [ -e "$data/state.db" ] || [ -e "$data/state.db-wal" ] || [ -e "$data/state.db-shm" ]; then
  saved_data=$(unique_dir "$data/agentbox.before-restore-$stamp")
  mkdir -p "$saved_data"
  for f in state.db state.db-wal state.db-shm; do
    [ ! -e "$data/$f" ] || cp -p "$data/$f" "$saved_data/"
  done
fi

# state.db arrives without the -wal and -shm of the database it replaces: the
# copy inside the backup is one whole file already (VACUUM INTO), and an old
# WAL beside a new database is worse than no WAL at all.
rm -f "$data/state.db-wal" "$data/state.db-shm"
cp "$backup/state.db" "$data/state.db"
chmod 600 "$data/state.db"

if [ -f "$backup/secrets.key" ]; then
  cp "$backup/secrets.key" "$config/secrets.key"
  chmod 600 "$config/secrets.key"
else
  printf 'restore-agentbox: this backup has no secrets.key: every secret sealed in it stays unreadable\n' >&2
fi

if [ -d "$backup/credentials" ]; then
  rm -rf "$config/credentials"
  cp -R "$backup/credentials" "$config/credentials"
  find "$config/credentials" -type d -exec chmod 700 {} +
  find "$config/credentials" -type f -exec chmod 600 {} +
elif [ -e "$config/credentials" ]; then
  printf 'restore-agentbox: this backup has no credentials: %s was left as it was\n' "$config/credentials" >&2
fi

printf 'Restored %s\n' "$backup"
printf '  config: %s\n' "$config"
printf '  data:   %s\n' "$data"
if [ -n "$saved_config" ]; then
  printf '  the files replaced here are in %s\n' "$saved_config"
fi
if [ -n "$saved_data" ]; then
  printf '  the database replaced here is in %s\n' "$saved_data"
fi
printf 'Start AgentBox when it is installed: agentbox daemon\n'
