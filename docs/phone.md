# Chatting from your phone

You can read your chats and answer them from a phone's browser, on the same network as the computer
AgentBox runs on, or from anywhere through a Cloudflare Tunnel: the project's chat and each agent's, live, with the messages you send, the
permission requests an agent makes, and a stopped agent started again to chat with. Nothing else of
AgentBox is reachable from the phone. No hub is involved: the phone talks to your own daemon.

It's off until you turn it on.

## Turning it on and pairing a phone

In the app: **Settings → Phone**, turn on **Chat from your phone**. A QR code appears; scan it with
the phone's camera and open the link. The phone is paired, and shows your chats.

From the command line:

```bash
agentbox phone on        # turn it on, and print a QR code to scan
agentbox phone pair      # a new QR code, for another phone
agentbox phone           # whether it's on, where, and which phones are paired
agentbox phone list
agentbox phone revoke "Pixel 8"   # by name or by id
agentbox phone off       # close the port; paired phones stay paired
```

A QR code pairs one phone, within ten minutes. A phone that isn't paired sees only how to pair
itself. Unpairing a phone, from Settings, `agentbox phone revoke` or the phone itself, cuts it off at
once, open chat included.

The phone connects to port 7780 (`agentbox phone on --port <port>` moves it). If a firewall runs on
the computer, it has to let that port in from the local network.

## From anywhere

On the local network only, the phone stops working once you leave the house. **Reach from anywhere**
puts the same page on an https address on the internet, through a
[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
that AgentBox runs itself. No port is opened on your computer or your router: AgentBox connects out
to Cloudflare, and Cloudflare brings the phone's requests back through that connection.

In the app: **Settings → Phone**, turn on **Reach from anywhere**. The first time, AgentBox downloads
Cloudflare's `cloudflared` (about 40 MiB, checked against the release's published checksum). Once
the tunnel is up, the QR code carries its https address; pair the phone with it as usual.

```bash
agentbox phone tunnel on       # turn it on (and chatting from a phone), and print a QR code
agentbox phone tunnel          # whether it's on, and its address
agentbox phone tunnel off      # take the page off the internet; the local network still works
```

By default it's a **quick tunnel**: no Cloudflare account, on a random `trycloudflare.com` address
that changes every time AgentBox starts. A phone's pairing belongs to the address it paired on, so
after a restart, pair the phone again with a new QR code.

For an address that stays, use a **named tunnel** of your own, on a domain you have in Cloudflare:

1. In Cloudflare's dashboard, under Networks → Tunnels, create a tunnel (cloudflared type).
2. Give it a public hostname, like `chat.example.com`, whose service is `http://localhost:7781`.
3. Copy its token: the long string after `cloudflared service install`.
4. In **Settings → Phone → Use your own Cloudflare tunnel**, enter the hostname and paste the
   token; or run `agentbox phone tunnel on --hostname chat.example.com` and paste it when asked.

The token is stored encrypted, like your secrets, and never shown again. **Use a quick tunnel
instead** forgets it (`agentbox phone tunnel on --quick`).

### What it exposes

Anyone who has the address can open the pairing page, so this is off until you turn it on, and
worth turning off when you don't need it. What protects the chats is the pairing: nothing works
without a QR code you made in the last ten minutes, and each code pairs one phone. Wrong codes and
unknown phones are counted by address, and an address that keeps getting them wrong is turned away
for a few minutes. Over the tunnel the connection is https all the way to the phone, and the phone's
pairing cookie is only ever sent over https. A paired phone still reaches only the chats.

## Plain HTTP

On the local network, the connection is plain HTTP, not encrypted: someone else on the same network could read the chats
as they pass. Use it on a network you trust, such as your home's. The phone's pairing is a cookie
the page can't read, sent only with AgentBox's own requests, and a paired phone can reach the chats
and nothing else — no terminals, secrets, settings or agents' machines.

## Where it works

- **Linux, AgentBox in its VM** (the default): the VM's supervisor, on your computer, opens the port
  on your network and passes each request to the daemon in the VM. The tunnel runs in the VM.
- **Linux, AgentBox on the machine itself**: the daemon opens the port.
- **Mac and Windows**: not on the local network yet; Settings says so when you turn it on. Reach
  from anywhere doesn't depend on it: the tunnel runs next to the daemon, in AgentBox's VM.

The page the phone is shown is the app's web version, which the desktop app puts in the daemon when
it connects: open the app once after installing or updating AgentBox.
