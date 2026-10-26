# A hub: your environments in one place

A hub puts every machine running AgentBox in one place — a VPS, another PC, the one you're on — so
the app, the CLI and a browser reach any of them from anywhere. Each machine, an **environment**,
connects out to the hub, so nothing on it has to be reachable. The hub is optional, and a separate
program: `agentbox-hub`.

Nothing on a hub has a password. Whoever opens its **setup link** first becomes its owner, and from
then on everything that connects — the app on another computer, a phone, a browser, an environment —
asks, shows a short code, and is let in once a device already connected **approves** it.

## Run one

```bash
npx agentbox-hub
```

With no flags it keeps its data in SQLite in AgentBox's data directory
(`~/.local/share/agentbox/hub.db`), listens on `127.0.0.1:8080`, and the first time it starts
prints a setup link and its QR code:

```
This hub has no owner yet. Whoever opens this link first becomes its owner:

  http://localhost:8080/hub/setup#abx_l_…
```

Open it in a browser, or paste it into the app's **Connect to a hub…**: you're the owner, and that
browser or app is your first device. The link works once; lost it before using it?
`npx agentbox-hub link` prints another and makes the old one stop working. Once the hub has an owner,
`agentbox-hub link` prints a link that signs the owner in once, within 15 minutes: the way back in
when every device you had is gone.

To reach it from other machines:

- **On your network**: `npx agentbox-hub --listen 0.0.0.0:8080`. The link it prints uses this
  machine's LAN address.
- **On a server**: keep it on `127.0.0.1`, put a TLS proxy such as Caddy in front of it, and give it
  its public address, which goes in the links it makes: `--url https://hub.example.com` (or
  `AGENTBOX_HUB_URL`). `--db postgres://…` keeps its data in Postgres instead.

`npx agentbox-hub --help` lists the rest. `agentbox-hub` is also a single binary, for a service
manager: the npm package only fetches the one for your platform.

## Connect the app

In the app, open the environment menu (top of the sidebar) and choose **Connect to a hub…**:

- Paste the hub's **setup link**, and you're connected as its owner.
- Or type its **address** (`https://hub.example.com`). The app shows a code like `BCDF-GHJK` and its
  QR code, and opens the hub's approval page in your browser. Approve it there (a browser that's
  connected to the hub), scan the QR code with a phone that is, or type the code into the app on a
  computer that is (**Approve a device…**). The app connects as soon as it's approved.

The CLI does the same: `agentbox login https://hub.example.com` prints the code, the approval link
and the QR code, and waits; `agentbox login <setup link>` uses the link. Then `agentbox env list`,
and `--env <name>` on any command.

## Connect an environment

On the machine you want to reach, open its AgentBox app, and in the environment menu, under the hub,
choose **Connect this AgentBox to the hub**. That's it: its AgentBox asks the hub, the app approves
the request itself since it's connected to the hub, and the machine connects, as an environment
named after its hostname. It reconnects by itself, also after restarts.

A machine without the app, like a VPS:

```bash
agentbox remote connect https://hub.example.com
```

It prints a code, the approval link and its QR code, and connects once you approve it from any
connected device. `agentbox remote status` says how it's doing, `agentbox remote disconnect` leaves.

## Approve, list and revoke devices

- **Approve a device…** (in the app, under the hub) takes the code a phone, a computer or a machine
  shows, says what it is and where it's asking from, and approves or denies it. Approve only what
  you started yourself, just now: an approved device can use every environment on the hub. Codes
  last 10 minutes.
- **Devices…** lists what's connected, and when each was last used; **Revoke** signs one out for
  good. It can ask again, and needs approving again.
- In a browser, the hub's own page (`https://hub.example.com/hub/`) does both, and lists the
  environments. A browser that isn't connected yet connects at `/hub/connect`.

An unused device signs out after 90 days.

## Accounts from before

Hubs used to have accounts with an email and a password (`agentbox-hub user add`). Those keep
working: the oldest one is the hub's owner, and approves devices like any other; their sessions
show up in **Devices…**; and they can still sign in with their password — the app's **Use a
password instead**, `agentbox login <hub> --email you@example.com`, or the hub's page — so nobody is
locked out by updating the hub. `agentbox-hub user password` still resets one. New accounts aren't
made with a password any more, and `--open-signup` does nothing.

## Phones

An environment's phones pair with it directly, not through the hub: see [Chatting from your phone](phone.md). A
hub with a Cloudflare zone gives each environment a stable address for them.
