#!/bin/sh
# Runs inside the build (Dockerfile): the unit tests of our rutabaga_gfx patch's
# is_render_node_path, compiled from the patched virgl_renderer.rs itself (the
# function and its test module, which need nothing but std), with plain
# rustc: the crate's own `cargo test` can't resolve offline outside crosvm.
#
#   render-node-path.sh PATCHED_VIRGL_RENDERER_RS
set -eu
src=$1
t=$(mktemp -d)
{
	echo 'use std::path::Path;'
	awk '/^fn is_render_node_path/,/^}/' "$src"
	awk '/^mod render_node_path_tests/,/^}/' "$src"
} > "$t/t.rs"
grep -q 'fn is_render_node_path' "$t/t.rs" && grep -q 'fn not_render_nodes' "$t/t.rs"
rustc --edition 2021 --test "$t/t.rs" -o "$t/t"
"$t/t"
rm -rf "$t"
