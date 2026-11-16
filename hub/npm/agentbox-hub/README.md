# agentbox-hub

The AgentBox hub: puts every machine running AgentBox in one place.

```sh
npx agentbox-hub
```

With no flags it runs on SQLite in a data directory, with no config, and prints a one-time setup link and a QR code. Whoever opens the link first becomes the owner.

- `--listen 0.0.0.0:8080` to reach it from other machines.
- `--url https://hub.example.com` for the address it puts in links.

`npx agentbox-hub --help` lists the rest.

The binary comes in a per-platform package (`@agentbox-hub/<os>-<cpu>`) installed as an optional dependency: linux, macOS and Windows on x64 and arm64. Don't install with `--omit=optional`. Set `AGENTBOX_HUB_BINARY` to run a binary of your own.
