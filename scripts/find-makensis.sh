#!/usr/bin/env bash
# Finds makensis, the compiler of NSIS (the native app's Windows installer,
# native/packaging/windows/installer.nsi), and prints where it is:
#
#   scripts/find-makensis.sh             the path, or exit 1 and print nothing
#   scripts/find-makensis.sh --install   the same, installing NSIS first when it
#                                        isn't there, and failing if it still isn't
#
# GitHub's Windows runners don't have NSIS (windows-2025-vs2026 20261116 doesn't),
# so the jobs that make the installer install it. With Chocolatey first, which
# is how it was done and which fails for long stretches: on 24 Nov 2026
# community.chocolatey.org answered 503 and 504 for over half an hour, and
# `choco install` printed "Unable to find package", "installed 0/0 packages" and
# exited 0 (nightly run 45326834657). Its exit status says nothing, so this asks
# where makensis is afterwards, which is the only proof. Waiting longer doesn't
# help a stretch like that, so after two tries it unpacks NSIS's portable zip
# from SourceForge instead, checked against the SHA-256 pinned below. A workflow
# step runs it, so a missing NSIS fails there and not minutes later in
# native-package.sh:
#
#   makensis=$(scripts/find-makensis.sh --install)
#   echo "MAKENSIS=$makensis" >>"$GITHUB_ENV"
#
# The path is printed the way Windows spells it (C:/Program Files (x86)/NSIS/...)
# when cygpath is there, which bash and makensis's callers both take; everything
# else goes to stderr.
#
# Moving NSIS on: the zip is only the fallback, so an older one than
# Chocolatey's is fine. To change it, set nsis_version and nsis_zip_sha256 (from
# the download, https://sourceforge.net/projects/nsis/files/NSIS%203/).
set -euo pipefail

nsis_version=3.13
nsis_zip_sha256=ba63dffc4410ee89193e1cb5a41989991bd77c61068da17e3156d136b7b0b3d8
# The two overrides are for tests.
nsis_zip_url=${NSIS_ZIP_URL:-https://sourceforge.net/projects/nsis/files/NSIS%203/$nsis_version/nsis-$nsis_version.zip/download}
nsis_zip_sha256=${NSIS_ZIP_SHA256:-$nsis_zip_sha256}

# The value of a Windows variable under whatever name bash has it: Git Bash
# upper-cases most, and leaves ProgramFiles(x86) alone, which bash can't name.
winenv() {
  env | awk -F= -v n="$1" 'tolower($1) == tolower(n) { sub(/^[^=]*=/, ""); print; exit }' || true
}

# A directory as bash can look inside it: C:\Program Files becomes /c/Program Files.
unix() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -u "$1"; else printf '%s\n' "$1"; fi
}

# A path as Windows' own programs (7z) take it.
native() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else printf '%s\n' "$1"; fi
}

# Where the portable zip is unpacked: the job's temporary directory (RUNNER_TEMP
# lasts the whole job, so later steps find it there).
portable_base() { unix "${RUNNER_TEMP:-${TMPDIR:-/tmp}}"; }

# Prints makensis's path if it is on PATH, where NSIS's own installer puts it
# (32-bit, so Program Files (x86) first), in Chocolatey's bin, or unpacked from
# the zip.
locate() {
  local found dir var
  local dirs=("/c/Program Files (x86)/NSIS" "/c/Program Files/NSIS" /c/ProgramData/chocolatey/bin)
  if found=$(command -v makensis); then printf '%s\n' "$found"; return 0; fi
  for var in 'ProgramFiles(x86)' ProgramFiles ProgramW6432; do
    dir=$(winenv "$var")
    [ -z "$dir" ] || dirs+=("$(unix "$dir")/NSIS")
  done
  dir=$(winenv ChocolateyInstall)
  [ -z "$dir" ] || dirs+=("$(unix "$dir")/bin")
  dirs+=("$(portable_base)/nsis-$nsis_version")
  # The variables first, then the usual places in case they aren't set.
  for dir in "${dirs[@]:3}" "${dirs[@]:0:3}"; do
    if [ -x "$dir/makensis.exe" ]; then printf '%s\n' "$dir/makensis.exe"; return 0; fi
  done
  return 1
}

install_choco() {
  local attempts=2 attempt
  if ! command -v choco >/dev/null 2>&1; then
    echo "no choco to install NSIS with" >&2
    return 1
  fi
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    echo "makensis not found: choco install nsis (attempt $attempt of $attempts)" >&2
    choco install nsis -y --no-progress >&2 || echo "choco exited $?" >&2
    if locate >/dev/null; then return 0; fi
    if ((attempt < attempts)); then
      echo "choco left no makensis (its feed is probably down); trying again in ${NSIS_RETRY_DELAY:-20}s" >&2
      sleep "${NSIS_RETRY_DELAY:-20}"
    fi
  done
  return 1
}

install_zip() {
  local base tmp sum
  base=$(portable_base)
  tmp=$(mktemp -d)
  echo "unpacking NSIS $nsis_version's portable zip from SourceForge" >&2
  if ! curl -fsSL --retry 5 --retry-all-errors --retry-delay 10 --max-time 300 -o "$tmp/nsis.zip" "$nsis_zip_url" >&2; then
    echo "downloading $nsis_zip_url failed" >&2
    rm -rf "$tmp"; return 1
  fi
  sum=$(sha256sum "$tmp/nsis.zip" | cut -d' ' -f1)
  if [ "$sum" != "$nsis_zip_sha256" ]; then
    echo "NSIS's zip has SHA-256 $sum, not the $nsis_zip_sha256 pinned in $0: not using it" >&2
    rm -rf "$tmp"; return 1
  fi
  rm -rf "${base:?}/nsis-$nsis_version"
  mkdir -p "$base"
  if command -v 7z >/dev/null 2>&1; then
    7z x -y -bso0 -o"$(native "$base")" "$(native "$tmp/nsis.zip")" >&2
  else
    unzip -q -o "$tmp/nsis.zip" -d "$base" >&2
  fi
  rm -rf "$tmp"
  locate >/dev/null
}

install() {
  install_choco || true
  if locate >/dev/null; then return 0; fi
  echo "choco gave no makensis; trying NSIS's zip instead" >&2
  install_zip || true
  if locate >/dev/null; then return 0; fi
  echo "no makensis: not on PATH, not installed by choco, and its zip wasn't unpacked either" >&2
  return 1
}

case ${1:-} in
  '' | --install) ;;
  *) echo "usage: $0 [--install]" >&2; exit 2 ;;
esac

if ! makensis=$(locate); then
  if [ "${1:-}" = --install ]; then
    install
    makensis=$(locate)
  else
    exit 1
  fi
fi

# Proof that it runs, for the log: the version is the only thing it prints.
"$makensis" -VERSION >&2

if command -v cygpath >/dev/null 2>&1; then cygpath -m "$makensis"; else printf '%s\n' "$makensis"; fi
