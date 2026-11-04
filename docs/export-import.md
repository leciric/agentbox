# Export and import

Export writes an AgentBox's data into one file; import merges that file into another AgentBox, on
another computer or after a reinstall. The same file works from the CLI and from the app's
**Settings → Export / Import**.

## What goes in the file

Each project's data comes in kinds, each of which you can leave out:

| Kind | What it is |
| --- | --- |
| `repository` | The remote URL and branch, or, with `--bundle`, a git bundle of every branch |
| `settings` | The project's settings: accounts, autonomy, models, branch prefix and the rest |
| `sections` | The sidebar section it's in |
| `icon` | Its icon |
| `notes` | Its notes |
| `skills` | The skills switched on for it, and those on for every project |
| `schedules` | Its schedules |
| `memory` | Memories, events, agents' reports and token usage |
| `chats` | The lead's chat and every past agent's (often the largest part) |
| `connectors` | Its connectors and AgentBox-wide ones, as a list: each is signed in to again |
| `secrets` | Its secrets, only when asked for |

Logins are only exported when asked for, each on its own: GitHub accounts, and the Claude Code,
Codex, OpenCode, Cursor and Gemini CLI accounts AgentBox keeps.

Machines, images and worktrees are never in it: the other AgentBox makes them again. Uncommitted
changes in a repository aren't either, bundled or not.

## Encryption

The file is encrypted with a passphrase you type (age's scrypt mode). Without the passphrase it
can't be opened, so keep it. `--no-encrypt` writes it in the clear, which is refused when it holds
secrets or logins.

## From the CLI

```bash
agentbox export --preview                       # what would go in, and how big
agentbox export ~/agentbox.agentbox             # everything but secrets and logins
agentbox export out.agentbox --bundle --secrets --login github,claude
agentbox export out.agentbox --project web --exclude chats,api:memory

agentbox import ~/agentbox.agentbox --dry-run   # what it holds, and what importing would do
agentbox import ~/agentbox.agentbox --repo web=/srv/src/web --exclude claude
```

`--exclude` takes a kind (`memory`), one project's kind (`web:chats`) or, on import, a login
(`github`). The passphrase is asked for, or read from `AGENTBOX_EXPORT_PASSPHRASE`.

## How an import merges

What is already here is skipped, so importing the same file twice changes nothing the second time.
What is here under the same name with other values is a conflict, and this AgentBox's own is kept.
The report lists, for each part, what was added, skipped and conflicted; it's saved under
`~/.local/share/agentbox/imports/`.

A project that isn't here yet needs its repository. Import looks where it was on the other
computer, or where you point it (`--repo name=/path`, or the path field in the app): a checkout
already there is used; otherwise the bundle is restored there when the file has one, else the
remote is cloned there. A project that is here already keeps its repository where it is.

An export from a newer AgentBox than this one is refused: update AgentBox, then import it.
