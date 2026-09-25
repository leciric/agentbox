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

  const questions: T.Question[] = [
    {
      id: 'q2',
      project: PROJECT,
      agent: 'agent-94',
      ref: `${PROJECT}/agent-94`,
      kind: 'github',
      question: `git push origin agentbox/agent-94 failed: "ERROR: Repository not found." The token here logs in as luisflorido-stf, which can't see ${WIDE.longUrl}`,
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

// seedQueryClient primes every query AgentRail and Sidebar read, at
// staleTime: Infinity (set by the caller's QueryClient), so nothing refetches
// through the stub bridge below.
export function seedQueryClient(queryClient: QueryClient, data: FixtureData): void {
  queryClient.setQueryData(['agents'], data.agents);
  queryClient.setQueryData(['usage'], { host: { cpu: 0, cores: 1, memUsed: 0, memTotal: 0, poolUsed: 0, poolTotal: 0 }, agents: [] });
  queryClient.setQueryData(['fleet', PROJECT], data.fleet);
  queryClient.setQueryData(['agentEvents', PROJECT], data.events);
  queryClient.setQueryData(['questions', PROJECT], data.questions);
  queryClient.setQueryData(['projects'], data.projects);
  queryClient.setQueryData(['sections'], data.sections);
  queryClient.setQueryData(['jobs'], []);
  queryClient.setQueryData(['setup'], { ready: true });
  queryClient.setQueryData(['target'], { kind: 'local' });
  queryClient.setQueryData(['hubs'], []);
  queryClient.setQueryData(['auth'], {
    claude: true,
    codex: false,
    opencode: false,
    claudeAccounts: [],
    github: true,
    githubAccounts: [
      { name: 'default', default: true, savedAt: new Date().toISOString(), login: 'luisflorido-stf' },
      { name: 'personal-account-with-a-long-name', default: false, savedAt: new Date().toISOString(), login: 'a-github-login-that-is-long-too' },
    ],
  } satisfies T.AuthStatus);
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

// installDevBridge stubs window.agentbox: every query above is pre-seeded
// and staleTime: Infinity keeps them from refetching, so nothing here needs
// to do real work — it only has to exist so components that call it don't
// throw outside Electron's preload.
export function installDevBridge(): void {
  (window as unknown as { agentbox: unknown }).agentbox = {
    request: async () => ({ status: 200, body: '{}', contentType: 'application/json' }),
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
