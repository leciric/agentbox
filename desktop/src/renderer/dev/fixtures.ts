// Fixture data for src/renderer/dev/preview.tsx: a project's worth of agents,
// events and questions, deliberately carrying the content that breaks narrow
// fixed-width columns — a long absolute path, an unbroken URL, a long branch
// name, a stack trace, a code block, an unbroken error string, a compressed
// JSON payload. Kept separate from preview.tsx so a future scenario (a new
// component, a new kind of wide content) can reuse it without copying it.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';

export const PROJECT = 'agentbox';

export const WIDE = {
  longPath: '/home/user/.local/share/agentbox/worktrees/agentbox/agent-96/desktop/src/renderer/components/chat/Markdown.tsx',
  longUrl: 'https://github.com/leciric/agentbox/pull/78/files#diff-1a2b3c4d5e6f7890abcdef1234567890abcdef1234567890abcdef1234567890',
  branchName: 'agentbox/agent-104-fix-the-context-budget-warning-that-never-clears-after-consolidation-runs',
  stackTrace: `TypeError: Cannot read properties of undefined (reading 'ref')
    at AgentRow (/home/user/.local/share/agentbox/worktrees/agentbox/agent-96/desktop/src/renderer/components/AgentRail.tsx:214:19)
    at renderWithHooks (react-dom-client.development.js:4204:20)
    at updateFunctionComponent (react-dom-client.development.js:6639:19)`,
  longErrorString:
    'ECONNREFUSED_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0987654321_connect_to_daemon_socket_failed_no_such_file_or_directory_agentbox_daemon_sock',
  jsonBlob:
    '{"ref":"agentbox/agent-96","project":"agentbox","state":"running","limits":{"cpu":"2","allowance":"400%","memory":"4Gi"},"branch":"agentbox/agent-96-fix-the-agent-rail-overflowing-on-wide-text"}',
};

const codeSummary = `Found the cause: \`overflow-y-auto\` with no \`overflow-x\` handling.

Here is the fix:

\`\`\`tsx
function VeryLongFunctionNameThatDescribesExactlyWhatItDoesInGreatDetail(argumentWithALongNameToo: SomeVeryLongGenericTypeParameterName<AnotherType>): void {
  return doTheThing(argumentWithALongNameToo);
}
\`\`\`

And the path: ${WIDE.longPath}

And a URL: ${WIDE.longUrl}

\`${WIDE.longErrorString}\`
`;

const agent12Task = `In desktop/src/renderer/components/Sidebar.tsx, add a "+" button in the Projects header that calls onAddProject (the same action as the "Add your first project" button), next to "New section". Take before and after screenshots, commit on agentbox/agent-12, push it and open a pull request against main.`;

const agent12Done = `The code change is done: a Plus "New project" button in the Sidebar's Projects header calls onAddProject, next to "New section", whose icon is now FolderTree so the two don't both read as "add". Checked with \`npx tsc --noEmit\` and the preview harness, before and after screenshots in /tmp/pr-shots/.

Pushing failed: origin is git@github.com:leciric/agentbox-hub.git, SSH host key verification fails for github.com from this machine, and the GH_TOKEN here (leciric-work) can't see the repository over HTTPS ("Repository not found"), so there's no branch on GitHub and no pull request yet.`;

const agent12Stuck = `I don't have a request_credential tool: the memory server here lists search_memory, report, my_task and update_my_task only, so I can't ask for a GitHub account the way the brief says. The branch is committed and unchanged; it still needs a push from an account that can reach leciric/agentbox-hub, and the pull request after it.`;

function agent(overrides: Partial<T.Agent> & { ref: string }): T.Agent {
  return {
    project: PROJECT,
    name: overrides.ref.split('/')[1],
    title: '',
    instance: '',
    ai: 'claude',
    autonomous: true,
    branch: WIDE.branchName,
    baseRef: 'main',
    baseCommit: 'abc123',
    worktree: WIDE.longPath,
    source: 'main',
    claudeAccount: '',
    githubAccount: '',
    interface: 'claude',
    state: 'running',
    ip: '',
    limits: { cpu: '2', allowance: '400%', memory: '4Gi' },
    createdAt: new Date().toISOString(),
    ...overrides,
  };
}

// compactionThread is a project chat that has compacted three times (D73):
// once cleanly, once without managing to save the conversation, and once
// still running, with a message the user sent meanwhile held under the card.
export function compactionThread(): T.ChatThread {
  const at = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();
  let n = 0;
  const item = (minutesAgo: number, it: Partial<T.ChatItem> & { kind: string; turn: string }): T.ChatItem => ({ id: `c${++n}`, createdAt: at(minutesAgo), updatedAt: at(minutesAgo), ...it });
  const done = (minutesAgo: number) => ({ state: 'completed', endedAt: at(minutesAgo) });
  return {
    agent: `${PROJECT}/lead`,
    seq: 1,
    session: { state: 'ready', tool: 'claude', options: [], commands: [] },
    items: [
      item(50, { kind: 'user', turn: 'c1', text: 'Where did we get to with the launch posts?', result: done(49) }),
      item(49, { kind: 'assistant', turn: 'c1', text: 'All four are written; the Product Hunt one is waiting on the screenshots in PR #3.' }),
      item(48, { kind: 'compaction', turn: 'c1', text: 'Conversation continued in a fresh session; earlier context was consolidated into project memory.', compaction: { state: 'done' } }),
      item(30, { kind: 'user', turn: 'c4', text: `Check ${WIDE.longUrl} once more`, result: done(29) }),
      item(29, { kind: 'assistant', turn: 'c4', text: 'It still answers with the old landing page.' }),
      item(28, {
        kind: 'compaction',
        turn: 'c4',
        text: "Conversation continued in a fresh session, but its earlier context couldn't be saved to project memory.",
        compaction: { state: 'failed', error: `the agentbox chat didn't summarise itself: ${WIDE.longErrorString}` },
      }),
      item(3, { kind: 'user', turn: 'c7', text: 'Ship the Mac build without the Apple secrets.', result: done(1) }),
      item(1, { kind: 'assistant', turn: 'c7', text: 'agent-13 is on it; I will tell you when its PR is up.' }),
      item(0, { kind: 'compaction', turn: 'c7', compaction: { state: 'running', waiting: 1 } }),
      item(0, { kind: 'aside', turn: 'c7', text: 'And make sure the release notes mention the ad hoc signing.', delivery: 'held' }),
    ],
  };
}

export interface FixtureData {
  agents: T.Agent[];
  events: T.AgentEvent[];
  questions: T.Question[];
  fleet: T.Fleet;
  projects: T.Project[];
  sections: T.Section[];
}

// buildFixtures makes a fresh set every call, so timestamps like "just now"
// stay accurate across a long-running dev server rather than freezing at
// module load.
export function buildFixtures(): FixtureData {
  const agents: T.Agent[] = [
    agent({ ref: `${PROJECT}/agent-12`, title: 'Add a "New project" button to the Sidebar', branch: 'agentbox/agent-12' }),
    agent({ ref: `${PROJECT}/agent-94`, title: 'Needs a GitHub account' }),
    agent({ ref: `${PROJECT}/agent-95`, title: 'Needs a secret' }),
    agent({ ref: `${PROJECT}/agent-96`, title: 'Fix the agent rail overflowing on wide text' }),
    agent({ ref: `${PROJECT}/agent-97`, title: 'Long path agent' }),
    agent({ ref: `${PROJECT}/agent-98`, title: 'Question agent' }),
    agent({ ref: `${PROJECT}/agent-99`, title: 'PR agent' }),
  ];

  const events: T.AgentEvent[] = [
    { id: 'e1', project: PROJECT, agent: 'agent-97', ref: `${PROJECT}/agent-97`, kind: 'created', at: new Date(Date.now() - 90_000).toISOString() },
    {
      id: 'e2',
      project: PROJECT,
      agent: 'agent-97',
      ref: `${PROJECT}/agent-97`,
      kind: 'finished',
      summary: codeSummary,
      changes: { files: 12, insertions: 340, deletions: 58, dirty: false },
      at: new Date(Date.now() - 60_000).toISOString(),
    },
    {
      id: 'e3',
      project: PROJECT,
      agent: 'agent-98',
      ref: `${PROJECT}/agent-98`,
      kind: 'asked',
      question: 'q1',
      summary: `Should this handle the case where the daemon reports a JSON payload like ${WIDE.jsonBlob} inline in the summary, or truncate it?`,
      at: new Date(Date.now() - 30_000).toISOString(),
    },
    {
      id: 'e4',
      project: PROJECT,
      agent: 'agent-99',
      ref: `${PROJECT}/agent-99`,
      kind: 'finished',
      summary: `A stack trace showed up in the logs:\n\n\`\`\`\n${WIDE.stackTrace}\n\`\`\``,
      changes: { files: 3, insertions: 20, deletions: 4, dirty: true },
      pr: {
        number: 78,
        title: 'A pull request whose title is genuinely long enough to be a problem for a 268 pixel wide column',
        state: 'open',
        checks: 'passing',
        url: WIDE.longUrl,
      },
      at: new Date().toISOString(),
    },
  ];

  // Two credential requests (request_credential, D95), with the reason as
  // long as an agent's pasted error gets.
  events.push(
    {
      id: 'e5',
      project: PROJECT,
      agent: 'agent-94',
      ref: `${PROJECT}/agent-94`,
      kind: 'asked',
      question: 'q2',
      at: new Date(Date.now() - 20_000).toISOString(),
    },
    {
      id: 'e6',
      project: PROJECT,
      agent: 'agent-95',
      ref: `${PROJECT}/agent-95`,
      kind: 'asked',
      question: 'q3',
      at: new Date(Date.now() - 10_000).toISOString(),
    },
  );

  // agent-12 as the real one stood when its card didn't show: a task, two
  // finishes with long summaries, and then a GitHub request it is blocked on,
  // newest first as the daemon returns them. The card is the fourth event of
  // a thread that opens scrolled to the top.
  events.push(
    { id: 'e12d', project: PROJECT, agent: 'agent-12', ref: `${PROJECT}/agent-12`, kind: 'asked', question: '27322c8f', at: new Date(Date.now() - 60_000).toISOString() },
    {
      id: 'e12c',
      project: PROJECT,
      agent: 'agent-12',
      ref: `${PROJECT}/agent-12`,
      kind: 'finished',
      summary: agent12Stuck,
      changes: { files: 1, insertions: 12, deletions: 3, dirty: false },
      at: new Date(Date.now() - 110_000).toISOString(),
    },
    {
      id: 'e12b',
      project: PROJECT,
      agent: 'agent-12',
      ref: `${PROJECT}/agent-12`,
      kind: 'finished',
      summary: agent12Done,
      cut: true,
      changes: { files: 1, insertions: 12, deletions: 3, dirty: false },
      at: new Date(Date.now() - 2_200_000).toISOString(),
    },
    { id: 'e12a', project: PROJECT, agent: 'agent-12', ref: `${PROJECT}/agent-12`, kind: 'created', summary: agent12Task, at: new Date(Date.now() - 2_400_000).toISOString() },
  );

  const questions: T.Question[] = [
    {
      id: '27322c8f',
      project: PROJECT,
      agent: 'agent-12',
      ref: `${PROJECT}/agent-12`,
      kind: 'github',
      question:
        '`git push -u origin agentbox/agent-12` to git@github.com:leciric/agentbox-hub.git fails with "Host key verification failed" over SSH, and the current GH_TOKEN (account leciric-work) can\'t resolve this repo over HTTPS either ("Repository not found"). I need a GitHub account that can push to leciric/agentbox-hub so I can push this branch and open a PR.',
      status: 'escalated',
      createdAt: new Date(Date.now() - 60_000).toISOString(),
    },
    {
      id: 'q2',
      project: PROJECT,
      agent: 'agent-94',
      ref: `${PROJECT}/agent-94`,
      kind: 'github',
      question: `git push origin agentbox/agent-94 failed: "ERROR: Repository not found." The token here logs in as leciric-work, which can't see ${WIDE.longUrl}`,
      status: 'escalated',
      createdAt: new Date().toISOString(),
    },
    {
      id: 'q3',
      project: PROJECT,
      agent: 'agent-95',
      ref: `${PROJECT}/agent-95`,
      kind: 'secret',
      secretName: 'STRIPE_SECRET_KEY_FOR_THE_CHECKOUT_INTEGRATION_TESTS',
      question: 'The checkout integration tests call Stripe in test mode and need a secret key; there is none in the environment.',
      status: 'escalated',
      createdAt: new Date().toISOString(),
    },
    {
      id: 'q1',
      project: PROJECT,
      agent: 'agent-98',
      ref: `${PROJECT}/agent-98`,
      question: `Should this handle the case where the daemon reports a JSON payload like ${WIDE.jsonBlob} inline in the summary, or truncate it?`,
      context: WIDE.longPath,
      status: 'escalated',
      escalation: 'The chat could not decide, so it is asking you: ' + WIDE.longUrl,
      createdAt: new Date().toISOString(),
    },
  ];

  const fleet: T.Fleet = {
    project: PROJECT,
    agents: agents.map((a) => ({
      ...a,
      chat: undefined,
      changes: { files: 0, insertions: 0, deletions: 0, dirty: false },
      media: 0,
      pr: a.ref === `${PROJECT}/agent-99` ? { number: 78, title: 'PR', state: 'open', checks: 'passing', url: WIDE.longUrl } : undefined,
      retire: { safe: false },
    })),
    creating: [],
    idle: 0,
  };

  const projects: T.Project[] = [
    {
      name: PROJECT,
      root: WIDE.longPath,
      branch: 'main',
      envFiles: [],
      android: false,
      claudeAccount: '',
      claudeAccounts: [],
      githubAccount: '',
      autonomy: 'ask',
      agentModel: '',
      branchPrefix: 'agentbox/',
      mediaRetentionDays: 30,
      finishNotices: 'all',
      rolloverThreshold: 0,
      contextBudget: 0,
      consolidation: 0,
      consolidationModel: '',
      section: 's1',
      position: 0,
      createdAt: new Date().toISOString(),
    },
    {
      name: 'a-project-with-a-name-so-long-it-should-never-be-allowed-to-widen-the-sidebar',
      root: '/home/user/projects/other',
      branch: 'main',
      envFiles: [],
      android: false,
      claudeAccount: '',
      claudeAccounts: [],
      githubAccount: '',
      autonomy: 'ask',
      agentModel: '',
      branchPrefix: 'agentbox/',
      mediaRetentionDays: 30,
      finishNotices: 'all',
      rolloverThreshold: 0,
      contextBudget: 0,
      consolidation: 0,
      consolidationModel: '',
      section: '',
      position: 1,
      createdAt: new Date().toISOString(),
    },
  ];

  const sections: T.Section[] = [
    { id: 's1', name: 'A section name that is also much too long for this narrow column of pixels', position: 0, collapsed: false, createdAt: new Date().toISOString() },
  ];

  return { agents, events, questions, fleet, projects, sections };
}

// agent12Chat is agent-12's conversation as the credential request left it:
// told to push again, the push failing, and the request_credential call it is
// blocked on, spinning until you answer it.
export function agent12Chat(): T.ChatThread {
  const at = (ago: number) => new Date(Date.now() - ago).toISOString();
  const tool = (id: string, ago: number, t: T.ChatTool): T.ChatItem => ({ id, turn: 'u1', kind: 'tool', tool: t, createdAt: at(ago), updatedAt: at(ago) });
  return {
    agent: `${PROJECT}/agent-12`,
    seq: 1,
    session: { state: 'working', tool: 'claude', turnStartedAt: at(90_000), options: [], commands: [] },
    items: [
      { id: 'u1', turn: 'u1', kind: 'user', text: 'Push again. If it fails on GitHub auth, ask for an account with request_credential.', createdAt: at(90_000), updatedAt: at(90_000) },
      tool('t1', 80_000, {
        callId: 't1',
        name: 'Bash',
        title: 'git push -u origin agentbox/agent-12',
        kind: 'execute',
        status: 'failed',
        command: 'git push -u origin agentbox/agent-12',
        output: 'Host key verification failed.\nfatal: Could not read from remote repository.',
      }),
      tool('t2', 75_000, {
        callId: 't2',
        name: 'Bash',
        title: 'gh repo view leciric/agentbox-hub',
        kind: 'execute',
        status: 'failed',
        command: 'gh repo view leciric/agentbox-hub',
        output: 'GraphQL: Could not resolve to a Repository with the name \'leciric/agentbox-hub\'.',
      }),
      { id: 'a1', turn: 'u1', kind: 'assistant', text: "Both routes fail on auth, so I'm asking for a GitHub account that can push to leciric/agentbox-hub.", createdAt: at(65_000), updatedAt: at(65_000) },
      tool('t3', 60_000, { callId: 't3', name: 'mcp__memory__request_credential', title: 'mcp__memory__request_credential', kind: 'other', status: 'in_progress' }),
    ],
  };
}

// leadChat is the project's chat, where you talk to the lead, a little after
// it told agent-12 to push again: the credential cards come after it, drawn
// from the project's questions rather than written into the conversation.
export function leadChat(): T.ChatThread {
  const at = (ago: number) => new Date(Date.now() - ago).toISOString();
  return {
    agent: `${PROJECT}/lead`,
    seq: 1,
    session: { state: 'idle', tool: 'claude', options: [], commands: [] },
    items: [
      {
        id: 'l1',
        turn: 'l1',
        kind: 'user',
        text: 'agent-12 still has its branch unpushed. Get it pushed.',
        result: { state: 'completed', stopReason: 'end_turn', endedAt: at(100_000) },
        createdAt: at(120_000),
        updatedAt: at(120_000),
      },
      {
        id: 'l2',
        turn: 'l1',
        kind: 'assistant',
        text: 'I told agent-12 to push again. If the push fails on GitHub auth it asks you for an account with `request_credential`, and the card shows up here.',
        createdAt: at(110_000),
        updatedAt: at(110_000),
      },
    ],
  };
}

// devState is what the dev bridge serves for the projects and the stored
// accounts, and what picking a project's GitHub account or renaming one
// changes, so a scenario that refetches them after a change (?github=1) sees
// what the daemon would have answered.
const devState: { projects: T.Project[]; auth?: T.AuthStatus } = { projects: [] };

// seedQueryClient primes every query AgentRail and Sidebar read, at
// staleTime: Infinity (set by the caller's QueryClient), so nothing refetches
// through the stub bridge below.
export function seedQueryClient(queryClient: QueryClient, data: FixtureData): void {
  devState.projects = structuredClone(data.projects);
  queryClient.setQueryData(['agents'], data.agents);
  queryClient.setQueryData(['usage'], { host: { cpu: 0, cores: 1, memUsed: 0, memTotal: 0, poolUsed: 0, poolTotal: 0 }, agents: [] });
  queryClient.setQueryData(['fleet', PROJECT], data.fleet);
  queryClient.setQueryData(['agentEvents', PROJECT], data.events);
  queryClient.setQueryData(['questions', PROJECT], data.questions);
  queryClient.setQueryData(['chat', `${PROJECT}/lead`], leadChat());
  queryClient.setQueryData(['projects'], data.projects);
  queryClient.setQueryData(['sections'], data.sections);
  queryClient.setQueryData(['jobs'], []);
  queryClient.setQueryData(['setup'], { ready: true });
  queryClient.setQueryData(['target'], { kind: 'local' });
  queryClient.setQueryData(['hubs'], []);
  devState.auth = {
    claude: true,
    codex: false,
    opencode: false,
    claudeAccounts: [],
    github: true,
    githubAccounts: [
      { name: 'default', default: true, savedAt: new Date().toISOString(), login: 'leciric-work' },
      { name: 'personal-account-with-a-long-name', default: false, savedAt: new Date().toISOString(), login: 'a-github-login-that-is-long-too' },
    ],
  } satisfies T.AuthStatus;
  queryClient.setQueryData(['auth'], devState.auth);
}

// fakeVM prints what `agentbox vm resize` prints, a line at a time, so the
// preview shows a resize in progress and after.
function fakeVM() {
  const listeners = new Set<(text: string) => void>();
  return {
    onOutput: (fn: (text: string) => void) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    resize: async (cpus: number, memory: string) => {
      const lines = [
        `$ agentbox vm resize --cpus ${cpus} --memory ${memory}\n`,
        '==> Stopping AgentBox\'s VM, and every agent in it\n',
        'INFO[0000] Sending SIGINT to hostagent process\n',
        'INFO[0004] [hostagent] Shutting down VZ\n',
        `==> Giving the VM ${cpus} CPUs and ${memory} of memory\n`,
        'INFO[0000] Instance `agentbox` configuration edited\n',
        "==> Starting AgentBox's VM (agentbox)\n",
        'INFO[0012] READY. Run `limactl shell agentbox` to open the shell.\n',
        '==> Starting the daemon\n',
        'agentbox daemon started (pid 812)\n',
      ];
      for (const line of lines) {
        await new Promise((resolve) => setTimeout(resolve, 450));
        for (const fn of listeners) fn(line);
      }
    },
  };
}

// defaultsSettings are Settings for the ?defaults=1 scenario: an account whose
// menu has Opus, Sonnet and Haiku, agents on Sonnet at 1M and the lead left on
// Claude Code's own default. The dev bridge answers PATCH /v1/settings against
// it, refusing Haiku at 1M the way the daemon does.
let defaultsSettings = {
  defaultClaudeModel: 'sonnet',
  defaultAgentContextWindow: '1000000',
  defaultLeadModel: '',
  defaultLeadContextWindow: '',
  claudeModelChoices: [
    { value: 'default', name: 'Default (recommended)', description: 'Opus 5.5 with 1M context' },
    { value: 'sonnet', name: 'Sonnet', description: 'Sonnet 5 for everyday tasks' },
    { value: 'haiku', name: 'Haiku', description: 'Haiku 4.5 for quick answers' },
  ],
  claudeContextWindows: { default: [200_000, 1_000_000], opus: [200_000, 1_000_000], sonnet: [200_000, 1_000_000], haiku: [200_000] },
  claudeMenuKnown: true,
  defaultClaudeEffort: '',
  claudeEffortChoices: [],
  openCodeModelChoices: [],
  openCodeReady: false,
  defaultCPU: '4',
  defaultCPUAllowance: '',
  defaultMemory: '8GiB',
  hostCores: 16,
  hostMemory: 32 * 1024 ** 3,
  resumeAfterLimit: true,
  claudeCompactWindow: 200_000,
  updateCheck: true,
  defaultClaudeCompactWindow: 200_000,
} as T.Settings;

function patchDefaults(req: T.UpdateSettingsRequest): { status: number; body: string; contentType: string } {
  const next = { ...defaultsSettings };
  const window = (v: string) => (/^1m$|^1000000$/i.test(v) ? '1000000' : '');
  if (req.defaultClaudeModel !== undefined) next.defaultClaudeModel = req.defaultClaudeModel;
  if (req.defaultLeadModel !== undefined) next.defaultLeadModel = req.defaultLeadModel;
  if (req.defaultAgentContextWindow !== undefined) next.defaultAgentContextWindow = window(req.defaultAgentContextWindow);
  if (req.defaultLeadContextWindow !== undefined) next.defaultLeadContextWindow = window(req.defaultLeadContextWindow);
  for (const [model, win] of [
    [next.defaultClaudeModel || 'opus', next.defaultAgentContextWindow],
    [next.defaultLeadModel || 'default', next.defaultLeadContextWindow],
  ]) {
    if (win && !(next.claudeContextWindows[model] ?? []).includes(1_000_000))
      return { status: 400, body: JSON.stringify({ error: `${model} has no 1M context window: it only has 200k` }), contentType: 'application/json' };
  }
  // A new object each time, as a real response is: React Query ignores data
  // that is the same object it already holds.
  defaultsSettings = next;
  return { status: 200, body: JSON.stringify(defaultsSettings), contentType: 'application/json' };
}

// seedDefaults puts defaultsSettings where the Settings components read them.
export function seedDefaults(queryClient: QueryClient): void {
  queryClient.setQueryData(['settings'], defaultsSettings);
}

// installDevBridge stubs window.agentbox: every query above is pre-seeded
// and staleTime: Infinity keeps them from refetching, so nothing here needs
// to do real work — it only has to exist so components that call it don't
// throw outside Electron's preload.
export function installDevBridge(): void {
  (window as unknown as { agentbox: unknown }).agentbox = {
    request: async (method: string, path: string, body?: unknown) => {
      if (method === 'PATCH' && path === '/v1/settings') return patchDefaults(body as T.UpdateSettingsRequest);
      // Renaming a Claude account answers with what it carried over (the
      // ?accounts=1 scenario), and refuses a name one of the fixtures has.
      const rename = method === 'POST' ? /^\/v1\/auth\/claude\/([^/]+)\/rename$/.exec(path) : null;
      if (rename) {
        const name = (body as { name: string }).name;
        if (['default', 'work'].includes(name))
          return { status: 400, body: JSON.stringify({ error: `there is already a Claude Code account named "${name}": remove it first, or pick another name` }), contentType: 'application/json' };
        const got = { old: decodeURIComponent(rename[1]), name, projects: [PROJECT], agents: [`${PROJECT}/agent-01`, `${PROJECT}/lead`] };
        return { status: 200, body: JSON.stringify(got), contentType: 'application/json' };
      }
      if (method === 'GET' && path === '/v1/projects') return { status: 200, body: JSON.stringify(devState.projects), contentType: 'application/json' };
      if (method === 'GET' && path === '/v1/auth') return { status: 200, body: JSON.stringify(devState.auth), contentType: 'application/json' };
      // Picking a project's GitHub account, and renaming one (?github=1),
      // change what the two above answer, the way the daemon's would.
      const patched = method === 'PATCH' ? /^\/v1\/projects\/([^/]+)$/.exec(path) : null;
      const req = body as Partial<T.UpdateProjectRequest> | undefined;
      if (patched && req?.githubAccount !== undefined) {
        const project = devState.projects.find((p) => p.name === decodeURIComponent(patched[1]))!;
        project.githubAccount = req.githubAccount;
        return { status: 200, body: JSON.stringify(project), contentType: 'application/json' };
      }
      const renameGitHub = method === 'POST' ? /^\/v1\/auth\/github\/([^/]+)\/rename$/.exec(path) : null;
      if (renameGitHub && devState.auth) {
        const old = decodeURIComponent(renameGitHub[1]);
        const name = (body as { name: string }).name;
        const accounts = devState.auth.githubAccounts;
        if (accounts.some((a) => a.name === name))
          return { status: 400, body: JSON.stringify({ error: `there is already a GitHub account named "${name}": remove it first, or pick another name` }), contentType: 'application/json' };
        devState.auth = { ...devState.auth, githubAccounts: accounts.map((a) => (a.name === old ? { ...a, name } : a)).sort((a, b) => a.name.localeCompare(b.name)) };
        const projects = devState.projects.filter((p) => p.githubAccount === old);
        for (const p of projects) p.githubAccount = name;
        const got = { old, name, projects: projects.map((p) => p.name), agents: [`${PROJECT}/agent-01`] };
        return { status: 200, body: JSON.stringify(got), contentType: 'application/json' };
      }
      // Answering a credential request gives back the question, answered, the
      // way the daemon does, so the rail and the chat can both be seen to
      // settle.
      const answered = method === 'POST' && /\/questions\/([^/]+)\/credential$/.exec(path);
      const question = answered && buildFixtures().questions.find((q) => q.id === decodeURIComponent(answered[1]));
      const reply = question ? { ...question, status: 'answered', answeredBy: 'user', answer: 'You have the GitHub account "default" now.' } : {};
      return { status: 200, body: JSON.stringify(reply), contentType: 'application/json' };
    },
    connection: async () => ({ state: 'connected' }),
    onConnection: () => () => {},
    onEvent: () => () => {},
    info: async () => ({ socket: '', version: 'preview', electron: '', packaged: false, platform: 'darwin' }),
    stream: { open: async () => 0, write: () => {}, close: () => {}, onOpened: () => () => {}, onData: () => () => {}, onExited: () => () => {} },
    cli: { status: async () => ({}), install: async () => ({}) },
    hostSetup: { status: async () => ({}), run: async () => ({ restarted: false }), onOutput: () => () => {} },
    vm: fakeVM(),
    hubs: { list: async () => [], login: async () => ({}), logout: async () => {}, environments: async () => [], addEnvironment: async () => ({}) },
    target: { get: async () => ({ kind: 'local' }), set: async (t: unknown) => t, onChange: () => () => {} },
    mediaUrl: () => '',
    pickDirectory: async () => null,
    openPath: async () => '',
    showItem: async () => {},
    openExternal: async () => {},
    copyText: () => {},
    readText: async () => '',
  };
}
