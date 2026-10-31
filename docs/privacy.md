# What AgentBox sends

AgentBox checks for updates, can share anonymous usage stats, and sends a problem report only when
you ask it to. This page says exactly what each one sends and how to turn it off.

## The update check

Once a day, and as it starts, the AgentBox daemon asks `agentbox.linting.dev` whether a newer
release is out. When one is, the app shows **Update available** in its sidebar and
`agentbox version` prints a link to the release. Clicking it opens your channel's latest release
as the list of releases has it at that moment (the daemon reads it,
`GET https://downloads.agentbox.linting.dev/releases.json`, with nothing added to it), so a
release made since the last check isn't missed. Nothing is downloaded or installed until you
click it. Clicking it downloads that release's build for your machine from `downloads.agentbox.linting.dev`, checks it,
puts it in place of the one you're running and restarts into it; the command-line tool and the
daemon move to the new version too, the VM's included. The Mac's `.app`, the Windows installer's
install and the portable `.exe` are checked against the release's `SHA256SUMS` (and a Mac app's
code signature), the AppImage against its `latest-linux.yml`. An update waits while jobs are
running. A `.deb` or `.pacman` (only root can replace them), a Mac app still on its disk image or
in a folder you can't write to, and an update that fails open the release page instead.
The same request is how we count active installations.

**What is sent** is one HTTPS request with four query parameters, and nothing else:

```
GET https://agentbox.linting.dev/api/v1/latest?install=<uuid>&version=0.1.0&os=linux&arch=amd64
```

- `install`: a random UUID, made the first time the check runs and kept in AgentBox's own database
  (`state.db`). It is derived from nothing, so it can't be traced back to you or the machine. It
  only lets repeated checks from one installation be counted once. When AgentBox runs in a VM or a
  WSL2 distro, a copy is kept on your computer too (`~/.config/agentbox/install-id`, or
  `%LOCALAPPDATA%\AgentBox\install-id` on Windows), so a VM made again is still the same install.
- `version`: the version of AgentBox.
- `os` and `arch`: the operating system and processor architecture, such as `linux` and `amd64`.
  On a Mac or on Windows, where the daemon runs in a Linux VM or WSL2 distro, `os` is `darwin` or
  `windows`: the machine's, not the VM's.

**What isn't sent:** your name, username or hostname, anything about your projects, agents,
repositories or accounts, and any other ID or hardware detail. Like any web request, it comes from
your IP address. If the request fails or takes longer than 5 seconds, AgentBox ignores it and
tells nobody.

**Anonymous usage stats** go with the check, so we can see which features are used and which
aren't. AgentBox counts uses of a fixed list of features (the `Feature…` keys in
[`internal/api/types.go`](../internal/api/types.go), such as `agent.create.claude`, `lead.turn.codex`,
`pr.list` or `menu.agent.destroy`) in `state.db`, per UTC day, and once a day is over it sends the
counts in one more request, with the same four fields as the check:

```
POST https://agentbox.linting.dev/api/v1/usage
{"install":"<uuid>","version":"0.1.0","os":"linux","arch":"amd64",
 "days":[{"day":"2026-09-24","features":{"agent.create.claude":3,"pr.list":1}}]}
```

A key names a feature and nothing about what it was used on: no names, paths, repositories,
prompts or anything you typed. The model, effort and context window an agent is created with and
each completed turn ran with are counted per AI tool too (`agent.create.model.claude.opus_5_5`,
`agent.create.effort.claude.high`, `turn.context.codex.200k`): a model is matched against a fixed
list of known ones (`usageModels` in
[`internal/daemon/usagemodels.go`](../internal/daemon/usagemodels.go)) and anything else, a custom or
typed-in model among them, is sent as `other`.

The same request's day also sends **anonymous events**, each a name and fixed fields, every one a
number, a yes or no, or a value from a fixed list
([`internal/daemon/usageevents.go`](../internal/daemon/usageevents.go)), save an error's reason
below, with a random id so a retry isn't counted twice. Every event also carries the
installation's offset from UTC in whole hours (`utc_offset`, such as `-3`), from the time zone the
app last ran in, or `unknown`, so the stats can be read in local time:

- `turn.completed`, for each turn of an agent or a project's chat: the AI tool, model, effort,
  context window and permission mode as above, whether it was the chat or an agent, whether it was
  done, cancelled or failed, how long it took, its input, cached, cache-creation, output and
  reasoning tokens, and whether it used subagents.
- `heartbeat`, once a day: how many projects and agents there are and the most agents at once,
  how AgentBox runs (`chv`, `lima`, `wsl` or `host`), the VM's memory in a range such as `16_32g`,
  the app's language, the update channel, whether nesting is on, and how many connectors, Claude
  accounts and GitHub accounts there are.
- `agent.finished`, as an agent is destroyed or retired: its AI tool, how long it lived in a range
  such as `30m_2h`, and whether it opened a pull request and had one merged.
- `error`, by a fixed code such as `create_failed`, `image_build_failed`, `adapter_crashed` or
  `auth_failed`, with the AI tool involved, a class from a fixed list (`oom_killed`, `killed`,
  `acp_closed`, `incus_down`, `no_space`, `network`, `timeout`, `auth`, `exit_code` and others,
  `errorClasses` in [`internal/daemon/errorreason.go`](../internal/daemon/errorreason.go)), and a
  short reason: the error's own wording, at most 120 characters, with every path, URL, host and
  file name, email address, IP address, id, quoted text, token or key, and the names of your
  projects, agents, branches, user and machine replaced by placeholders, such as
  `starting the machine: incus start [name]: exit status 1`. It never holds a prompt, a file's
  contents or anything you typed.
- `setup`, the first time a new installation reaches each step (`setup_started`, `image_built`,
  `first_project`, `first_agent`, `first_pr`), with how long after the setup began, in a range.

```
POST https://agentbox.linting.dev/api/v1/events
{"install":"<uuid>","version":"0.1.0","os":"linux","arch":"amd64",
 "events":[{"id":"<random uuid>","day":"2026-09-24","name":"error",
            "props":{"code":"create_failed","tool":"claude","class":"no_space",
                     "reason":"copying the base: incus copy [path]: no space left on device",
                     "utc_offset":"-3"}}]}
```

**See what's sent** under **Settings → General** shows both requests exactly as the next check
would send them. Counts and events the server has are deleted from `state.db`, and those it never
got are dropped after 31 days. Switch off **Share anonymous usage stats** under
**Settings → General** to stop them and delete what wasn't sent yet; they are also off whenever
the update check is.

The server that receives the usage stats and events also records the country and state or region
each batch came from, taken from Cloudflare's request data. It never stores the IP address or the
city, and its admin shows only places with at least 5 installs.

**Nightly builds** are built from the next release as it stands, and published as
prereleases named `<next version>-nightly.<date>.<run>`, such as `0.11.0-nightly.20260929.12`.
They are for trying what's coming, and are never marked as the latest release. To be offered them,
pick **Nightly** under **Update channel** in **Settings → General**, or run
`agentbox version --channel nightly`; a nightly build starts out on that channel, and shows
**Nightly** in the app's sidebar. On the nightly channel the check also asks for the list of
releases (`GET https://downloads.agentbox.linting.dev/releases.json`, a public file, with nothing
added to it), since `agentbox.linting.dev` only answers with stable releases. Going back to
**Stable** offers the latest stable release, even though its version is lower than the nightly's.

**To turn the check off**, and the usage stats with it, switch off **Check for updates** in the app under **Settings → General**,
or set `AGENTBOX_NO_UPDATE_CHECK=1` or `DO_NOT_TRACK=1` in the daemon's environment (restart it with
`agentbox daemon stop` afterwards). Builds from source, which report version `dev`, never check.

## Reporting a problem

**Report a problem**, under **Settings → General**, or `agentbox report` sends us what went wrong in
your words, with what helps us find out why: AgentBox's version, your OS and how AgentBox runs on it
(on the machine itself, or in its VM), how its setup stands, the end of the daemon's log and, in VM
mode, of the VM supervisor's, and from the app its own log and recent errors. Tokens, keys, email
addresses and home folders are taken out of all of it, your message included, and you see every part
in full before anything is sent, and can leave any of them out (`agentbox report --show` prints it,
and sends nothing). It goes to `https://agentbox.linting.dev/api/v1/reports` with the same four fields
as the update check, and is kept for 90 days.

**Error reports** are the same, sent by the app itself when it hits an error it didn't expect: the
error, where in the app it happened, and the app's version and OS, with no logs and nothing you
typed. They are off until you turn them on: the first time an error happens, the app asks. Switch
**Send error reports automatically** under **Settings → General** to change your answer. They are
never sent while `DO_NOT_TRACK=1` is set.
