#!/bin/sh
# Runs inside the build (Dockerfile): turns $PREFIX into the tarball, in $1.
set -eu
out=$1
name="chv-${CHV_VERSION}-${CHV_BUILD}-x86_64"
stage="/stage/$name"

mkdir -p "$stage/bin" "$stage/lib" "$stage/libexec" "$stage/licenses" "$out"
cp "$PREFIX/bin/cloud-hypervisor" "$PREFIX/bin/ch-remote" "$PREFIX/bin/crosvm" "$stage/bin/"
cp "$PREFIX/libexec/virgl_render_server" "$stage/libexec/"
# The library and its soname link, not the unversioned development link.
cp -P "$PREFIX"/lib/libvirglrenderer.so.* "$stage/lib/"
strip --strip-unneeded "$stage"/bin/* "$stage"/libexec/* "$stage"/lib/libvirglrenderer.so.*.*
# virgl_render_server has Venus built in; check it needs no rpath of its own.
if readelf -d "$stage/libexec/virgl_render_server" | grep -q 'libvirglrenderer'; then
	echo "virgl_render_server links libvirglrenderer: give it an rpath" >&2
	exit 1
fi

for d in cloud-hypervisor vhost crosvm crosvm/third_party/minijail virglrenderer; do
	mkdir -p "$stage/licenses/$d"
	find "/src/$d" -maxdepth 1 \( -name 'LICENSE*' -o -name 'COPYING*' -o -name 'NOTICE*' \) \
		-exec cp -r {} "$stage/licenses/$d/" \;
done
# The patches as applied, with the licences of Spectrum's (LICENSES/).
cp -r /src/patches "$stage/licenses/patches"

{
	echo "cloud-hypervisor $CHV_VERSION, AgentBox build $CHV_BUILD (the hash of its inputs): Spectrum OS's GPU patchset (50.0-spectrum0) rebased onto it, $(ls /src/patches/cloud-hypervisor | paste -sd' ') (static, musl, --features mshv)"
	echo "vhost $VHOST_URL, patched: $(ls /src/patches/vhost | paste -sd' ')"
	echo "crosvm $CROSVM_COMMIT (minijail $MINIJAIL_COMMIT), features: default + virgl_renderer, patched: $(ls /src/patches/crosvm | paste -sd' '), rutabaga_gfx 0.1.75 patched: $(ls /src/patches/rutabaga_gfx | paste -sd' ')"
	echo "virglrenderer $VIRGLRENDERER_VERSION ($VIRGLRENDERER_COMMIT): venus, render-server, egl+glx, minigbm_allocation (system libgbm)"
	echo "built with $(rustc --version) (crosvm), $(rustc "+$CHV_RUST" --version) (Cloud Hypervisor), on Debian 12 (snapshot.debian.org $DEBIAN_SNAPSHOT)"
} > "$stage/VERSIONS"

# What the host must provide: every shared library the binaries load that
# the tarball doesn't carry.
for f in "$stage"/bin/* "$stage"/libexec/* "$stage"/lib/libvirglrenderer.so.*.*; do
	readelf -d "$f" | sed -n 's/.*(NEEDED).*\[\(.*\)\]/\1/p'
done | grep -v '^libvirglrenderer' | sort -u > "$stage/HOST-LIBRARIES"
(cd "$stage" && find . -type f ! -name SHA256SUMS | LC_ALL=C sort | xargs sha256sum > SHA256SUMS)

find "$stage" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
tar -C /stage --sort=name --owner=0 --group=0 --numeric-owner \
	--mtime="@$SOURCE_DATE_EPOCH" --pax-option=exthdr.name=%d/PaxHeaders/%f,delete=atime,delete=ctime \
	-cf - "$name" | gzip -9n > "$out/$name.tar.gz"
cp "$stage/VERSIONS" "$stage/HOST-LIBRARIES" "$out/"
(cd "$out" && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
