#!/usr/bin/env bash
# Builds the npm packages: one per platform holding the binary, and the main
# agentbox-hub package that launches it. Usage: scripts/npm-build.sh <version> [outdir]
set -euo pipefail

version="${1:?usage: scripts/npm-build.sh <version> [outdir]}"
root="$(cd "$(dirname "$0")/.." && pwd)"
out="${2:-$root/npm/dist}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"

# npm os-cpu : GOOS/GOARCH
platforms=(
  "linux-x64:linux/amd64"
  "linux-arm64:linux/arm64"
  "darwin-x64:darwin/amd64"
  "darwin-arm64:darwin/arm64"
  "win32-x64:windows/amd64"
  "win32-arm64:windows/arm64"
)

for p in "${platforms[@]}"; do
  key="${p%%:*}"; goos="${p#*:}"; goarch="${goos#*/}"; goos="${goos%/*}"
  os="${key%%-*}"; cpu="${key#*-}"
  exe="agentbox-hub"; [ "$os" = win32 ] && exe="agentbox-hub.exe"
  dir="$out/$key"
  rm -rf "$dir"
  mkdir -p "$dir/bin"
  echo "building @agentbox-hub/$key"
  (cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$dir/bin/$exe" ./cmd/agentbox-hub)
  chmod +x "$dir/bin/$exe"
  echo "The $os $cpu binary of agentbox-hub, the AgentBox hub. Install agentbox-hub instead." > "$dir/README.md"
  node -e '
    const [file, name, version, os, cpu] = process.argv.slice(1);
    require("fs").writeFileSync(file, JSON.stringify({
      name, version,
      description: `The ${os} ${cpu} binary of agentbox-hub`,
      license: "MIT",
      os: [os], cpu: [cpu],
      files: ["bin"],
    }, null, 2) + "\n");
  ' "$dir/package.json" "@agentbox-hub/$key" "$version" "$os" "$cpu"
done

main="$out/agentbox-hub"
rm -rf "$main"
mkdir -p "$main"
cp -R "$root/npm/agentbox-hub/." "$main/"
chmod +x "$main/bin/agentbox-hub.js"
node -e '
  const fs = require("fs");
  const [file, version] = process.argv.slice(1);
  const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
  pkg.version = version;
  for (const k of Object.keys(pkg.optionalDependencies)) pkg.optionalDependencies[k] = version;
  fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
' "$main/package.json" "$version"

echo "built agentbox-hub $version in $out"
