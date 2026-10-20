// What the agent list's rows show in their slot when an agent isn't using
// its desktop (components/v2/AgentList.tsx): the end of its terminal, and
// when its machine's usage is worth an amber strip.
import type * as T from '../../shared/api';

// heavy is when an agent's machine uses more than a core, or more than a
// quarter of the memory there is: its usage strip turns amber.
export const heavy = (usage: { cpu?: number; memory?: number }, hostMemory?: number) => (usage.cpu ?? 0) > 100 || (!!hostMemory && (usage.memory ?? 0) > hostMemory / 4);

// eslint-disable-next-line no-control-regex
const ansi = /\x1b\[[0-9;?]*[ -/]*[@-~]/g;
const textLines = (text: string) =>
  text
    .replace(ansi, '')
    .split('\n')
    .map((l) => l.trimEnd())
    .filter((l) => l.trim());

// terminalLines is what a row's terminal thumbnail shows, the last few lines
// of what the agent runs: its terminal while something runs in it (the AI
// tool, for an agent used from the terminal); else, for one used from the
// chat, its latest command or tool call and the end of its output; else the
// terminal at its prompt; else what the row's second line says.
export function terminalLines(tail: T.TerminalTail | undefined, items: T.ChatItem[] | undefined, fallback: string | undefined, n = 4): string[] {
  if (tail?.busy && tail.lines.length > 0) return tail.lines.slice(-n);
  const tool = items
    ?.slice(-30)
    .reverse()
    .find((i) => i.tool)?.tool;
  if (tool) return [tool.command ? `$ ${tool.command.split('\n')[0]}` : tool.title, ...textLines(tool.output ?? '').slice(-(n - 1))];
  if (tail && tail.lines.length > 0) return tail.lines.slice(-n);
  return fallback ? textLines(fallback).slice(0, n) : [];
}

