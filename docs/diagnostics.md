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

## Where agents' time goes: `agentbox timing`

```bash
agentbox timing                                # every agent, the last 24 hours
agentbox timing --agent acme/agent-12 --since 1h
agentbox timing --project acme --since 7d --top 20
```

Every chat turn is timed as it ends, and so is every tool call in it and every heavy command (a
test run or a build that goes through `agentbox heavy` or the shims for `go`, `npm`, `make`…).
The report splits the turns' time into:

- **Model**: waiting on the model, from each request (the prompt, or a tool's result) to the last
  token it streamed, with how long the first token took per turn and per request.
- **Tools**: the time at least one of the turn's own tool calls ran. Calls in parallel count
  once, and a subagent's calls are inside its Agent call.
- **Starting**: from the message to the prompt reaching the AI tool, which is mostly the tool
  starting up.
- **Other**: what's left: permissions, the adapter, and whatever nothing timed.

Then it lists each agent, the slowest tools, the slowest single calls, and the slowest heavy
commands with the worker count each was given and how it exited (`?` from an older `agentbox`
inside the agent, which doesn't say). Heavy commands run inside tool calls or a terminal, so their
time is part of the tools' time. Timings are kept for 30 days and go when their project does.

## What agents still have running: `agentbox ps` and `agentbox kill`

An agent can leave a dev server, a watcher or a database running after it's done with it, and those
hold memory until something stops them. The app's **Running** view lists what each agent's machine
still runs. Open it from the **Running** button at the foot of the sidebar, whose badge counts what
you can kill, or from the top bar: click the machine's status, or the memory meter in the detailed
top bar, then **What's running**. It opens on the project you're in, with a switch to every
project's. Each agent's **Resources** panel shows its own list.

- Processes are grouped in trees. A dev server and everything it started count as one tree, shown
  by the command that started it (for a Claude Code Bash tool, the command, not its shell).
- Each tree shows its memory (proportional set size, so shared memory is counted once), CPU, how
  long it has run, and how long it has used next to no CPU. Trees of 1 GiB or more are flagged
  **Heavy**, and trees idle for 30 minutes or more are flagged **Idle**.
- The AI tool's own processes (its ACP adapter, the tool, its MCP servers) and the machine's own
  (init and its services, tmux and its shell, Docker's daemon, the desktop's display) are listed as
  **Protected**, so the totals add up, and can't be killed here.
- **Kill** asks for confirmation in the row. Then every process in the tree gets SIGTERM, and
  whatever is left after 5 seconds gets SIGKILL. The agent is told in its chat what you killed, so
  it doesn't start it again without a reason.

The same from the command line:

```bash
agentbox ps                          # every agent with a running machine, the biggest trees first
agentbox ps myproject                # one project's agents
agentbox ps myproject/agent-07 -a    # one agent, with each tree's processes
agentbox kill myproject/agent-07 412 # kill pid 412 and everything it started
```

The pids are the ones the agent's own `ps` shows in its machine.

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
| `GET /v1/timing?since=24h&project=&agent=&top=10` | turns' model, tool and start time in total and by agent; the slowest tools, calls and heavy commands |
| `GET /v1/github/usage` | each GitHub token's requests since the daemon started, and what is left of its budgets |
