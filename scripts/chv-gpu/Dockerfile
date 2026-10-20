# syntax=docker/dockerfile:1
# Builds AgentBox's Cloud Hypervisor and its GPU device; run by build.sh, which passes every pin in
# versions.env as a build argument. The last stage is only the tarball.
# The default is versions.env's RUST_IMAGE (build.sh checks they match), so
# FROM has a valid image even before the build arguments arrive.
ARG RUST_IMAGE=rust:1.88.0-slim-bookworm@sha256:38bc5a86d998772d4aec2348656ed21438d20fcdce2795b56ca434cf21430d89
FROM ${RUST_IMAGE} AS build

ARG DEBIAN_SNAPSHOT
ARG SOURCE_DATE_EPOCH
ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} \
    DEBIAN_FRONTEND=noninteractive \
    CARGO_TERM_PROGRESS_WHEN=never

# Debian's packages as they were on one day, so a rebuild gets the same ones.
RUN rm -f /etc/apt/sources.list.d/* /etc/apt/sources.list && \
    printf '%s\n' \
      "deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT} bookworm main" \
      "deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT} bookworm-updates main" \
      "deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT} bookworm-security main" \
      > /etc/apt/sources.list && \
    apt-get -o Acquire::Retries=5 update && \
    apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
      bzip2 ca-certificates clang curl file git libcap-dev libclang-dev \
      libdrm-dev libegl-dev libepoxy-dev libgbm-dev libgles-dev libvulkan-dev \
      libwayland-dev libx11-dev libxext-dev make meson musl-tools ninja-build \
      patch pkg-config protobuf-compiler python3 python3-yaml \
      wayland-protocols && \
    rm -rf /var/lib/apt/lists/*

# crosvm pins Rust 1.88.0 (the image's); Cloud Hypervisor v53 needs 1.89.
ARG CHV_RUST=1.89.0
RUN rustup toolchain install "$CHV_RUST" --profile minimal --target x86_64-unknown-linux-musl
ARG CARGO_BUILD_JOBS=4
ENV CARGO_BUILD_JOBS=${CARGO_BUILD_JOBS}
WORKDIR /src
# The install prefix inside the build; the tarball is relocatable.
ENV PREFIX=/opt/chv-gpu
# Every git source goes through fetch-git.sh: retries, mirrors, commit check.
COPY fetch-git.sh /usr/local/bin/fetch-git

# --- virglrenderer, with Venus and its render server -------------------------
# minigbm_allocation (with the system's libgbm, despite the name): with
# crosvm's external-blob, which try.sh turns on, GL buffers the guest maps are
# allocated as GBM buffers and exported as dma-bufs. Over vhost-user crosvm
# can only hand Cloud Hypervisor a blob as an fd (SHMEM_MAP): Spectrum's
# patchset has no EXTERNAL_MAP, the fallback that passes a pointer into
# crosvm's own memory.
ARG VIRGLRENDERER_REPO
ARG VIRGLRENDERER_COMMIT
RUN fetch-git virglrenderer "$VIRGLRENDERER_COMMIT" "$VIRGLRENDERER_REPO" && \
    cd virglrenderer && \
    meson setup build --prefix="$PREFIX" --libdir=lib --buildtype=release \
      -Dvenus=true -Drender-server=true -Dplatforms=egl,glx -Dminigbm_allocation=true \
      -Dtests=false -Dc_args=-ffile-prefix-map=/src/= && \
    ninja -C build install

# --- crosvm, for `crosvm device gpu` -----------------------------------------
ARG CROSVM_REPO
ARG CROSVM_MIRROR
ARG CROSVM_COMMIT
ARG MINIJAIL_REPO
ARG MINIJAIL_MIRROR
ARG MINIJAIL_COMMIT
RUN fetch-git crosvm "$CROSVM_COMMIT" "$CROSVM_REPO" "$CROSVM_MIRROR" && \
    cd crosvm && \
    test "$(git ls-tree HEAD third_party/minijail | awk '{print $3}')" = "$MINIJAIL_COMMIT" && \
    rmdir third_party/minijail && \
    fetch-git third_party/minijail "$MINIJAIL_COMMIT" "$MINIJAIL_REPO" "$MINIJAIL_MIRROR"
# Our patches to crosvm and to rutabaga_gfx (patches/README.md).
COPY patches/crosvm/ /src/patches/crosvm/
COPY patches/rutabaga_gfx/ /src/patches/rutabaga_gfx/
COPY test/render-node-path.sh /src/test-render-node-path.sh
RUN for p in /src/patches/crosvm/*.patch; do \
      patch -d crosvm -p1 --no-backup-if-mismatch < "$p" || exit 1; \
    done
# rutabaga_gfx, the crates.io crate crosvm renders with, patched: fetched at
# the version crosvm's Cargo.lock pins and checks, then used from a path. From
# here cargo builds --offline, so nothing else can change: the lock loses only
# rutabaga's registry source.
RUN cd crosvm && cargo fetch --locked && \
    src=$(ls -d /usr/local/cargo/registry/src/*/rutabaga_gfx-0.1.75) && \
    cp -r "$src" /src/rutabaga_gfx && \
    for p in /src/patches/rutabaga_gfx/*.patch; do \
      patch -d /src/rutabaga_gfx -p1 --no-backup-if-mismatch < "$p" || exit 1; \
    done && \
    sed -i 's|^\[patch.crates-io\]$|&\nrutabaga_gfx = { path = "../rutabaga_gfx" }|' Cargo.toml && \
    grep -q '^rutabaga_gfx = { path = "../rutabaga_gfx" }' Cargo.toml
RUN cd crosvm && \
    export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig" CROSVM_USE_SYSTEM_VIRGLRENDERER=1 && \
    export RUSTFLAGS='-C link-arg=-Wl,-rpath,$ORIGIN/../lib --remap-path-prefix=/src=. --remap-path-prefix=/usr/local/cargo=cargo' && \
    /src/test-render-node-path.sh /src/rutabaga_gfx/src/virgl_renderer.rs && \
    cargo build --release --offline --features virgl_renderer && \
    install -D -m 755 target/release/crosvm "$PREFIX/bin/crosvm"

# --- Cloud Hypervisor with the GPU patchset (patches/README.md) -------------
COPY patches/vhost/ /src/patches/vhost/
COPY patches/cloud-hypervisor/ /src/patches/cloud-hypervisor/
ARG CHV_URL
ARG CHV_SHA256
ARG VHOST_URL
ARG VHOST_SHA256
RUN fetch() { curl -fsSL --retry 5 -o "$1" "$2" && echo "$3  $1" | sha256sum -c --quiet; } && \
    fetch chv.tar.gz "$CHV_URL" "$CHV_SHA256" && \
    fetch vhost.tar.gz "$VHOST_URL" "$VHOST_SHA256" && \
    mkdir cloud-hypervisor vhost && \
    tar xzf chv.tar.gz --strip-components=1 -C cloud-hypervisor && \
    tar xzf vhost.tar.gz --strip-components=1 -C vhost && \
    for p in /src/patches/vhost/*.patch; do \
      patch -d vhost -p1 --no-backup-if-mismatch < "$p" || exit 1; \
    done && \
    for p in /src/patches/cloud-hypervisor/*.patch; do \
      patch -d cloud-hypervisor -p1 --no-backup-if-mismatch < "$p" || exit 1; \
    done
# Static (musl), as Cloud Hypervisor's own release builds its
# cloud-hypervisor-static (v53's .github/workflows/release.yaml: --all
# --release --features mshv), so that it differs from upstream's binary only by
# the patches.
ARG CHV_BUILD
RUN cd cloud-hypervisor && \
    export RUSTFLAGS='--remap-path-prefix=/src=. --remap-path-prefix=/usr/local/cargo=cargo' && \
    export CH_EXTRA_VERSION="agentbox-$CHV_BUILD" && \
    cargo "+$CHV_RUST" build --all --release --features mshv --locked \
      --target x86_64-unknown-linux-musl && \
    install -m 755 target/x86_64-unknown-linux-musl/release/cloud-hypervisor \
      target/x86_64-unknown-linux-musl/release/ch-remote "$PREFIX/bin/"

# --- the tarball --------------------------------------------------------------
COPY patches/LICENSES/ /src/patches/LICENSES/
COPY patches/README.md /src/patches/README.md
COPY package.sh /package.sh
ARG CHV_VERSION
ARG CHV_BUILD
ARG VIRGLRENDERER_VERSION
RUN /package.sh /out

FROM scratch
COPY --from=build /out/ /
