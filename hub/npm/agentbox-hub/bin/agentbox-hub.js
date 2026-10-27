#!/usr/bin/env node
"use strict";

const { spawn } = require("child_process");

const SUPPORTED = [
  "linux-x64",
  "linux-arm64",
  "darwin-x64",
  "darwin-arm64",
  "win32-x64",
  "win32-arm64",
];

function binaryPath() {
  if (process.env.AGENTBOX_HUB_BINARY) return process.env.AGENTBOX_HUB_BINARY;
  const key = `${process.platform}-${process.arch}`;
  const exe = process.platform === "win32" ? "agentbox-hub.exe" : "agentbox-hub";
  try {
    return require.resolve(`@agentbox-hub/${key}/bin/${exe}`);
  } catch (e) {
    console.error(
      `agentbox-hub: no binary for this platform (${key}).\n\n` +
        `The package @agentbox-hub/${key} is not installed. If your platform is supported, ` +
        `reinstall without --no-optional / --omit=optional (for example "npm i agentbox-hub"), ` +
        `since the binary comes in an optional dependency.\n\n` +
        `Supported platforms: ${SUPPORTED.join(", ")}.\n` +
        `To use a binary you built yourself, set AGENTBOX_HUB_BINARY to its path.`
    );
    process.exit(1);
  }
}

const child = spawn(binaryPath(), process.argv.slice(2), { stdio: "inherit" });

for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(sig, () => {
    try {
      child.kill(sig);
    } catch (e) {
      // already gone
    }
  });
}

child.on("error", (err) => {
  console.error(`agentbox-hub: could not run ${child.spawnfile}: ${err.message}`);
  process.exit(1);
});

child.on("exit", (code, signal) => {
  if (signal) {
    // Remove our handlers so the re-raised signal takes the default action.
    for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) process.removeAllListeners(sig);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
