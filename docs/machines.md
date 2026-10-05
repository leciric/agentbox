# Machines for Claude Code and Codex on your computer

[← back to the index](README.md)

`agentbox machines` gives an AI tool running on your own computer — Claude Code or Codex in a
terminal, or in an app like t3code — a desktop machine to test its work in, with the same desktop,
Chromium and recording an AgentBox agent has. It needs Docker or Podman, and neither AgentBox's VM
nor its daemon.

```bash
agentbox machines install     # add the "machine" MCP server to Claude Code and Codex, for every session
agentbox machines uninstall   # take it away again
```

`install` writes the server into `~/.claude.json` (or `$CLAUDE_CONFIG_DIR`) and
`~/.codex/config.toml` (or `$CODEX_HOME`), at user scope, leaving the rest of each file as it was.
Running it again changes nothing, or updates the path if `agentbox` moved.

## One machine per worktree

A session's machine is its working directory's: the top of its git checkout, mounted in the
machine at the same path, so a path means the same thing inside and out. Two sessions in two
worktrees of one project have two machines; two sessions in the same worktree share one. A session
started in your home directory has none, and its tools say so.

The first `machine_start` builds the image (`agentbox-machine:<hash>`, a few minutes, once per
AgentBox version); `agentbox machines build` does it ahead of time. The image holds no AI tool and
no account: a desktop (Xvnc, openbox), Chromium, ffmpeg, Node, git and Playwright's MCP server.

Commands run as you (your uid), so what they write in the worktree is yours. On rootless Podman or
Docker they run as root in the machine, which is you outside.

## The tools

| Tool | What it does |
| --- | --- |
| `machine_start`, `machine_stop`, `machine_status` | Make or start the machine, stop it, say how it is. |
| `run` | Run a shell command in the worktree; `background` for a dev server, answered with the start of its log and a job id to read more. |
| `preview_url` | The URL on your computer of a port published from the machine (`http://127.0.0.1:<port>`). |
| `view_url` | A link to watch the machine's desktop live in your browser, and take control of it. |
| `screenshot` | A picture of the desktop, in the conversation, also saved in full size. |
| `record_start`, `record_stop` | Record the desktop to an mp4. |
| `click`, `type`, `key`, `scroll` | The real mouse and keyboard. |
| `browser_*` | Playwright on the machine's Chromium: navigate, snapshot, click, type, evaluate, wait, console, network. |

## Screenshots and recordings

They're saved to `~/.local/share/agentbox/machines/media/<id>.png` or `.mp4`, each with an
`<id>.json` beside it saying where it came from: the worktree, repository, branch, AI tool and
session, its caption, size and duration. The tools answer with the path.

## Watching a machine live

```bash
agentbox machines serve --open   # a page on 127.0.0.1:7790
```

The page's sidebar lists your machines, with their repository, branch, uptime and memory, to start
and stop them. Click one to watch its desktop live: view-only until you press **Take control**,
which gives it your mouse and keyboard. **Paste clipboard** sends what you copied to the machine,
and what you copy there lands on your clipboard. The same page browses every screenshot and
recording.

Ask the AI tool for a link and it calls `view_url`, which starts `serve` in the background when it
isn't running. The desktop's VNC server listens only inside the machine: the page reaches it
through `docker exec` (or Podman's), and the page itself only answers on `127.0.0.1`, to pages it
served.

## Configuring a project's machine

`.agentbox/machine.json` in the worktree, or in the main checkout for every worktree of it:

```json
{
  "memory": "4g",
  "ports": [3000, 4173, 5173, 8000, 8080],
  "env_files": [".env"],
  "docker_inside": false
}
```

- `memory` caps the machine (4g by default).
- `ports` are published on `127.0.0.1` at a free port each, which `preview_url` tells.
- `env_files` are dotenv files, relative to the worktree, read into the machine's environment.
  That's how a project's secrets reach it, and nothing else of yours does: no GitHub or Claude
  token, no SSH key, no home directory.
- `docker_inside` runs Docker in the machine, for a project that needs it (kind, compose). The
  machine is then **privileged**, with high inotify and open-file limits, and its images are kept
  in a volume of its own. Off by default.

A change to any of these, or to an env file, makes the machine again at the next `machine_start`.

## Managing machines

```bash
agentbox machines list        # every machine, running or stopped, and its worktree
agentbox machines rm [dir]    # delete a worktree's machine, and its Docker volume
```

`AGENTBOX_MACHINES_RUNTIME=docker` picks Docker when Podman is installed too.
