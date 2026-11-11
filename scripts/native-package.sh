#!/usr/bin/env bash
# Packages a release build of the native app (native/, agentbox-native) for a
# nightly, as an experimental download beside the Electron app:
#
#   scripts/native-package.sh linux <version> <out>     a .tar.gz and a .deb
#   scripts/native-package.sh windows <version> <out>   a .zip of the .exe
#   scripts/native-package.sh mac <version> <out>       a .app in a .dmg, arm64
#
# <version> is the nightly's (0.17.0-nightly.20261106.157). It expects
# native/target/release/agentbox-native(.exe) to be built already.
#
# On a Mac, the .app is signed with scripts/mac-sign.sh: with
# $MAC_SIGN_IDENTITY (a Developer ID Application identity in a keychain) the
# way a release is, hardened runtime and timestamp, and the .dmg too;
# without it ad hoc, which Gatekeeper refuses on a download until its
# quarantine is removed (xattr -dr com.apple.quarantine). Notarizing the .dmg
# is the workflow's (nightly.yml).
#
# Every asset is named AgentBox-Native-experimental-<version>-…, which nothing
# that looks for the Electron app's files matches: the app's own update
# (latest-linux.yml, AgentBox-<version>-<arch>-setup.exe/-portable.exe by
# name), install.sh (AgentBox-<version>-amd64.deb, …), and r2-page.py, which
# lists these under "Native app (experimental)" and never offers them on its
# download button.
#
# Beside them, three more files with the exact names the app's own update asks
# for (native/crates/app/src/update.rs, asset_name; <arch> is Node's, x64 or
# arm64): agentbox-native-<version>-linux-x64 (the bare binary) and .AppImage
# (x86_64, when $APPIMAGETOOL is set: the Linux install that updates itself),
# -windows-x64.exe
# and -mac-arm64.app.tar.gz (the signed .app). nightly.yml's SHA256SUMS lists
# them (its agentbox-* glob). A copy installed from the .deb is under /usr and
# is never overwritten: its update opens the release page instead. They are
# lowercase agentbox-native-*, so the Electron app's update, install.sh and
# r2-page.py (which hides them) don't see them. A test pins the names:
# update.rs, release_files_match_the_packaging.
#
# The .pacman (AgentBox-Native-experimental-<version>-x64.pacman) is the same
# package for Arch, installed with `sudo pacman -U`.
#
# The .deb is the package agentbox-native, beside (not instead of) the
# Electron app's agentbox, with /usr/bin/agentbox-native and a menu entry.
# Its Depends come from dpkg-shlibdeps on the binary, so it installs where
# WebKitGTK 4.1 is, on glibc 2.39 or newer (the Ubuntu 24.04 it is built on).
# FFmpeg is linked statically (nightly.yml), so Arch and Debian 13 run it too.
set -euo pipefail

# stamped checks that the binary knows its version (AGENTBOX_VERSION at build
# time): one that doesn't takes the daemon's for its own and keeps offering
# the update it has just installed.
stamped() {
  local said
  said=$("$1" --version) || { echo "$1 --version failed" >&2; exit 1; }
  [ "$said" = "agentbox-native $version" ] || {
    echo "$1 says \"$said\", not agentbox-native $version: build it with AGENTBOX_VERSION=$version" >&2
    exit 1
  }
}

os=${1:?usage: $0 linux|windows|mac <version> <out>}
version=${2:?usage: $0 linux|windows|mac <version> <out>}
out=${3:?usage: $0 linux|windows|mac <version> <out>}
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
It may break.

  agentbox-native                   against this computer's AgentBox
  agentbox-native --socket PATH     against another daemon's socket

It can't yet: show an agent's iOS simulator; do what only the Electron app's
main process does (resize the VM, restart it, host setup with sudo, install
the command-line tool), for which it shows the command to run instead; or
hide to the tray (it minimises). On Wayland, HTML previews are a still
picture. On Windows and macOS, agents' recordings don't play (no FFmpeg in these
builds).

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
    stamped "$bin"
    arch=$(dpkg --print-architecture) # amd64
    case $arch in amd64) uname=x86_64 ;; arm64) uname=aarch64 ;; *) uname=$arch ;; esac

    # The tarball: the binary, its icon and menu entry, and what it needs.
    dir=$tmp/$name-linux-$uname
    mkdir -p "$dir"
    install -m755 "$bin" "$dir/agentbox-native"
    install -m644 "$root/desktop/build/icon.png" "$dir/agentbox-native.png"
    readme >"$dir/README.txt"
    cat >>"$dir/README.txt" <<'EOF'

It needs WebKitGTK 4.1, GTK 3, ALSA and the Vulkan loader, and glibc 2.39 or
newer; FFmpeg is built in. On Debian and Ubuntu the .deb beside this pulls
them in.
EOF
    cp "$root/LICENSE" "$dir/LICENSE"
    tar -C "$tmp" -czf "$out/$name-linux-$uname.tar.gz" "$(basename "$dir")"
    # What the app's own update downloads: the bare binary.
    case $arch in amd64) node=x64 ;; *) node=$arch ;; esac
    install -m755 "$bin" "$out/agentbox-native-$version-linux-$node"


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
 AgentBox daemon. It may break.
EOF
    dpkg-deb --root-owner-group -Zxz --build "$pkg" "$out/$name-$arch.deb" >/dev/null

    # The .pacman, for `sudo pacman -U` on Arch: the package agentbox-native,
    # as the .deb is, with the same files. pkgver can't hold a hyphen, so the
    # nightly's is 0.17.0_nightly.20261106.157. No .MTREE: pacman -U needs none.
    if [ "$arch" = amd64 ]; then
      pm=$tmp/pacman
      mkdir -p "$pm"
      cp -a "$pkg/usr" "$pm/usr"
      rm -rf "$pm/usr/share/doc"
      mkdir -p "$pm/usr/share/licenses/agentbox-native"
      cp "$root/LICENSE" "$pm/usr/share/licenses/agentbox-native/LICENSE"
      cat >"$pm/.PKGINFO" <<EOF
pkgname = agentbox-native
pkgbase = agentbox-native
pkgver = ${version/-/_}-1
pkgdesc = AgentBox's native desktop app (experimental), a client of the same AgentBox daemon
url = https://downloads.agentbox.linting.dev
builddate = $(date +%s)
packager = AgentBox <https://github.com/leciric/agentbox>
size = $(du -sb "$pm/usr" | cut -f1)
arch = x86_64
license = MIT
depend = gtk3
depend = webkit2gtk-4.1
depend = alsa-lib
depend = vulkan-icd-loader
optdepend = vulkan-swrast: draws without a GPU
optdepend = libayatana-appindicator: the tray icon
EOF
      (cd "$pm" && tar --owner=0 --group=0 --numeric-owner -I 'zstd -19' -cf "$out/$name-x64.pacman" .PKGINFO usr)
    fi

    # The AppImage, the Linux install that updates itself: the same binary,
    # which links FFmpeg statically (nightly.yml builds it with
    # ffmpeg-next/build), so it needs only GTK and WebKitGTK 4.1 from the
    # system (webkit2gtk-4.1 on Arch, libwebkit2gtk-4.1-0 on Debian), not
    # Ubuntu's libavcodec. WebKitGTK's helper processes can't be bundled.
    # $APPIMAGETOOL is appimagetool (nightly.yml downloads and checks 1.9.1);
    # unset, none is made. --appimage-extract-and-run: no FUSE needed to build.
    if [ "$arch" = amd64 ] && [ -n "${APPIMAGETOOL:-}" ]; then
      app=$tmp/AppDir
      mkdir -p "$app/usr/bin"
      install -m755 "$bin" "$app/usr/bin/agentbox-native"
      install -m644 "$root/desktop/build/icon.png" "$app/agentbox-native.png"
      cp "$app/agentbox-native.png" "$app/.DirIcon"
      cat >"$app/agentbox-native.desktop" <<'DESK'
[Desktop Entry]
Type=Application
Name=AgentBox Native (experimental)
Comment=AgentBox's native desktop app, a preview
Exec=agentbox-native
Icon=agentbox-native
Categories=Development;
StartupWMClass=agentbox-native
DESK
      printf '#!/bin/sh\nexec "$(dirname "$(readlink -f "$0")")/usr/bin/agentbox-native" "$@"\n' >"$app/AppRun"
      chmod 755 "$app/AppRun"
      ARCH=x86_64 "$APPIMAGETOOL" --appimage-extract-and-run --no-appstream "$app" \
        "$out/agentbox-native-$version-linux-$node.AppImage"
    fi
    ;;
  windows)
    bin=$root/native/target/release/agentbox-native.exe
    [ -f "$bin" ] || { echo "no $bin: build it first" >&2; exit 1; }
    dir=$tmp/$name-windows-x64
    mkdir -p "$dir"
    cp "$bin" "$dir/agentbox-native.exe"
    # What the app's own update downloads: the .exe itself.
    cp "$bin" "$out/agentbox-native-$version-windows-x64.exe"
    readme | sed 's/$/\r/' >"$dir/README.txt"
    sed 's/$/\r/' "$root/LICENSE" >"$dir/LICENSE.txt"
    # 7z is on GitHub's Windows runners; zip isn't.
    (cd "$tmp" && 7z a -tzip -bso0 "$out/$name-windows-x64.zip" "$(basename "$dir")")
    ;;
  mac)
    bin=$root/native/target/release/agentbox-native
    [ -x "$bin" ] || { echo "no $bin: build it first" >&2; exit 1; }
    stamped "$bin"
    arch=$(lipo -archs "$bin" | tr ' ' '-') # arm64
    app="$tmp/dmg/AgentBox Native (experimental).app"
    mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
    install -m755 "$bin" "$app/Contents/MacOS/agentbox-native"
    # The icon: Electron's, at every size an .icns holds.
    set=$tmp/icon.iconset
    mkdir -p "$set"
    for px in 16 32 128 256 512; do
      sips -z $px $px "$root/desktop/build/icon.png" --out "$set/icon_${px}x${px}.png" >/dev/null
      sips -z $((px * 2)) $((px * 2)) "$root/desktop/build/icon.png" --out "$set/icon_${px}x${px}@2x.png" >/dev/null
    done
    iconutil -c icns "$set" -o "$app/Contents/Resources/agentbox-native.icns"
    # CFBundleVersion takes numbers and dots only: the release, then the
    # nightly's date and run (0.17.0.20261106.157).
    cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>AgentBox Native</string>
  <key>CFBundleDisplayName</key><string>AgentBox Native (experimental)</string>
  <key>CFBundleIdentifier</key><string>dev.agentbox.native</string>
  <key>CFBundleExecutable</key><string>agentbox-native</string>
  <key>CFBundleIconFile</key><string>agentbox-native</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version%%-*}</string>
  <key>CFBundleVersion</key><string>${version/-nightly/}</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
    readme >"$tmp/dmg/README.txt"
    ln -s /Applications "$tmp/dmg/Applications"
    "$root/scripts/mac-sign.sh" ${MAC_SIGN_IDENTITY:+--identity "$MAC_SIGN_IDENTITY"} "$app"
    # What the app's own update downloads: the signed .app, alone at the top.
    tar -C "$tmp/dmg" -czf "$out/agentbox-native-$version-mac-$arch.app.tar.gz" "$(basename "$app")"
    dmg=$out/$name-mac-$arch.dmg
    hdiutil create -quiet -volname "AgentBox Native" -srcfolder "$tmp/dmg" -fs HFS+ -format UDZO -ov "$dmg"
    if [ -n "${MAC_SIGN_IDENTITY:-}" ]; then
      codesign --force --sign "$MAC_SIGN_IDENTITY" --timestamp "$dmg"
    fi
    ;;
  *)
    echo "usage: $0 linux|windows|mac <version> <out>" >&2
    exit 1
    ;;
esac
ls -l "$out"
