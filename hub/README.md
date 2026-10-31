# AgentBox hub

The hub: accounts, the environments (machines running AgentBox) that connect to
it, and the proxy that reaches them. It is the server half of AgentBox, a
program of its own in AgentBox's module; everything that *uses* a hub is in the
rest of this repository. MIT, like the rest.

```bash
npx agentbox-hub                     # or, from the repository's root:
go test ./hub/... ./hubapi/...
go build -o bin/agentbox-hub ./hub/cmd/agentbox-hub
bin/agentbox-hub                     # SQLite in AgentBox's data directory, on 127.0.0.1:8080
```

The first start prints a one-time setup link and its QR code: whoever opens it
first becomes the hub's owner. Everything else that connects — the app, the
CLI, a browser, a phone, an environment — asks with `POST /v1/pair`, shows a
code, and is let in when a device already connected approves it
(`internal/hub/pairing.go`, the hub's own pages in `pages.html`). There are no
passwords, except on accounts made before pairing, which keep theirs.
`agentbox-hub link` prints another setup link, or, once the hub has an owner,
a 15-minute sign-in link for them.

Using a hub, and running one on a server (`--listen`, `--url`, Postgres with
`--db`, a TLS proxy in front): [`docs/hub.md`](../docs/hub.md).
Publishing the npm package: [`npm/PUBLISHING.md`](npm/PUBLISHING.md).

## What's here

| Path | What it is |
|------|-----------|
| `cmd/agentbox-hub/` | The binary. It was `agentbox server` in the AgentBox CLI. |
| `internal/hub/server.go` | The HTTP server: sessions, environments, the tunnel endpoint, and the reverse proxy to a connected environment. |
| `internal/hub/pairing.go` | The setup link, pairing (a device or an environment asks, a signed-in device approves its code), and devices. |
| `internal/hub/pages.go`, `pages.html` | The hub's own pages: setup, approval, connecting a browser, and the list of environments and devices. |
| `internal/hub/store.go`, `sqlstore.go` | Accounts, devices (sessions), setup links and environments, in Postgres or SQLite, with the migrations. |
| `internal/hub/auth.go` | Minting and hashing tokens, and argon2id passwords for the accounts from before pairing. |
| `npm/`, `scripts/npm-build.sh` | `npx agentbox-hub`: a launcher, and a package per platform with the binary. |
| [`../hubapi/`](../hubapi/) | The protocol, shared with the rest of AgentBox. See below. |

## Stable addresses for phones

An environment's phones can reach it from anywhere through a Cloudflare
Tunnel. With a Cloudflare zone, the hub gives each environment one that
stays the same: the first time the environment asks
(`POST /v1/environment/tunnel`, with its environment token), the hub makes a
remotely-managed tunnel named `agentbox-<id>` and a proxied CNAME
`<id>.<zone>` → `<tunnel>.cfargotunnel.com`, routes it to the daemon
(`hubapi.EnvironmentTunnelOrigin`), and answers the hostname and the tunnel's
token; after that it answers the same hostname every time
(`internal/hub/tunnels.go`, `cloudflare.go`). The environment runs
cloudflared with the token: phone traffic goes through Cloudflare straight to
it, never through the hub, and phones still pair with the environment's own
one-time secret, which the hub never sees.

To turn it on, give the hub:

- **A zone**, by its ID (the zone's Overview page in Cloudflare's dashboard):
  `--cloudflare-zone <id>` or `AGENTBOX_HUB_CLOUDFLARE_ZONE`. Hostnames are
  one level below the zone, which Cloudflare's Universal SSL covers.
- **An API token**, in `AGENTBOX_HUB_CLOUDFLARE_TOKEN`, or a file named by
  `AGENTBOX_HUB_CLOUDFLARE_TOKEN_FILE` (a mounted secret, say); never a flag.
  Make it under My Profile → API Tokens → Create Custom Token, with:
  - Account › **Cloudflare Tunnel** › Edit, on the zone's account;
  - Zone › **DNS** › Edit, and Zone › **Zone** › Read, on the zone.

Without both, the hub answers 501 and environments use a quick tunnel. Once an
hour it removes the tunnels and records of deleted environments, and of those
neither connected nor asking for 60 days; one that comes back gets its
hostname again.

## Tailscale instead of Cloudflare

`--transport tailscale` (or `AGENTBOX_HUB_TRANSPORT=tailscale`) puts each
environment in your tailnet instead of giving it a Cloudflare Tunnel: asked
the same `POST /v1/environment/tunnel`, the hub answers an auth key
(`hubapi.TransportTailscale`), the environment's daemon joins the tailnet
with it as a node named `agentbox-<name>-<id>` (tailscaled in userspace mode,
next to the daemon), serves on it with `tailscale serve`, and reports the
address it got (`POST /v1/environment/address`), which the hub lists on the
environment. Only the tailnet's devices reach it; nothing is on the internet,
and the hub sees none of the traffic (`internal/hub/tailscale.go`,
`tunnels.go`).

Give the hub one of:

- **An auth key**, in `AGENTBOX_HUB_TAILSCALE_AUTHKEY` or a file named by
  `AGENTBOX_HUB_TAILSCALE_AUTHKEY_FILE`: reusable, pre-approved, and tagged
  `tag:agentbox` (the admin console's Settings → Keys). Every environment
  gets the same key, and the hub can't remove their nodes.
- **An OAuth client**, `--tailscale-oauth-client <id>` or
  `AGENTBOX_HUB_TAILSCALE_OAUTH_CLIENT`, with its secret in
  `AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET` or `…_SECRET_FILE`, made under
  Settings → OAuth clients with the **Auth Keys** (write) and **Devices
  Core** (write) scopes and the tag `tag:agentbox`. The hub makes each
  environment a one-off key that lasts an hour, and removes its node when the
  environment is deleted or idle for 60 days.

Secrets are never flags and never logged. `--tailscale-tailnet` (or
`AGENTBOX_HUB_TAILSCALE_TAILNET`) names the tailnet, as Settings → General
shows it, `-` (the key's own) by default; `--tailscale-tag` changes the tag.
The tailnet needs MagicDNS and HTTPS certificates on, and a policy that owns
the tag and lets your devices reach it; AgentBox's
[`docs/hub.md`](../docs/hub.md#tailscale-instead-of-cloudflare)
has the policy. A hub moved from Cloudflare to Tailscale keeps its zone, when
it still has one, only to remove the tunnels it made before.

## Where it came from

The hub was `internal/hub/` and `agentbox server` in AgentBox until
[#21](https://github.com/leciric/agentbox/pull/21) split it out (commit
`6a7091e`) into its own repository, lintingdotdev/agentbox-hub, where it got
`npx agentbox-hub`, pairing and Tailscale. [#547](https://github.com/leciric/agentbox/pull/547)
brought it back as `hub/`, from that repository's `feat-tailscale` (`d2e5bd4`),
whose history stays there. It stays a separate program (D44): it shares only
`hubapi` with the rest of AgentBox.

## `hubapi`, AgentBox's protocol

The hub needs one thing from the rest of AgentBox: [`hubapi`](../hubapi/), the
wire types, the token prefixes, the endpoint paths, and the yamux settings both
ends of the tunnel configure. It imports nothing else of AgentBox: no client,
no daemon, no CLI (`TestHubImportsOnlyHubapi`). A change to `hubapi` is
checked against the hub and the client by the same `go test ./...`.

## Testing

`internal/hub/connector_test.go` is an environment for the hub's own tests;
the real connector is AgentBox's `internal/remote`. It speaks the
protocol and nothing more, so a passing test here means the hub matches
`hubapi`, not that it matches AgentBox. Checking the two against each other
means running a real AgentBox against a real hub: `scripts/demo/remote.mjs`
does, with `AGENTBOX_HUB_BIN` pointing at a built `agentbox-hub`.

The store tests run on SQLite by default, and on Postgres when
`AGENTBOX_TEST_POSTGRES` names a database they may empty.
