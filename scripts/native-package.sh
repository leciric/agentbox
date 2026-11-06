#!/usr/bin/env bash
# Packages a release build of the native app (native/, agentbox-native) for a
# nightly, as an experimental download beside the Electron app:
#
#   scripts/native-package.sh linux <version> <out>     a .tar.gz and a .deb
#   scripts/native-package.sh windows <version> <out>   a .zip of the .exe
#
# <version> is the nightly's (0.17.0-nightly.20261106.157). It expects
# native/target/release/agentbox-native(.exe) to be built already.
#
# Every asset is named AgentBox-Native-experimental-<version>-…, which nothing
# that looks for the Electron app's files matches: the app's own update
# (latest-linux.yml, AgentBox-<version>-<arch>-setup.exe/-portable.exe by
# name), install.sh (AgentBox-<version>-amd64.deb, …), and r2-page.py, which
# lists these under "Native app (experimental)" and never offers them on its
# download button.
#
# The .deb is the package agentbox-native, beside (not instead of) the
# Electron app's agentbox, with /usr/bin/agentbox-native and a menu entry.
# Its Depends come from dpkg-shlibdeps on the binary, so it installs on the
# Ubuntu it was built on (24.04) and Debian of the same age or newer.
set -euo pipefail

os=${1:?usage: $0 linux|windows <version> <out>}
version=${2:?usage: $0 linux|windows <version> <out>}
out=${3:?usage: $0 linux|windows <version> <out>}
root=$(cd "$(dirname "$0")/.." && pwd)
name=AgentBox-Native-experimental-$version
mkdir -p "$out"
out=$(cd "$out" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

readme() {
  cat <<EOF
AgentBox native app $version (experimental)

A preview of AgentBox's desktop app rewritten in Rust with GPUI, the toolkit
Zed is drawn with. It is a client of the same AgentBox daemon the Electron app
uses: install AgentBox as usual, then start this instead of the AgentBox app.
It may break, and it doesn't update itself.

  agentbox-native                   against this computer's AgentBox
  agentbox-native --socket PATH     against another daemon's socket

It can't yet: show an agent's iOS simulator; do what only the Electron app's
main process does (resize the VM, restart it, host setup with sudo, install
the command-line tool), for which it shows the command to run instead; or
hide to the tray (it minimises). On Wayland, HTML previews are a still
picture. On Windows, agents' recordings don't play (no FFmpeg in this build).

Without a GPU it draws with Mesa's software Vulkan (lavapipe), slowly:
  VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json agentbox-native

MIT licensed. Inter and JetBrains Mono are bundled under the SIL Open Font
License.
EOF
}

case $os in
  linux)
    bin=$root/native/target/release/agentbox-native
    [ -x "$bin" ] || { echo "no $bin: build it first" >&2; exit 1; }
    arch=$(dpkg --print-architecture) # amd64
    case $arch in amd64) uname=x86_64 ;; arm64) uname=aarch64 ;; *) uname=$arch ;; esac

    # The tarball: the binary, its icon and menu entry, and what it needs.
    dir=$tmp/$name-linux-$uname
    mkdir -p "$dir"
    install -m755 "$bin" "$dir/agentbox-native"
    install -m644 "$root/desktop/build/icon.png" "$dir/agentbox-native.png"
    readme >"$dir/README.txt"
    cat >>"$dir/README.txt" <<'EOF'

It needs WebKitGTK 4.1, FFmpeg's libraries (6.1, as Ubuntu 24.04 has them),
ALSA and the Vulkan loader: on Debian and Ubuntu the .deb beside this pulls
them in.
EOF
    cp "$root/LICENSE" "$dir/LICENSE"
    tar -C "$tmp" -czf "$out/$name-linux-$uname.tar.gz" "$(basename "$dir")"

    # The .deb. Debian orders ~ before anything, so 0.17.0~nightly.… comes
    # before 0.17.0, as the semver prerelease does.
    pkg=$tmp/deb
    mkdir -p "$pkg/DEBIAN" "$pkg/usr/bin" "$pkg/usr/share/applications" \
      "$pkg/usr/share/icons/hicolor/512x512/apps" "$pkg/usr/share/doc/agentbox-native"
    install -m755 "$bin" "$pkg/usr/bin/agentbox-native"
    install -m644 "$root/desktop/build/icon.png" "$pkg/usr/share/icons/hicolor/512x512/apps/agentbox-native.png"
    readme >"$pkg/usr/share/doc/agentbox-native/README"
    cp "$root/LICENSE" "$pkg/usr/share/doc/agentbox-native/copyright"
    cat >"$pkg/usr/share/applications/agentbox-native.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Name=AgentBox Native (experimental)
Comment=AgentBox's native desktop app, a preview
Exec=agentbox-native
Icon=agentbox-native
Categories=Development;
StartupWMClass=agentbox-native
EOF
    # dpkg-shlibdeps wants a source package around the binary.
    mkdir -p "$tmp/src/debian"
    printf 'Source: agentbox-native\n\nPackage: agentbox-native\nArchitecture: any\n' >"$tmp/src/debian/control"
    depends=$(cd "$tmp/src" && dpkg-shlibdeps -O "$pkg/usr/bin/agentbox-native" 2>/dev/null | sed -n 's/^shlibs:Depends=//p')
    [ -n "$depends" ] || { echo "dpkg-shlibdeps found no dependencies" >&2; exit 1; }
    cat >"$pkg/DEBIAN/control" <<EOF
Package: agentbox-native
Version: ${version/-/\~}
Architecture: $arch
Maintainer: AgentBox <https://github.com/leciric/agentbox>
Homepage: https://downloads.agentbox.linting.dev
Section: devel
Priority: optional
Depends: $depends
Recommends: mesa-vulkan-drivers, libayatana-appindicator3-1
Installed-Size: $(du -sk "$pkg/usr" | cut -f1)
Description: AgentBox's native desktop app (experimental)
 A preview of the AgentBox desktop app in Rust and GPUI, a client of the same
 AgentBox daemon. It may break and doesn't update itself.
EOF
    dpkg-deb --root-owner-group -Zxz --build "$pkg" "$out/$name-$arch.deb" >/dev/null
    ;;
  windows)
    bin=$root/native/target/release/agentbox-native.exe
    [ -f "$bin" ] || { echo "no $bin: build it first" >&2; exit 1; }
    dir=$tmp/$name-windows-x64
    mkdir -p "$dir"
    cp "$bin" "$dir/agentbox-native.exe"
    readme | sed 's/$/\r/' >"$dir/README.txt"
    sed 's/$/\r/' "$root/LICENSE" >"$dir/LICENSE.txt"
    # 7z is on GitHub's Windows runners; zip isn't.
    (cd "$tmp" && 7z a -tzip -bso0 "$out/$name-windows-x64.zip" "$(basename "$dir")")
    ;;
  *)
    echo "usage: $0 linux|windows <version> <out>" >&2
    exit 1
    ;;
esac
ls -l "$out"
