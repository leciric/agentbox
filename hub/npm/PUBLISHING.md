# Publishing agentbox-hub to npm

`agentbox-hub` is a small Node launcher; the Go binary lives in one package per platform
(`@agentbox-hub/linux-x64`, `linux-arm64`, `darwin-x64`, `darwin-arm64`, `win32-x64`, `win32-arm64`),
which the main package lists as `optionalDependencies`, the way esbuild does.

## Once

An npm account that owns the `agentbox-hub` package name and the `@agentbox-hub` scope
(create it with `npm org create agentbox-hub`, or on npmjs.com). Check with `npm whoami`.
For CI, an automation token in the repository secret `NPM_TOKEN`.

## By hand

From the repository's root:

```sh
hub/scripts/npm-build.sh 1.2.3          # builds into hub/npm/dist
for d in hub/npm/dist/*-*; do (cd "$d" && npm publish --access public); done
(cd hub/npm/dist/agentbox-hub && npm publish --access public)
```

Platform packages first and the main package last, so `npx` never resolves a main package
whose platform packages aren't there yet.

## From CI

`.github/workflows/hub-npm.yml` does the same on a pushed tag `hub-v*` (the version is the tag
without `hub-v`; the app's own `v*` tags don't publish the hub), or by hand from the Actions tab
with a version.

## Testing without publishing

```sh
hub/scripts/npm-build.sh 0.0.0-test /t/npmtest
mkdir -p /t/npmtest/packs /t/npmtest/try && cd /t/npmtest/packs
npm pack ../linux-x64 ../agentbox-hub      # use your own platform's directory
cd ../try && npm init -y >/dev/null
npm i ../packs/agentbox-hub-linux-x64-0.0.0-test.tgz ../packs/agentbox-hub-0.0.0-test.tgz
npx agentbox-hub --version
```

Installing both tarballs in one command lets npm use the local platform package instead of
fetching it from the registry. Or `npm i -g` the tarballs. Set `AGENTBOX_HUB_BINARY` to try the
launcher against any binary.
