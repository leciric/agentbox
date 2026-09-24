#!/usr/bin/env bash
# Fills in the AppImage, CLI and LICENSE sha256sums in PKGBUILD from a
# published release's checksums, and regenerates .SRCINFO. Run after every
# release this package tracks, before submitting it to the AUR.
#
#   packaging/aur-bin/update-checksums.sh [version]
#
# version defaults to PKGBUILD's own pkgver.
set -euo pipefail

cd "$(dirname "$0")"

version=${1:-$(awk -F= '/^pkgver=/{print $2}' PKGBUILD)}
base="https://github.com/leciric/agentbox/releases/download/v$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "==> Fetching $base/SHA256SUMS"
curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"

appimage="AgentBox-$version-x86_64.AppImage"
cli="agentbox-$version-linux-amd64"

appimage_sum=$(awk -v f="$appimage" '$2==f{print $1}' "$tmp/SHA256SUMS")
cli_sum=$(awk -v f="$cli" '$2==f{print $1}' "$tmp/SHA256SUMS")
[ -n "$appimage_sum" ] || { echo "$appimage not found in $base/SHA256SUMS" >&2; exit 1; }
[ -n "$cli_sum" ] || { echo "$cli not found in $base/SHA256SUMS" >&2; exit 1; }

echo "==> Fetching LICENSE at v$version"
license_sum=$(curl -fsSL "https://raw.githubusercontent.com/leciric/agentbox/v$version/LICENSE" | sha256sum | cut -d' ' -f1)

python3 - "$version" "$appimage_sum" "$cli_sum" "$license_sum" <<'EOF'
import re, sys
version, appimage_sum, cli_sum, license_sum = sys.argv[1:]
with open("PKGBUILD") as f:
    text = f.read()
text = re.sub(r'^pkgver=.*$', f'pkgver={version}', text, flags=re.M)
text = re.sub(r'^pkgrel=.*$', 'pkgrel=1', text, flags=re.M)
sums = re.search(r"sha256sums=\((.*?)\)", text, re.S).group(1)
entries = re.findall(r"'[^']*'", sums)
entries[0] = f"'{appimage_sum}'"
entries[1] = f"'{cli_sum}'"
entries[2] = f"'{license_sum}'"
new_sums = "sha256sums=(" + "\n            ".join(entries) + ")"
text = re.sub(r"sha256sums=\(.*?\)", new_sums, text, flags=re.S)
with open("PKGBUILD", "w") as f:
    f.write(text)
EOF

echo "==> Updated PKGBUILD for v$version"
if command -v makepkg >/dev/null; then
  makepkg --printsrcinfo >.SRCINFO
  echo "==> Regenerated .SRCINFO"
else
  echo "makepkg not found: regenerate .SRCINFO by hand before submitting" >&2
fi
