#!/usr/bin/env bash
# Finds makensis, the compiler of NSIS (the native app's Windows installer,
# native/packaging/windows/installer.nsi), and prints where it is:
#
#   scripts/find-makensis.sh             the path, or exit 1 and print nothing
#   scripts/find-makensis.sh --install   the same, installing NSIS first when it
#                                        isn't there, and failing if it still isn't
#
# GitHub's Windows runners don't have NSIS (windows-2025-vs2026 20261116 doesn't),
# so the jobs that make the installer install it with Chocolatey. That fails now
# and then: community.chocolatey.org answers 503, and `choco install` prints
# "Unable to find package", "installed 0/0 packages" and exits 0 (nightly run
# 45326834657). Its exit status says nothing, so --install retries and then asks
# where makensis is, which is the only proof. A workflow step runs it, so a
# missing NSIS fails there and not minutes later in native-package.sh:
#
#   makensis=$(scripts/find-makensis.sh --install)
#   echo "MAKENSIS=$makensis" >>"$GITHUB_ENV"
#
# The path is printed the way Windows spells it (C:/Program Files (x86)/NSIS/...)
# when cygpath is there, which bash and makensis's callers both take; everything
# else goes to stderr.
set -euo pipefail

# The value of a Windows variable under whatever name bash has it: Git Bash
# upper-cases most, and leaves ProgramFiles(x86) alone, which bash can't name.
winenv() {
  env | awk -F= -v n="$1" 'tolower($1) == tolower(n) { sub(/^[^=]*=/, ""); print; exit }' || true
}

# A directory as bash can look inside it: C:\Program Files becomes /c/Program Files.
unix() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -u "$1"; else printf '%s\n' "$1"; fi
}

# Prints makensis's path if it is on PATH, where NSIS's own installer puts it
# (32-bit, so Program Files (x86) first), or in Chocolatey's bin.
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
  # The variables first, then the usual places in case they aren't set.
  for dir in "${dirs[@]:3}" "${dirs[@]:0:3}"; do
    if [ -x "$dir/makensis.exe" ]; then printf '%s\n' "$dir/makensis.exe"; return 0; fi
  done
  return 1
}

install() {
  local attempts=4 attempt
  if ! command -v choco >/dev/null 2>&1; then
    echo "no makensis, and no choco to install NSIS with: install NSIS, or put makensis on PATH" >&2
    return 1
  fi
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    echo "makensis not found: choco install nsis (attempt $attempt of $attempts)" >&2
    choco install nsis -y --no-progress >&2 || echo "choco exited $?" >&2
    if locate >/dev/null; then return 0; fi
    if ((attempt < attempts)); then
      echo "choco left no makensis (its feed is probably down); trying again in $((attempt * ${NSIS_RETRY_DELAY:-20}))s" >&2
      sleep $((attempt * ${NSIS_RETRY_DELAY:-20}))
    fi
  done
  echo "no makensis after $attempts attempts at choco install nsis" >&2
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
