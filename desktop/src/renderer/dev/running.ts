// The Running view's fixtures (?running=): what three of the fixture project's
// agents still have running, as GET /v1/processes would say it. agent-99 is
// the case the view was made for: a `nest start --watch` it started from its
// Bash tool and forgot, 2.3 GiB and idle for two hours. A kill answers as the
// daemon would and the tree leaves the list; the tool's and the machine's
// are refused, as the daemon refuses them.
import type * as T from '../../shared/api';
import { PROJECT } from './fixtures';

const MiB = 1024 ** 2;
const GiB = 1024 ** 3;

function tree(
  pid: number,
  label: string,
  memory: number,
  more: Partial<T.ProcessTree> & { minutes?: number; members?: T.ProcessMember[] } = {},
): T.ProcessTree {
  const { minutes = 30, ...rest } = more;
  const kind = rest.kind ?? 'agent';
  const isProtected = kind !== 'agent';
  return {
    pid,
    label,
    command: label,
    kind,
    protected: isProtected,
    processes: rest.members?.length || 1,
    memory,
    rss: Math.round(memory * 1.4),
    pss: memory,
    cpu: 0,
    cpuSeconds: 12,
    started: new Date(Date.now() - minutes * 60_000).toISOString(),
    idleSeconds: 0,
    heavy: !isProtected && memory >= GiB,
    idle: false,
    members: [{ pid, ppid: 1, name: label.split(' ')[0], command: label, memory }],
    ...rest,
  };
}

function agentOf(name: string, title: string, trees: T.ProcessTree[]): T.AgentProcesses {
  return {
    ref: `${PROJECT}/${name}`,
    project: PROJECT,
    name,
    title,
    running: true,
    trees,
    memory: trees.reduce((n, t) => n + t.memory, 0),
    killable: trees.filter((t) => !t.protected).reduce((n, t) => n + t.memory, 0),
    cpu: trees.reduce((n, t) => n + t.cpu, 0),
    processes: trees.reduce((n, t) => n + t.processes, 0),
  };
}

function claude(pid: number, memory: number, cpu = 0.4): T.ProcessTree {
  return tree(pid, 'Claude Code', memory, {
    kind: 'tool',
    cpu,
    minutes: 190,
    command: 'node /home/lint/.local/share/mise/installs/npm-agentclientprotocol-claude-agent-acp/0.109.0/node_modules/.bin/claude-agent-acp',
    members: [
      { pid, ppid: 188, name: 'node', command: 'node …/claude-agent-acp', memory: 110 * MiB },
      { pid: pid + 29, ppid: pid, name: 'claude', command: '…/claude --output-format stream-json --verbose', memory: memory - 140 * MiB },
      { pid: pid + 52, ppid: pid + 29, name: 'agentbox', command: '/usr/local/bin/agentbox memory mcp', memory: 30 * MiB },
    ],
  });
}

function system(memory: number): T.ProcessTree {
  return tree(1, 'System', memory, { kind: 'system', minutes: 200, command: '/sbin/init', processes: 24 });
}

function fixtureAgents(): T.AgentProcesses[] {
  return [
    agentOf('agent-99', 'PR agent', [
      tree(412, 'npx nest start --watch', 2.3 * GiB, {
        minutes: 134,
        idleSeconds: 2 * 3600 + 10 * 60,
        idle: true,
        cpu: 0.1,
        cpuSeconds: 41,
        command: `/bin/bash -c source /home/lint/.claude/shell-snapshots/snapshot-bash.sh && eval 'npx nest start --watch' < /dev/null`,
        members: [
          { pid: 412, ppid: 214, name: 'bash', command: "/bin/bash -c … eval 'npx nest start --watch'", memory: 4 * MiB },
          { pid: 413, ppid: 412, name: 'node', command: 'node /w/api/node_modules/.bin/nest start --watch', memory: 880 * MiB },
          { pid: 466, ppid: 413, name: 'node', command: 'node --enable-source-maps /w/api/dist/main', memory: 1.4 * GiB },
        ],
      }),
      claude(214, 610 * MiB, 2.1),
      tree(980, 'chromium --remote-debugging-port=9222 --user-data-dir=/home/lint/.config/agentbox-browser', 420 * MiB, { minutes: 95, cpu: 1.2, idleSeconds: 40 }),
      system(160 * MiB),
      tree(1502, 'sleep 600', 1 * MiB, { minutes: 3 }),
      tree(1530, 'tail -f /w/api/logs/app.log', 2 * MiB, { minutes: 50, idleSeconds: 3000 }),
    ]),
    agentOf('agent-12', 'Add a "New project" button to the Sidebar', [
      tree(733, 'npm run dev -- --host 0.0.0.0 --port 5173 --strictPort --open=false --clearScreen=false --logLevel=info', 640 * MiB, {
        minutes: 22,
        cpu: 6.5,
        members: [
          { pid: 733, ppid: 523, name: 'npm run dev', command: 'npm run dev -- --host 0.0.0.0 --port 5173', memory: 70 * MiB },
          { pid: 744, ppid: 733, name: 'node', command: 'node /w/web/node_modules/vite/bin/vite.js --host 0.0.0.0', memory: 570 * MiB },
        ],
      }),
      claude(301, 540 * MiB, 18),
      tree(870, 'container: postgres', 180 * MiB, { minutes: 70, idleSeconds: 2400, idle: true }),
      system(140 * MiB),
    ]),
    agentOf('agent-90', 'Bump Electron to the next major, and every native module that breaks with it', [claude(230, 480 * MiB), system(120 * MiB)]),
  ].sort((a, b) => b.memory - a.memory);
}

let agents: T.AgentProcesses[] = [];

const json = (status: number, body: unknown) => ({ status, body: JSON.stringify(body), contentType: 'application/json' });

// seedRunning fills the fixtures, afresh each time it's called.
export function seedRunning(): void {
  agents = fixtureAgents();
}

// runningRequest answers the Running view's routes from the fixtures.
export function runningRequest(method: string, path: string): { status: number; body: string; contentType: string } | undefined {
  if (agents.length === 0) return undefined;
  if (method === 'GET' && (path === '/v1/processes' || path.startsWith('/v1/processes?'))) {
    return json(200, { agents, at: new Date().toISOString() } satisfies T.ProcessList);
  }
  const one = /^\/v1\/agents\/([^/]+)\/([^/]+)\/processes$/.exec(path);
  if (method === 'GET' && one) {
    const a = agents.find((x) => x.ref === `${decodeURIComponent(one[1])}/${one[2]}`);
    return a ? json(200, a) : json(200, { ...agentOf(one[2], '', []), running: false });
  }
  const kill = /^\/v1\/agents\/([^/]+)\/([^/]+)\/processes\/(\d+)\/kill$/.exec(path);
  if (method === 'POST' && kill) {
    const ref = `${decodeURIComponent(kill[1])}/${kill[2]}`;
    const pid = Number(kill[3]);
    const a = agents.find((x) => x.ref === ref);
    const t = a?.trees.find((x) => x.pid === pid);
    if (!a || !t) return json(404, { error: `${ref}: pid ${pid}: no such process in its machine` });
    if (t.protected) return json(403, { error: `${ref}: pid ${pid} is the AI tool's own, which the agent's chat needs: protected`, code: 'protected-process' });
    agents = agents.map((x) => (x === a ? agentOf(a.name, a.title, a.trees.filter((y) => y !== t)) : x));
    return json(200, { ref, pid, label: t.label, processes: t.processes, memory: t.memory, forced: 0, left: 0 } satisfies T.KillProcessResult);
  }
  return undefined;
}
