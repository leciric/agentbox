# Chatting from your phone

You can read your chats and answer them from a phone's browser, on the same network as the computer
AgentBox runs on: the project's chat and each agent's, live, with the messages you send, the
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

## Plain HTTP

The connection is plain HTTP, not encrypted: someone else on the same network could read the chats
as they pass. Use it on a network you trust, such as your home's. The phone's pairing is a cookie
the page can't read, sent only with AgentBox's own requests, and a paired phone can reach the chats
and nothing else — no terminals, secrets, settings or agents' machines.

## Where it works

- **Linux, AgentBox in its VM** (the default): the VM's supervisor, on your computer, opens the port
  on your network and passes each request to the daemon in the VM.
- **Linux, AgentBox on the machine itself**: the daemon opens the port.
- **Mac and Windows**: not yet. Settings says so when you turn it on.

The page the phone is shown is the app's web version, which the desktop app puts in the daemon when
it connects: open the app once after installing or updating AgentBox.
