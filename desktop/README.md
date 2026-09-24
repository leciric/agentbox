# AgentBox desktop app

A client of the AgentBox daemon: projects and agents, with each agent's chat, terminal, browser, Android emulator, media and snapshots, and a Setup page for the machine.

## Run

```bash
npm install
npm start          # builds into out/ and opens the app
```

The app uses the same socket as the CLI: `~/.local/share/agentbox/run/agentbox.sock`, or `AGENTBOX_SOCKET`. When no daemon answers, the app starts one with `agentbox daemon start`, so put `agentbox` on your `PATH` or set `AGENTBOX_BIN`.

## Package

```bash
npm run dist       # dist/AgentBox-<version>-x86_64.AppImage
```

`dist` builds `../bin/agentbox` for the app's version, builds the app, and packages both with electron-builder. The packaged app:
- keeps its own copy of `agentbox` in `~/.local/share/agentbox/bin`, and starts the daemon from it;
- links `~/.local/bin/agentbox` to that copy from the Setup page (**Install command-line tool**);
- restarts a daemon of another version, or one that started without the `incus-admin` or `kvm` group, when no job is running (D27, D28).

On a Mac, `npm run dist -- --mac` packages `dist/AgentBox-<version>-mac-<arch>.dmg` and `.zip` instead
(`--arm64`, `--x64` or both; default this Mac's). The Mac app carries two binaries: `bin/agentbox`, the
macOS front end of AgentBox's Linux VM, and `bin/agentbox-linux`, the Linux build it installs in that
VM (D92). The app keeps both beside each other in
`~/.local/share/agentbox/bin`, and until the VM exists its first screen sets it up.

A new machine's setup is in the README's [Set up](../README.md#set-up), and a Mac's in [On a Mac](../README.md#on-a-mac).

## Develop

```bash
npm run typecheck
npm run build
UPDATE_TS=1 go test ./internal/api   # from the repository root, after changing internal/api/types.go
```

- `src/main`: the Electron main process. It relays API calls, the event stream, WebSocket streams and media files to the daemon's unix socket, and installs the command-line tool. It holds no AgentBox logic.
- `src/preload`: the bridge the renderer gets.
- `src/renderer`: the React UI (Tailwind, Radix, xterm.js, noVNC). The chat is `components/chat/`, with `lib/chat.ts` keeping each agent's thread up to date from the event stream (react-markdown, Shiki and jsdiff).
- `src/shared/api.ts`: the API types, generated from `internal/api/types.go`.
- `build/icon.svg`: the app icon; `build/icon.png` is rendered from it with `rsvg-convert -w 512 -h 512`.

The demo scripts in [`scripts/demo/`](../scripts/demo/) drive the app end to end with Playwright, on a private X display: `step-6.mjs` (agents and terminals), `step-7.mjs` (browser), `step-8.mjs` (media), `step-9.mjs` (Android), `chat.mjs` (the chat) and `packaging.mjs` (the AppImage).
