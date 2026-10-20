#!/usr/bin/env bash
# Pins a published Cloud Hypervisor build in internal/hostvm/chv/tools.go:
# rewrites chvBundle's version, URL and SHA256s from the tarball, as
# publish.sh left it.
#
#   scripts/chv-gpu/pin.sh TARBALL
#
# It prints whether anything changed. The Cloud Hypervisor workflow runs it
# on main after publish.sh, and opens a pull request with the change.
set -euo pipefail

tarball=${1:?usage: $0 TARBALL}
here=$(cd "$(dirname "$0")" && pwd)
tools=$here/../../internal/hostvm/chv/tools.go
name=$(basename "$tarball" -x86_64.tar.gz)          # chv-53.0-<hash>
version=${name#chv-}                                # 53.0-<hash>
tag=$name
sums=$(tar -xzOf "$tarball" --wildcards '*/SHA256SUMS')
sum() { awk -v f="./$1" '$2 == f { print $1 }' <<<"$sums"; }
virgl=$(awk '$2 ~ /^\.\/lib\/libvirglrenderer\.so\.1\.[0-9.]+$/ { print substr($2, 3) }' <<<"$sums")
[ -n "$virgl" ] || { echo "no libvirglrenderer in $tarball" >&2; exit 1; }

python3 - "$tools" <<PY
import re, sys
path = sys.argv[1]
s = open(path).read()
start = s.index("var chvBundle = bundle{")
end = s.index("\n}\n", start) + 3
block = s[start:end]
def setfield(block, key, value):
    new, n = re.subn(r'(?m)^(\t%s: *)"[^"]*",$' % key, lambda m: m.group(1) + '"' + value + '",', block, count=1)
    assert n == 1, key
    return new
block = setfield(block, "version", "v${version%%-*} (AgentBox build ${version#*-})")
block = setfield(block, "url", "https://github.com/leciric/agentbox/releases/download/$tag/$(basename "$tarball")")
block = setfield(block, "sha256", "$(sha256sum "$tarball" | cut -d' ' -f1)")
files = {
    "bin/cloud-hypervisor": "$(sum bin/cloud-hypervisor)",
    "bin/crosvm": "$(sum bin/crosvm)",
    "libexec/virgl_render_server": "$(sum libexec/virgl_render_server)",
    "lib/libvirglrenderer.so.1": "$(sum "$virgl")",
}
for member, value in files.items():
    assert re.fullmatch(r"[0-9a-f]{64}", value), member
    if member == "lib/libvirglrenderer.so.1":
        block, n = re.subn(r'member: "lib/libvirglrenderer\.so\.1[0-9.]*"', 'member: "$virgl"', block)
        assert n == 1
        member = "$virgl"
    block, n = re.subn(r'(member: "%s",[^\n]*?sha256: )"[0-9a-f]*"' % re.escape(member), lambda m: m.group(1) + '"' + value + '"', block)
    assert n == 1, member
new = s[:start] + block + s[end:]
if new != s:
    open(path, "w").write(new)
    print("pinned $tag")
else:
    print("already pinned: $tag")
PY
