#!/usr/bin/env bash
# Builds the FFmpeg the native app (native/) plays agents' recordings with, as
# static libraries and nothing more than that playback needs: the MP4
# demuxer, the H.264 decoder and parser, the file protocol and swscale (the
# scaling to BGRA, native/crates/app/src/video.rs). LGPL, no external
# libraries, so the app asks the distribution for no libavcodec: it starts on
# Arch and Debian 13, whose FFmpeg isn't the 6.1 Ubuntu 24.04 has.
#
#   scripts/build-ffmpeg.sh <prefix>
#   FFMPEG_DIR=<prefix> cargo build --release -p agentbox-native --features ffmpeg-next/static
#
# FFmpeg 8.1 (n8.1), the release ffmpeg-next 8.1 binds, from the project's
# repository at a pinned commit. Needs git, make, a C compiler and nasm.
set -euo pipefail

prefix=${1:?usage: $0 <prefix>}
tag=n8.1
commit=9047fa1b084f76b1b4d065af2d743df1b40dfb56

mkdir -p "$prefix"
prefix=$(cd "$prefix" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git clone -q --depth 1 -b "$tag" https://github.com/FFmpeg/FFmpeg "$tmp/src"
[ "$(git -C "$tmp/src" rev-parse HEAD)" = "$commit" ] || { echo "FFmpeg $tag is not $commit" >&2; exit 1; }

cd "$tmp/src"
./configure --prefix="$prefix" \
  --disable-everything --disable-autodetect --disable-programs --disable-doc \
  --disable-avdevice --disable-avfilter --disable-swresample --disable-network \
  --disable-debug --enable-static --disable-shared --enable-pic \
  --enable-avcodec --enable-avformat --enable-avutil --enable-swscale \
  --enable-demuxer=mov --enable-protocol=file \
  --enable-decoder=h264 --enable-parser=h264 \
  >"$tmp/configure.log" || { tail -30 "$tmp/configure.log" >&2; exit 1; }
make -j"$(nproc)" >"$tmp/make.log" 2>&1 || { tail -30 "$tmp/make.log" >&2; exit 1; }
make install >/dev/null
ls -l "$prefix/lib"/*.a
