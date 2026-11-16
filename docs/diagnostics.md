# Diagnostics

[← back to the index](README.md)

When AgentBox feels slow, the daemon can say where the time went. It keeps a trace of its own work
on your machine, and nothing in it is sent anywhere.

## What is kept

- **Spans**: one line for each API request, job, Incus call, git command, AI tool turn (ACP) and
  SQLite transaction the daemon ran, plus the spans the desktop app sends. Each line has its name,
  when it started, how long it took, whether it failed, and a few details. The file is
  `spans.ndjson`, next to `state.db` in AgentBox's data folder (`~/.local/share/agentbox` on
  Linux, inside the VM). It's rotated at 8 MiB, and three older files are kept (`spans.ndjson.1`
  is the newest). On a busy machine that's a few hours.
- **Stalls**: the spans where something was stuck, also written to `stalls.ndjson` (512 KiB and one
  older file, which is days or weeks):
  - the daemon took more than 2 seconds to start answering a request from the app, or it hasn't
    answered after 30 seconds;
  - a SQLite transaction waited more than 1 second for the database's write lock;
  - the VM's memory was stalling (pressure stall information: some task waiting for memory 20% of
    the last 10 seconds, or every task 5%), or its CPU (some task waiting for a CPU 60% of the
    time). One stall covers the whole episode, with the worst it got.
- **Resources**: what the VM and each agent used (CPU, memory, processes, disk), sampled every
  30 seconds and kept in the daemon's memory for an hour.

## `agentbox trace summary`

```bash
agentbox trace summary                         # the last 30 minutes
agentbox trace summary --since 2h --limit 40
```

It reads the files directly, so it works while the daemon is hung or stopped. Each line is one kind
of span: how many ended in the window, how many per minute, how many failed, and the median (P50),
95th percentile (P95), longest (MAX) and total time. The busiest come first, by total time. The
stalls in the window are listed below the table.

Long-lived requests, like the app's event stream or a terminal, count for as long as they stay
open, so they can top the table without being slow.

## The daemon's memory

```bash
agentbox debug heap                            # saves agentbox-heap-<time>.pb.gz here
go tool pprof -top -sample_index=inuse_space agentbox-heap-*.pb.gz
```

`agentbox debug heap` saves what the daemon's heap holds, by where it was allocated, after a garbage
collection, and prints how much is live and how much the daemon has from the system. Go's other
profiles (goroutines, CPU, allocations) are at `/debug/pprof/` on the daemon's socket, and only
there: not to phones or the hub.

The daemon keeps its heap under a soft limit of 256 MiB: near it, Go collects garbage more often
rather than asking for more memory. Set `GOMEMLIMIT` in the daemon's environment to choose another
(`GOMEMLIMIT=512MiB`), or `GOMEMLIMIT=off` for none.

## GitHub's rate limit

GitHub gives each account 5,000 REST requests an hour, and AgentBox's token is shared: the daemon,
the app's views behind it and every agent's own `gh` spend the same budget. `agentbox github usage`
shows what the daemon sent since it started, by account, by what asked (the pull request list, the
pull request watch, the pull request view, issue pickup…) and by endpoint, with what GitHub last said
is left. Its "others" figure is what GitHub counted beyond AgentBox's own requests: mostly agents'
`gh`, and anything else of yours using the token.

AgentBox asks GitHub again for what it already read with its ETag (an unchanged answer is free),
shares a read in flight, reads every open pull request's checks in one GraphQL query, and re-reads
less often once a fifth of the budget is left. Below 5%, its background reads wait for the reset;
once GitHub says the budget is spent, nothing is sent until then, and the app says when that is.
`AGENTBOX_GITHUB_DEBUG=1` in the daemon's environment logs every request.

## The API

The app's Diagnostics page reads the same data over the daemon's socket:

| Route | What it answers |
| --- | --- |
| `GET /v1/diagnostics/spans?since=30m` | per span name: count, errors, p50/p95/max ms; the 50 slowest spans; the latest 50 failures |
| `GET /v1/diagnostics/stalls?since=24h` | the stalls, newest first |
| `GET /v1/diagnostics/resources?window=1h` | the VM's samples and each agent's |
| `POST /v1/diagnostics/client-spans` | the app's own spans, up to 500 at a time, stored with `source: "app"` |
| `GET /v1/github/usage` | each GitHub token's requests since the daemon started, and what is left of its budgets |
