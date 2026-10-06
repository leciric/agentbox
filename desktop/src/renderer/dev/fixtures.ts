// Fixture data for src/renderer/dev/preview.tsx: a project's worth of agents,
// events and questions, deliberately carrying the content that breaks narrow
// fixed-width columns — a long absolute path, an unbroken URL, a long branch
// name, a stack trace, a code block, an unbroken error string, a compressed
// JSON payload. Kept separate from preview.tsx so a future scenario (a new
// component, a new kind of wide content) can reuse it without copying it.
import type { QueryClient } from '@tanstack/react-query';
import type { HostSetupStatus, VMMigration, VMPower, VMPowerAction, VMPowerState } from '../../preload';
import type * as T from '../../shared/api';
import type { FreeRun } from '../components/ResourceControls';
import { freeTargets } from '../lib/freeResources';

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
  longLogin: 'a-github-login-long-enough-to-be-a-problem-for-a-narrow-pull-request-row',
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
      // A settled turn keeps what it said in view, folding only its work.
      item(3, { kind: 'assistant', turn: 'c7', text: 'Creating agent-13 for the Mac build, signed ad hoc.' }),
      item(2, { kind: 'tool', turn: 'c7', tool: { callId: 'create', title: 'create_agent', kind: 'other', status: 'completed' } }),
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
    agent({ ref: `${PROJECT}/agent-12`, title: 'Add a "New project" button to the Sidebar', branch: 'agentbox/agent-12', chat: 'running' }),
    agent({ ref: `${PROJECT}/agent-94`, title: 'Needs a GitHub account', chat: 'waiting' }),
    agent({ ref: `${PROJECT}/agent-95`, title: 'Needs a secret', chat: 'waiting' }),
    // An agent in each of the avatars' moods (avatarMood), across the three
    // AI tools: working, asking (the escalated questions below), idle,
    // stopped and broken.
    agent({ ref: `${PROJECT}/agent-96`, title: 'Fix the agent rail overflowing on wide text', ai: 'codex', chat: 'running' }),
    agent({ ref: `${PROJECT}/agent-97`, title: 'Long path agent', ai: 'opencode', chat: 'ready' }),
    agent({ ref: `${PROJECT}/agent-98`, title: 'Question agent', ai: 'codex', chat: 'waiting' }),
    agent({ ref: `${PROJECT}/agent-99`, title: 'PR agent', chat: 'running' }),
    agent({ ref: `${PROJECT}/agent-92`, title: 'Lost its machine', ai: 'claude', state: 'incomplete' }),
    agent({ ref: `${PROJECT}/agent-89`, title: 'Still being created', ai: 'claude', state: 'initializing' }),
    // Its turn still running, but the stall watch found no progress on it for
    // 22 minutes; and one whose AI tool exited mid-turn (exit status 143).
    agent({ ref: `${PROJECT}/agent-88`, title: 'Import the product catalogue', chat: 'running', stalledSince: new Date(Date.now() - 22 * 60_000).toISOString() }),
    agent({ ref: `${PROJECT}/agent-87`, title: 'Migrate the reminders table', ai: 'codex', chat: 'error' }),
    // A name given to create by hand, as long as the title beside it: the
    // row's name must give way before it pushes the title out.
    agent({
      ref: `${PROJECT}/fix-the-context-budget-warning-that-never-clears`,
      title: 'Fix the context budget warning that never clears after consolidation runs',
      chat: 'running',
    }),
    // Done with, one way or another: the rail's Finished section, with agent-97
    // above, which finished and sits idle.
    agent({ ref: `${PROJECT}/agent-93`, title: 'Stopped for the night', ai: 'opencode', state: 'stopped' }),
    agent({ ref: `${PROJECT}/agent-91`, title: 'Rename the settings keys', state: 'stopped' }),
    agent({ ref: `${PROJECT}/agent-90`, title: 'Bump Electron to the next major, and every native module that breaks with it', state: 'paused' }),
  ];

  const events: T.AgentEvent[] = [
    // Stopped for the night, freeing its Docker space on the way (Overview's State row).
    {
      id: 'e0',
      project: PROJECT,
      agent: 'agent-93',
      ref: `${PROJECT}/agent-93`,
      kind: 'docker_pruned',
      summary: 'Freed 22.6 GiB of Docker images and build cache',
      at: new Date(Date.now() - 8 * 3600_000).toISOString(),
    },
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
        checks: 'failing',
        conflict: true,
        review: 'changes_requested',
        watched: true,
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
  // newest first as the daemon returns them.
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
      pr: a.ref === `${PROJECT}/agent-99` ? { number: 78, title: 'PR', state: 'open', checks: 'failing', conflict: true, review: 'changes_requested', watched: true, url: WIDE.longUrl } : undefined,
      retire: { safe: false },
    })),
    creating: [],
    idle: 0,
  };

  const projects: T.Project[] = [
    {
      name: PROJECT,
      displayName: PROJECT,
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
      finishNotices: 'lead',
      rolloverThreshold: 0,
      contextBudget: 0,
      consolidation: 0,
      consolidationModel: '',
      section: 's1',
      position: 0,
      nesting: false,
      agentPRs: false,
      syncBase: true,
      prWatch: '',
      prWatching: true,
      slots: 0,
      alwaysQueue: false,
      agentSize: '',
      createdAt: new Date().toISOString(),
    },
    {
      name: 'a-project-with-a-name-so-long-it-should-never-be-allowed-to-widen-the-sidebar',
      displayName: 'A project with a name so long, and in Ünïcode, it should never be allowed to widen the sidebar',
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
      finishNotices: 'lead',
      rolloverThreshold: 0,
      contextBudget: 0,
      consolidation: 0,
      consolidationModel: '',
      section: '',
      position: 1,
      nesting: false,
      agentPRs: false,
      syncBase: true,
      prWatch: '',
      prWatching: true,
      slots: 0,
      alwaysQueue: false,
      agentSize: '',
      createdAt: new Date().toISOString(),
    },
  ];

  const sections: T.Section[] = [
    { id: 's1', name: 'A section name that is also much too long for this narrow column of pixels', position: 0, collapsed: false, createdAt: new Date().toISOString() },
  ];

  return { agents, events, questions, fleet, projects, sections };
}

// mediaFiles is what the dev bridge's mediaUrl serves for each fixture item:
// a drawn thumbnail for a screenshot, the text of a log. Filled by
// mediaItems, read by installDevBridge.
const mediaFiles = new Map<string, string>();

// setMediaFile serves url as item id's file, for fixtures made outside this file.
// setNotifications gives the dev bridge a bell history and the all-projects
// media list to answer with, and marks them seen the way the daemon does.
export function setNotifications(notifications: T.Notification[], allMedia: T.MediaItem[]): void {
  devState.notifications = notifications;
  devState.allMedia = allMedia;
}

function seeNotifications(req: T.SeeNotificationsRequest): T.SeeNotificationsResult {
  let seen = 0;
  const media = new Set(req.media ?? []);
  devState.notifications = devState.notifications?.map((n) => {
    if (n.seen || !(req.all || req.ids?.includes(n.id) || (n.media && media.has(n.media.id)))) return n;
    seen++;
    if (n.media) media.add(n.media.id);
    return { ...n, seen: true };
  });
  devState.allMedia = devState.allMedia?.map((m) => (m.unseen && (req.all || media.has(m.id)) ? { ...m, unseen: false } : m));
  return { seen };
}

export function setMediaFile(id: string, url: string): void {
  mediaFiles.set(id, url);
}

// mediaItems is a project's Media: hundreds of items across four agents and
// every kind, so the gallery and its search can be checked at the size a busy
// project reaches, with names, file names and notes long and unbroken enough
// to push a card or the toolbar wider than its column.
export function mediaItems(count = 360): T.MediaItem[] {
  const agents = [
    { name: 'agent-12', title: 'Add a "New project" button to the Sidebar' },
    { name: 'agent-96', title: 'Fix the agent rail overflowing on wide text' },
    { name: 'agent-97', title: 'Long path agent' },
    { name: 'agent-99', title: 'PR agent' },
    ...Array.from({ length: 10 }, (_, n) => ({ name: `agent-${100 + n}`, title: `Fixture agent number ${n + 1}` })),
  ];
  const kinds = ['screenshot', 'recording', 'note', 'report', 'log', 'file', 'screenshot', 'note'];
  const topics = ['checkout', 'sidebar-overflow', 'login-flow', 'settings-agents-defaults', 'media-gallery', 'release-dry-run', 'rail-folded', 'pull-requests'];
  const notes = [
    'The sidebar no longer grows past 312px with a long branch name. Checked against dev/fixtures.ts.',
    'Retried the flaky lead-wake test 200 times: all green. The race was in the chat goroutine that outlived its turn.',
    'Stack trace before the fix: TypeError: Cannot read properties of undefined (reading "agentName") at MediaCard (MediaTab.tsx:444)',
    'See https://github.com/leciric/agentbox/pull/82/files#diff-a-very-long-anchor-that-never-breaks-anywhere-at-all for the overflow.',
    'Coverage went from 61.2% to 63.8%; the floor in .github/coverage-floor.txt is bumped to match.',
  ];
  const colors = ['#6d5dfc', '#0ea5e9', '#f59e0b', '#10b981', '#f43f5e'];
  const now = Date.now();
  const items: T.MediaItem[] = [];
  for (let i = 0; i < count; i++) {
    const kind = kinds[i % kinds.length];
    // Each run of kinds goes to one agent, so every agent has every kind,
    // and topics cycle on their own period, so each kind has every topic.
    const a = agents[Math.floor(i / kinds.length) % agents.length];
    const topic = topics[Math.floor(i / 3) % topics.length];
    const id = `m${String(i).padStart(4, '0')}`;
    const long = i % 11 === 0 ? '-with-a-name-far-too-long-for-any-card-in-the-gallery-to-show-whole' : '';
    const ext = { screenshot: 'png', recording: 'mp4', report: 'html', log: 'log', file: 'zip' }[kind as 'screenshot'];
    const mime = { screenshot: 'image/png', recording: 'video/mp4', report: 'text/html', log: 'text/plain', file: 'application/zip' }[kind as 'screenshot'];
    const item: T.MediaItem = {
      id,
      agent: `${PROJECT}/${a.name}`,
      agentName: a.name,
      agentTitle: a.title,
      kind,
      name: `${topic}${long} ${i}`,
      file: kind === 'note' ? undefined : `${topic}${long}-${i}.${ext}`,
      mime: kind === 'note' ? undefined : mime,
      size: kind === 'note' ? 0 : 20_000 + ((i * 7919) % 2_000_000),
      source: i % 5 === 0 ? 'user' : 'agent',
      text: kind === 'note' ? notes[i % notes.length] : undefined,
      meta: kind === 'recording' ? { duration: 12 + (i % 50) } : kind === 'report' ? { tests: { passed: 40 + (i % 9), failed: i % 3, skipped: 1 } } : {},
      createdAt: new Date(now - i * 7 * 60_000).toISOString(),
    };
    if (kind === 'screenshot') {
      const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="200"><rect width="320" height="200" fill="${colors[i % colors.length]}" opacity="0.35"/><rect x="16" y="16" width="120" height="168" rx="8" fill="#fff" opacity="0.15"/><text x="152" y="104" font-family="sans-serif" font-size="18" fill="#fff">${topic}</text></svg>`;
      mediaFiles.set(id, `data:image/svg+xml,${encodeURIComponent(svg)}`);
    } else if (kind === 'log') {
      mediaFiles.set(id, `data:text/plain,${encodeURIComponent(`$ go test ./internal/${topic}\nok  \tgithub.com/leciric/agentbox/internal/${topic}\t0.412s\n`)}`);
    }
    items.push(item);
  }
  return items;
}

// seedMedia gives the project, and agent-99, the gallery mediaItems makes.
export function seedMedia(queryClient: QueryClient): void {
  devState.media = mediaItems();
  queryClient.setQueryData(['projectMedia', PROJECT], devState.media);
  queryClient.setQueryData(['media', `${PROJECT}/agent-99`], devState.media.filter((m) => m.agentName === 'agent-99'));
}

// seedAllMedia gives the all-projects Media view the same gallery's
// screenshots and recordings, with every third item moved to a second project.
export function seedAllMedia(queryClient: QueryClient): void {
  const all = mediaItems(720)
    .filter((m) => m.kind === 'screenshot' || m.kind === 'recording')
    .map((m, i) => (i % 3 === 0 ? { ...m, agent: `organic/${m.agentName}` } : m));
  setNotifications([], all);
  queryClient.setQueryData(['allMedia', ['screenshot', 'recording']], all);
}

// pullRequests is a project's pull requests list, with a long GitHub login to
// check the author column doesn't widen the row (#pull-request-author).
export function pullRequests(): T.ProjectPullRequests {
  return {
    project: PROJECT,
    github: 'leciric/agentbox',
    githubAccount: 'default',
    fetchedAt: new Date().toISOString(),
    canMerge: true,
    canMergeKnown: true,
    mergeMethods: ['merge', 'squash', 'rebase'],
    pullRequests: [
      {
        number: 78,
        title: 'A pull request whose title is genuinely long enough to be a problem for a narrow column',
        state: 'open',
        checks: 'failing',
        conflict: true,
        review: 'changes_requested',
        watched: true,
        url: WIDE.longUrl,
        additions: 120,
        deletions: 34,
        comments: 3,
        updatedAt: new Date().toISOString(),
        baseBranch: 'main',
        headBranch: WIDE.branchName,
        author: WIDE.longLogin,
        authorAvatar: 'https://avatars.githubusercontent.com/u/1?v=4',
        agent: 'agent-99',
      },
      {
        number: 65,
        title: 'Bump a dependency',
        state: 'merged',
        checks: 'passing',
        url: 'https://github.com/leciric/agentbox/pull/65',
        additions: 4,
        deletions: 4,
        comments: 0,
        updatedAt: new Date(Date.now() - 86_400_000).toISOString(),
        baseBranch: 'main',
        headBranch: 'deps/bump-something',
        author: 'someone',
      },
    ],
  };
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

// agent99Chat is agent-99's session as its info card reads it (model, effort,
// context): nothing it says, since only session.options is read there.
export function agent99Chat(): T.ChatThread {
  return {
    agent: `${PROJECT}/agent-99`,
    seq: 1,
    session: {
      state: 'working',
      tool: 'claude',
      options: [
        { id: 'model', name: 'Model', category: 'model', type: 'select', value: 'claude-sonnet-5', choices: [{ value: 'claude-sonnet-5', name: 'Sonnet' }] },
        { id: 'effort', name: 'Effort', category: 'thought_level', type: 'select', value: 'high', choices: [{ value: 'high', name: 'High' }] },
      ],
      commands: [],
      contextUsed: 84_000,
      contextSize: 200_000,
    },
    items: [],
  };
}

// figures is the four token counters a report, an agent or a model carries,
// plus their total and the AI tool's cost estimate — every ledger fixture
// below is built from these, with its own avgTPS beside them (T.TokenCounts
// itself has no avgTPS: each container that embeds it adds its own).
function figures(input: number, output: number, cacheRead: number, cacheWrite: number, costUSD: number) {
  return { input, output, cacheRead, cacheWrite, total: input + output + cacheRead + cacheWrite, costUSD };
}

// sumModels adds a set of models' figures together, for the agent (or
// report) row that carries all of them: cost and tokens add up; avgTPS is
// weighted by each model's own output, the way summing output-over-seconds
// and only then dividing would come out.
function sumModels(models: (T.ModelTokens | T.AgentTokens)[]) {
  const totals = models.reduce(
    (sum, m) => ({ input: sum.input + m.input, output: sum.output + m.output, cacheRead: sum.cacheRead + m.cacheRead, cacheWrite: sum.cacheWrite + m.cacheWrite, costUSD: sum.costUSD + m.costUSD }),
    { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, costUSD: 0 },
  );
  const outputSum = models.reduce((sum, m) => sum + m.output, 0) || 1;
  const avgTPS = models.reduce((sum, m) => sum + (m.avgTPS ?? 0) * m.output, 0) / outputSum;
  return { ...figures(totals.input, totals.output, totals.cacheRead, totals.cacheWrite, totals.costUSD), avgTPS };
}

// agent99Models and agent04Models are what each agent spent, split by model:
// a subagent on a cheaper model shows up as its own line (Claude Code bills it
// to the turn that started it), each with its own average tokens per second.
const agent99Models: T.ModelTokens[] = [
  { model: 'claude-sonnet-5', avgTPS: 68.2, ...figures(40_000, 180_000, 3_200_000, 120_000, 11.4) },
  { model: 'claude-haiku-4-5', avgTPS: 142.1, ...figures(5_000, 20_000, 150_000, 8_000, 0.4) },
];
const agent04Models: T.ModelTokens[] = [{ model: 'claude-opus-5', avgTPS: 31.5, ...figures(22_000, 60_000, 900_000, 30_000, 6.9) }];

// agentTokens builds one agent's row of a report from its models, the way
// the daemon sums the ledger's rows into internal/api.AgentTokens.
function agentTokens(agent: string, title: string, turns: number, maxContext: number, lastAgo: number, models: T.ModelTokens[]): T.AgentTokens {
  return { project: PROJECT, agent, ref: `${PROJECT}/${agent}`, ai: 'claude', title, exists: true, turns, maxContext, lastAt: new Date(Date.now() - lastAgo).toISOString(), ...sumModels(models), models };
}

// agent99Tokens is agent-99's line of the ledger alone, for its info card's
// average TPS and spend so far (?open=agent-99, its Settings → AI tool).
export function agent99Tokens(): T.TokenReport {
  const agent = agentTokens('agent-99', 'PR agent', 42, 3_540_000, 90_000, agent99Models);
  return { until: new Date().toISOString(), ...sumModels([agent]), agents: [agent], buckets: [], bucketSeconds: 3600 };
}

// tokenBuckets makes n buckets, step seconds apart ending now, each ramping
// output up and cost with it, and a slower turn or two (lower avgTPS) midway —
// enough shape for the spend-over-time chart's bars and tooltips to differ,
// without claiming to be a real trace.
function tokenBuckets(n: number, step: number): T.TokenBucket[] {
  // slots() (TokensPanel.tsx) looks a bucket up by its start floored to a
  // step-sized boundary since the epoch, the way the daemon's own bucketing
  // does it (at / w) * w: a start off that grid, however close, is one
  // slots() never finds, and the chart draws every column empty.
  const stepMs = step * 1000;
  const lastStart = Math.floor(Date.now() / stepMs) * stepMs;
  return Array.from({ length: n }, (_, i) => {
    const output = 6_000 + i * 2_400 + (i % 3 === 0 ? 3_000 : 0);
    const total = Math.round(output * 4.6);
    const avgTPS = i === 3 || i === 4 ? 28 : 55 + i * 4;
    return { start: new Date(lastStart - (n - 1 - i) * stepMs).toISOString(), avgTPS, ...figures(Math.round(total * 0.02), output, Math.round(total * 0.87), Math.round(total * 0.08), total * 0.0000038) };
  });
}

// projectTokenReport is the project's whole ledger (?tokens=1, the project's
// Settings → Tokens): two agents on different models, each with its own average
// TPS, and buckets across a five-hour window for the chart.
export function projectTokenReport(): T.TokenReport {
  const agents = [agentTokens('agent-99', 'PR agent', 42, 3_540_000, 90_000, agent99Models), agentTokens('agent-04', 'Fix the agent rail overflowing on wide content', 18, 1_012_000, 40 * 60_000, agent04Models)];
  const buckets = tokenBuckets(8, 2_250); // 8 columns, 37.5 minutes apart: a five-hour window
  const since = new Date(Date.now() - 5 * 3_600_000).toISOString();
  return { until: new Date().toISOString(), since, ...sumModels(agents), agents, buckets, bucketSeconds: 2_250 };
}

// agent99Turns is agent-99's ledger itself, newest first, for the recent-turns
// table under its info's "By model": a mix of turns and one background
// result (no duration, so no TPS of its own).
function agent99Turns(): T.TokenTurn[] {
  const at = (ago: number) => new Date(Date.now() - ago).toISOString();
  const turn = (ago: number, model: string, output: number, generationMS: number, context: number): T.TokenTurn => ({
    project: PROJECT,
    agent: 'agent-99',
    ai: 'claude',
    kind: 'turn',
    model,
    at: at(ago),
    context,
    generationMS,
    ...figures(4_000, output, 380_000, 12_000, output * 0.00002),
  });
  return [
    turn(2 * 60_000, 'claude-sonnet-5', 6_200, 88_000, 950_000),
    turn(9 * 60_000, 'claude-haiku-4-5', 3_100, 22_000, 940_000),
    { ...turn(18 * 60_000, 'claude-sonnet-5', 0, 0, 900_000), kind: 'background', output: 0, input: 0, cacheRead: 0, cacheWrite: 0, total: 0, generationMS: 0 },
    turn(34 * 60_000, 'claude-sonnet-5', 5_400, 79_000, 880_000),
  ];
}

// devState is what the dev bridge serves for the projects and the stored
// accounts, and what picking a project's GitHub account or renaming one
// changes, so a scenario that refetches them after a change (?github=1) sees
// what the daemon would have answered.
const devState: {
  projects: T.Project[];
  media?: T.MediaItem[];
  // The ?notify= scenarios' bell history and all-projects media (notifications.tsx).
  notifications?: T.Notification[];
  allMedia?: T.MediaItem[];
  auth?: T.AuthStatus;
  jobLog?: string;
  job?: T.Job;
  setup?: T.SetupStatus;
  cli?: unknown;
  memoryUsage?: T.MemoryUsage;
  cpuUsage?: T.CPUUsage;
  diskUsage?: T.DiskUsage;
  homeDisk?: T.VMHomeDisk;
  agents?: T.Agent[];
  vmPower?: VMPower | null;
  hostSetup?: HostSetupStatus;
  migration?: VMMigration;
  update?: T.UpdateStatus;
  appVersion?: string;
} = { projects: [] };

// seedQueryClient primes every query AgentRail and Sidebar read, at
// staleTime: Infinity (set by the caller's QueryClient), so nothing refetches
// through the stub bridge below.
export function seedQueryClient(queryClient: QueryClient, data: FixtureData): void {
  devState.projects = structuredClone(data.projects);
  queryClient.setQueryData(['agents'], data.agents);
  devState.agents = data.agents;
  queryClient.setQueryData(['usage'], { host: { cpu: 0, cores: 1, memUsed: 0, memTotal: 0, poolUsed: 0, poolTotal: 0, diskRead: 0, diskWrite: 0 }, agents: [] });
  queryClient.setQueryData(['fleet', PROJECT], data.fleet);
  queryClient.setQueryData(['agentEvents', PROJECT], data.events);
  queryClient.setQueryData(['questions', PROJECT], data.questions);
  queryClient.setQueryData(['chat', `${PROJECT}/lead`], leadChat());
  queryClient.setQueryData(['chat', `${PROJECT}/agent-99`], agent99Chat());
  queryClient.setQueryData(['tokens', PROJECT, 'agent-99', 'all'], agent99Tokens());
  // Every agent's disk, as the info card asks for it: the machine's root disk
  // and the worktree, apart from agent-92, whose machine is gone, so Incus has
  // no volume to measure.
  data.agents.forEach((a, i) => {
    const machine = a.ref === `${PROJECT}/agent-92` ? undefined : (3.2 + i * 0.7) * 1024 ** 3;
    queryClient.setQueryData(['agentDisk', a.ref], { machine, worktree: (180 + i * 37) * 1024 ** 2, measuredAt: new Date().toISOString() } satisfies T.AgentDisk);
  });
  const agent99Recent = agentTokens('agent-99', 'PR agent', 6, 3_540_000, 90_000, [{ model: 'claude-sonnet-5', avgTPS: 71.8, ...figures(6_000, 26_000, 480_000, 18_000, 1.6) }]);
  queryClient.setQueryData(['tokens', PROJECT, 'agent-99', '5h'], { until: new Date().toISOString(), since: new Date(Date.now() - 5 * 3_600_000).toISOString(), ...sumModels([agent99Recent]), agents: [agent99Recent], buckets: [], bucketSeconds: 600 } satisfies T.TokenReport);
  queryClient.setQueryData(['tokens', PROJECT, '5h'], projectTokenReport());
  queryClient.setQueryData(['tokenTurns', PROJECT, 'agent-99', '', 15], agent99Turns());
  queryClient.setQueryData(['projectChat', PROJECT], { project: PROJECT, ref: `${PROJECT}/lead`, started: true, chat: 'running' } satisfies T.ProjectChat);
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

// What each running agent holds and uses, for the stop-agents job's result:
// a made-up figure per agent, largest for the ones working.
function agentFootprint(a: T.Agent, i: number): { memory: number; cpu: number } {
  const GiB = 1024 ** 3;
  if (a.state === 'paused') return { memory: 5.5 * GiB, cpu: 0 };
  return a.chat === 'running' ? { memory: (2.4 + (i % 3) * 0.9) * GiB, cpu: 60 + (i % 4) * 35 } : { memory: (0.9 + (i % 2) * 0.4) * GiB, cpu: 3 };
}

// stopAgentsResult is what the daemon's stop-agents job would answer for
// refs, out of agents as they were before it.
export function stopAgentsResult(agents: T.Agent[], refs: string[]): T.StopAgentsResult {
  const GiB = 1024 ** 3;
  const stopped = agents
    .map((a, i) => ({ a, ...agentFootprint(a, i) }))
    .filter(({ a }) => refs.includes(a.ref))
    .map(({ a, memory, cpu }): T.StoppedAgent => ({ ref: a.ref, title: a.title, memory, cpu, working: a.chat === 'running' }))
    .sort((x, y) => y.memory - x.memory);
  const freedMemory = stopped.reduce((n, a) => n + a.memory, 0);
  const freedCPU = stopped.reduce((n, a) => n + a.cpu, 0);
  return { stopped, freedMemory, freedCPU, hostMemoryBefore: 9 * GiB + freedMemory, hostMemoryAfter: 9.4 * GiB };
}

// stopAgents plays the daemon's stop-agents job: a job whose agents stop one
// after another, then its result, so the preview's Free resources runs
// start to finish against it.
function stopAgents(refs: string[]) {
  const before = devState.agents ?? [];
  const job: T.Job = { id: 'stop-agents-1', kind: 'stop-agents', target: `${refs.length} agents`, status: 'running', createdAt: new Date().toISOString() };
  devState.job = job;
  devState.jobLog = '';
  refs.forEach((ref, i) => {
    setTimeout(() => {
      devState.agents = (devState.agents ?? []).map((a) => (a.ref === ref ? { ...a, state: 'stopped', chat: 'off' } : a));
      devState.jobLog += `Stopped ${ref}\n`;
    }, 600 * (i + 1));
  });
  setTimeout(
    () => {
      devState.job = { ...job, status: 'succeeded', result: stopAgentsResult(before, refs), finishedAt: new Date().toISOString() };
    },
    600 * (refs.length + 1),
  );
  return { status: 202, body: JSON.stringify(job), contentType: 'application/json' };
}

// seedPower is the top bar's resource controls (?power=): in host mode with
// agents running (host) or with every one stopped by Free resources and
// Start to bring them back (host-start); or in VM mode with the VM in a
// state: running, paused, off, starting or stopping.
export function seedPower(queryClient: QueryClient, power: string): void {
  const GiB = 1024 ** 3;
  queryClient.setQueryData(['usage'], { host: { cpu: 38, cores: 16, memUsed: 21 * GiB, memTotal: 32 * GiB, poolUsed: 0, poolTotal: 0, diskRead: 0, diskWrite: 0 }, agents: [] });
  queryClient.setQueryData(['claudeLimits'], []);
  localStorage.removeItem('agentbox.freed');
  if (power === 'host') {
    devState.vmPower = null;
    return;
  }
  if (power === 'host-start') {
    devState.vmPower = null;
    const agents = (devState.agents ?? []).map((a) => (a.state === 'running' || a.state === 'paused' ? { ...a, state: 'stopped', chat: 'off' } : a));
    localStorage.setItem('agentbox.freed', JSON.stringify(freeTargets(devState.agents ?? []).map((t) => t.ref)));
    devState.agents = agents;
    queryClient.setQueryData(['agents'], agents);
    return;
  }
  const state = power as VMPowerState;
  const up = state !== 'off' && state !== 'starting';
  devState.vmPower = { state, memoryUsed: up ? 11.3 * GiB : 0, memoryGranted: up ? 16 * GiB : 0, memoryCap: 24 * GiB, cpus: 12 };
  if (state === 'off') {
    const agents = (devState.agents ?? []).map((a) => (a.state === 'running' || a.state === 'paused' ? { ...a, state: 'stopped', chat: 'off' } : a));
    localStorage.setItem('agentbox.freed', JSON.stringify(freeTargets(devState.agents ?? []).map((t) => t.ref)));
    devState.agents = agents;
    queryClient.setQueryData(['agents'], agents);
  }
  queryClient.setQueryData(['vmPower'], devState.vmPower);
}

// freeRun is a Free resources in one of its phases (?free=), against the
// fixtures' agents: part-way through stopping them, or done — in VM mode
// once the VM is off too.
export function freeRun(queryClient: QueryClient, phase: string): FreeRun {
  const agents = devState.agents ?? [];
  const targets = freeTargets(agents);
  const vmBefore = devState.vmPower ?? null;
  if (phase === 'progress') {
    const done = new Set(targets.slice(0, 5).map((t) => t.ref));
    devState.agents = agents.map((a) => (done.has(a.ref) ? { ...a, state: 'stopped' } : a));
    queryClient.setQueryData(['agents'], devState.agents);
    return { phase: 'stopping', targets, vmBefore };
  }
  if (phase === 'error') return { phase: 'error', targets, vmBefore, error: "Couldn't reach the daemon: connect ENOENT /home/you/.local/share/agentbox/run/agentbox.sock" };
  const result = stopAgentsResult(
    agents,
    targets.map((t) => t.ref),
  );
  if (phase === 'partial') {
    const [failed, ...rest] = result.stopped;
    return {
      phase: 'done',
      targets,
      vmBefore: null,
      result: {
        ...result,
        stopped: rest,
        freedMemory: result.freedMemory - failed.memory,
        failed: [{ ref: failed.ref, title: failed.title, error: 'Failed to stop instance: the instance is busy (operation 4f1c… is still running)' }],
      },
    };
  }
  return { phase: 'done', targets, vmBefore, result, vmStopped: !!vmBefore };
}

// fakeVM prints what `agentbox vm resize` prints, a line at a time, so the
// preview shows a resize in progress and after.
function fakeVM() {
  const listeners = new Set<(text: string) => void>();
  const swapListeners = new Set<(text: string) => void>();
  return {
    onSwapOutput: (fn: (text: string) => void) => {
      swapListeners.add(fn);
      return () => swapListeners.delete(fn);
    },
    onOutput: (fn: (text: string) => void) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    resize: async (cpus: number, memory: string, restart?: boolean, disk?: string) => {
      // On Linux (?chv=), the Cloud Hypervisor VM changes while it runs.
      const chv = devState.hostSetup?.chv;
      if (chv) {
        const GiB = 1024 ** 3;
        const pool = disk ? parseFloat(disk) * GiB : chv.disk.pool.size;
        const size = `${cpus} CPUs, a memory cap of ${memory} and a ${pool / GiB}GiB disk`;
        const flags = `--cpus ${cpus} --memory-cap ${memory}${disk ? ` --disk ${disk}` : ''}`;
        const lines = restart
          ? [
              `$ agentbox vm resize ${flags} --restart\n`,
              `==> Restarting AgentBox's VM to give it ${size}: every agent in it stops\n`,
              "AgentBox's VM is off (1.6s).\n",
              "AgentBox's VM is up (6.3s).\n",
              '==> Starting the daemon\n',
              `AgentBox's VM has ${size} now.\n`,
            ]
          : [
              `$ agentbox vm resize ${flags}\n`,
              `==> Giving AgentBox's VM ${size}, while it runs\n`,
              '==> Starting the daemon\n',
              `AgentBox's VM has ${size} now, and every agent kept running.\n`,
            ];
        for (const line of lines) {
          await new Promise((resolve) => setTimeout(resolve, 350));
          for (const fn of listeners) fn(line);
        }
        const room = { minCpus: 1, maxCpus: 16, minMemory: 4 * GiB, maxMemory: 32 * GiB, minDisk: pool, maxDisk: 16384 * GiB };
        devState.hostSetup = {
          ...devState.hostSetup!,
          chv: {
            ...chv,
            cpus,
            memory: { ...chv.memory, cap: parseFloat(memory) * GiB },
            disk: {
              ...chv.disk,
              pool: { ...chv.disk.pool, size: pool },
              size: chv.disk.size - chv.disk.pool.size + pool,
            },
            limits: { ...chv.limits!, minDisk: pool },
            live: room,
          },
        };
        return;
      }
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
    // `agentbox vm swap`, against ?chv='s VM: made in a moment, while it runs.
    swap: async (size: string | null) => {
      const chv = devState.hostSetup?.chv;
      if (!chv) throw new Error('AgentBox is not in VM mode');
      if (chv.state !== 'running') throw new Error("AgentBox's VM is off: start it (agentbox vm start), then change its swap");
      const GiB = 1024 ** 3;
      const bytes = size === null ? 0 : parseFloat(size) * GiB;
      // The VM's 120 GiB disk has 40 GiB free and keeps 10 GiB of it free.
      if (bytes > 30 * GiB)
        throw new Error(
          `a ${size} swapfile would leave ${Math.max(40 - bytes / GiB, 0).toFixed(1)} GiB free on the VM's disk, under the 10.0 GiB it keeps free (the disk floor, in Settings): make it at most 30GiB`,
        );
      const lines =
        size === null
          ? ['$ agentbox vm swap off\n', "==> Turning AgentBox's VM's swap off\n", '    (took 0.4s)\n', "AgentBox's VM has no swap now.\n"]
          : [
              `$ agentbox vm swap on --size ${size}\n`,
              `==> Making a ${size} swapfile in AgentBox's VM\n`,
              '    (took 1.2s)\n',
              `AgentBox's VM has ${size} of swap now, a swapfile on its own disk that it keeps across restarts.\n`,
            ];
      for (const line of lines) {
        await new Promise((resolve) => setTimeout(resolve, 300));
        for (const fn of swapListeners) fn(line);
      }
      devState.hostSetup = { ...devState.hostSetup!, chv: { ...chv, swap: { size: bytes, total: bytes, used: 0 } } };
    },
    // The ?power= scenarios' VM (seedPower): each action takes a moment in
    // its transition, the way `agentbox vm start` and friends do.
    power: async () => devState.vmPower ?? null,
    disk: async () => (devState.vmPower ? (devState.homeDisk ?? null) : null),
    act: async (action: VMPowerAction) => {
      const vm = devState.vmPower;
      if (!vm) throw new Error('AgentBox is not in VM mode');
      const transition = { start: 'starting', pause: 'pausing', resume: 'resuming', stop: 'stopping' } as const;
      devState.vmPower = { ...vm, state: transition[action] };
      await new Promise((resolve) => setTimeout(resolve, action === 'pause' || action === 'resume' ? 700 : 1800));
      const GiB = 1024 ** 3;
      devState.vmPower =
        action === 'stop'
          ? { ...vm, state: 'off', memoryUsed: 0, memoryGranted: 0 }
          : action === 'pause'
            ? { ...vm, state: 'paused' }
            : { ...vm, state: 'running', memoryGranted: vm.memoryGranted || 8 * GiB, memoryUsed: vm.memoryUsed || 3.1 * GiB };
      return devState.vmPower;
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
  enforceAgentDefaults: false,
  defaultLeadModel: '',
  defaultLeadContextWindow: '',
  language: new URLSearchParams(location.search).get('lang') ?? 'en-US',
  mediaRetention: '1d',
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
  resumeAfterLimit: true,
  continueAfterRestart: true,
  claudeCompactWindow: 200_000,
  updateCheck: true,
  usageStats: true,
  errorReports: false,
  errorReportsAsked: false,
  prWatch: true,
  defaultClaudeCompactWindow: 200_000,
  diskFloorMin: 10 * 1024 ** 3,
  diskFloorPercent: 5,
  autoStopIdle: false,
  dockerPruneOnStop: true,
  idleTimeSeconds: 2 * 60 * 60,
  agentQueue: false,
  taskTarget: 'agent',
  leadRecheck: false,
  leadRecheckMinutes: 20,
  imageCache: true,
  imageCacheMaxBytes: 20 * 1024 ** 3,
  defaultImageCacheMaxBytes: 20 * 1024 ** 3,
  imageCacheBytes: 7.4 * 1024 ** 3,
  packageCache: true,
  packageCacheMaxBytes: 20 * 1024 ** 3,
  defaultPackageCacheMaxBytes: 20 * 1024 ** 3,
  packageCacheBytes: 3.1 * 1024 ** 3,
} as T.Settings;

function patchDefaults(req: T.UpdateSettingsRequest): { status: number; body: string; contentType: string } {
  const next = { ...defaultsSettings };
  const window = (v: string) => (/^1m$|^1000000$/i.test(v) ? '1000000' : '');
  if (req.defaultClaudeModel !== undefined) next.defaultClaudeModel = req.defaultClaudeModel;
  if (req.defaultLeadModel !== undefined) next.defaultLeadModel = req.defaultLeadModel;
  if (req.defaultAgentContextWindow !== undefined) next.defaultAgentContextWindow = window(req.defaultAgentContextWindow);
  if (req.enforceAgentDefaults !== undefined) next.enforceAgentDefaults = req.enforceAgentDefaults;
  if (req.language !== undefined) next.language = req.language || 'en-US';
  if (req.defaultLeadContextWindow !== undefined) next.defaultLeadContextWindow = window(req.defaultLeadContextWindow);
  if (req.diskFloorMin !== undefined) next.diskFloorMin = req.diskFloorMin || 10 * 1024 ** 3;
  if (req.diskFloorPercent !== undefined) next.diskFloorPercent = req.diskFloorPercent < 0 ? 5 : req.diskFloorPercent;
  if (req.autoStopIdle !== undefined) next.autoStopIdle = req.autoStopIdle;
  if (req.idleTimeSeconds !== undefined) next.idleTimeSeconds = req.idleTimeSeconds;
  if (req.imageCache !== undefined) next.imageCache = req.imageCache;
  if (req.imageCacheMaxBytes !== undefined) next.imageCacheMaxBytes = req.imageCacheMaxBytes || next.defaultImageCacheMaxBytes;
  if (req.clearImageCache) next.imageCacheBytes = 0;
  if (req.packageCache !== undefined) next.packageCache = req.packageCache;
  if (req.packageCacheMaxBytes !== undefined) next.packageCacheMaxBytes = req.packageCacheMaxBytes || next.defaultPackageCacheMaxBytes;
  if (req.clearPackageCache) next.packageCacheBytes = 0;
  // The channel changes what the update check offers (seedNightly).
  if (req.updateChannel !== undefined && devState.update) devState.update = nightlyStatus(devState.update.current, req.updateChannel);
  // The rest are stored as they are sent, the way the daemon stores them.
  for (const key of [
    'defaultClaudeEffort',
    'resumeAfterLimit',
    'continueAfterRestart',
    'claudeCompactWindow',
    'updateCheck',
    'usageStats',
    'errorReports',
    'prWatch',
    'dockerPruneOnStop',
    'mediaRetention',
    'agentQueue',
    'taskTarget',
    'leadRecheck',
    'leadRecheckMinutes',
  ] as const) {
    if (req[key] !== undefined) (next as Record<string, unknown>)[key] = req[key];
  }
  if (req.errorReports !== undefined) next.errorReportsAsked = true;
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

// seedImageUpdate is Setup while the daemon updates the base image's agent
// tools in place (?setup=updating): every check ready but the image's, which
// is updating, with the job doing it and its log.
const imageToolsJob = 'job-image-tools';
const imageToolsLog = `==> Copying agentbox-base/ready to agentbox-base-next
==> Installing claude@2.1.280
mise claude@2.1.280 ✓ installed
==> Checking every tool
`;
export function seedImageUpdate(queryClient: QueryClient): void {
  const ok = (id: string, title: string, detail: string, required = true): T.SetupCheck => ({ id, title, detail, required, status: 'ok' });
  const none: T.ImageComponents = { android: false, codex: false, opencode: false, devCaches: false, incus: false };
  const setup = {
    ready: true,
    checks: [
      ok('incus', 'Incus', 'installed, and you can use it'),
      {
        id: 'image',
        title: 'Base image',
        required: true,
        status: 'updating',
        job: imageToolsJob,
        fix: 'agentbox image build',
        detail: "Updating agent tools… (installing claude@2.1.280). Agents keep using the current image until it's done",
      },
      ok('claude', 'Claude Code', 'signed in as default', false),
      ok('github', 'GitHub', 'signed in as leciric', false),
    ],
    image: { version: '2026.09.25.1', components: none, installed: none, downloads: [], hint: '' },
  } as T.SetupStatus;
  // Setup and the command-line tool's status are polled, so the bridge answers
  // with them too.
  devState.setup = setup;
  devState.cli = { linkPath: '~/.local/bin/agentbox', linked: true, path: '~/.local/bin/agentbox', version: 'preview', onPath: true, bundled: true, binary: null };
  queryClient.setQueryData(['setup'], setup);
  queryClient.setQueryData(['cli'], devState.cli);
  devState.job = {
    id: imageToolsJob,
    kind: 'image-tools',
    target: 'agentbox-base/ready',
    status: 'running',
    createdAt: new Date(Date.now() - 42_000).toISOString(),
  };
  devState.jobLog = imageToolsLog;
  queryClient.setQueryData(['job', imageToolsJob], devState.job);
}

// seedSettings is the whole Settings page on a machine that is set up
// (?settings=<section>): every check the daemon makes, most of them ready, one
// that wants a look and the optional ones skipped; two Claude Code accounts,
// the Omarchy theme followed, and the defaults above, which the dev bridge
// saves.
export function seedSettings(queryClient: QueryClient): void {
  const check = (id: string, title: string, status: string, detail: string, required = true, fix?: string): T.SetupCheck => ({ id, title, status, detail, required, fix });
  const none: T.ImageComponents = { android: false, codex: false, opencode: false, devCaches: false, incus: false };
  const setup = {
    ready: true,
    checks: [
      check('incus', 'Incus', 'ok', 'installed, and you can use it'),
      check('host', 'User mapping', 'ok', 'your files in agents\' worktrees are yours'),
      check('image', 'Base image', 'ok', 'agentbox-base 2026.09.25.1, with its agent tools up to date', true, 'agentbox image build'),
      check('storage', 'Storage', 'warn', 'dir: every new agent is a full copy of the base image (about 6 GB)', false),
      check('claude', 'Claude Code', 'ok', 'signed in as default', false),
      check('codex', 'Codex', 'optional', 'not signed in', false, 'agentbox auth codex'),
      check('opencode', 'OpenCode', 'optional', 'not in the base image', false, 'agentbox image build --opencode'),
      check('android', 'Android', 'optional', 'no Android SDK found', false),
      check('preview', 'Preview proxy', 'ok', 'http://<port>.<agent>.agentbox.localhost:7777', false),
    ],
    image: { version: '2026.09.25.1', components: { ...none, incus: true }, installed: { ...none, incus: true }, downloads: [], hint: '' },
  } as T.SetupStatus;
  devState.setup = setup;
  devState.cli = { linkPath: '~/.local/bin/agentbox', linked: true, path: '~/.local/bin/agentbox', version: 'preview', onPath: true, bundled: true, binary: null };
  devState.auth = {
    ...devState.auth!,
    claude: true,
    claudeAccounts: [
      { name: 'default', default: true, savedAt: '2026-08-01T10:00:00Z', valid: 'valid' },
      { name: 'work', default: false, savedAt: '2026-09-12T10:00:00Z' },
    ],
  };
  queryClient.setQueryData(['setup'], setup);
  queryClient.setQueryData(['cli'], devState.cli);
  queryClient.setQueryData(['auth'], devState.auth);
  queryClient.setQueryData(['settings'], defaultsSettings);
  queryClient.setQueryData(['update'], devState.update ?? ({ current: '0.7.0', enabled: true, channel: 'stable' } satisfies T.UpdateStatus));
  queryClient.setQueryData(['theme'], {
    appearance: 'follow',
    available: true,
    name: 'Tokyo Night',
    mode: 'dark',
    background: '#1a1b26',
    surface: '#24283b',
    foreground: '#c0caf5',
    muted: '#565f89',
    accent: '#7aa2f7',
  } satisfies T.Theme);
  queryClient.setQueryData(['host-setup'], {});
}

// seedLinuxHost is a Linux machine that runs AgentBox itself (?linux=move),
// set up before AgentBox ran in a VM on Linux (seedSettings), with /dev/kvm or
// without: the app prompts it to move into the VM, with its projects and
// agents to move.
export function seedLinuxHost(queryClient: QueryClient, kvm: boolean): void {
  seedSettings(queryClient);
  devState.hostSetup = { pkexec: '/usr/bin/pkexec', user: 'leandro', running: false, resizing: false, vm: null, wsl: null, linux: { mode: 'host', kvm, cores: 16, memory: 32 * 1024 ** 3, defaultCpus: 8, defaultMemoryCap: 24 * 1024 ** 3 }, chv: null };
  queryClient.setQueryData(['host-setup'], devState.hostSetup);
  devState.migration = { state: 'available', projects: [PROJECT, 'organic'], agents: [`${PROJECT}/agent-01`, `${PROJECT}/agent-12`, `${PROJECT}/agent-99`, 'organic/agent-3'] };
  queryClient.setQueryData(['vm-migration'], devState.migration);
}

// seedLinuxVM is Settings on a Linux machine in VM mode (?chv=…), at its
// Resources section, whose VM size card sizes the Cloud Hypervisor VM:
// running with room to grow (live), started by an older AgentBox with none
// (old), or off.
export function seedLinuxVM(queryClient: QueryClient, kind: string): void {
  seedSettings(queryClient);
  const GiB = 1024 ** 3;
  const room = { minCpus: 1, maxCpus: 16, minMemory: 4 * GiB, maxMemory: 32 * GiB, minDisk: 100 * GiB, maxDisk: 16384 * GiB };
  const chv: T.VMStatus = {
    mode: 'vm',
    driver: 'cloud-hypervisor',
    name: 'agentbox',
    state: kind === 'off' ? 'off' : 'running',
    since: new Date(Date.now() - 3_600_000).toISOString(),
    cpus: 8,
    memory: { min: 4 * GiB, cap: 24 * GiB, granted: kind === 'off' ? 0 : 9 * GiB, used: 6.2 * GiB, resident: 0 },
    disk: { size: 120 * GiB, allocated: 14 * GiB, pool: { size: 100 * GiB, allocated: 10 * GiB }, root: { size: 20 * GiB, allocated: 4 * GiB } },
    limits: room,
    live: kind === 'live' ? room : undefined,
    swap: kind === 'live' ? { size: 8 * GiB, total: 8 * GiB, used: 1.3 * GiB } : { size: 0, total: 0, used: 0 },
  };
  devState.hostSetup = {
    pkexec: '/usr/bin/pkexec',
    user: 'leandro',
    running: false,
    resizing: false,
    vm: null,
    wsl: null,
    linux: { mode: 'vm', kvm: true, cores: 16, memory: 32 * GiB, defaultCpus: 8, defaultMemoryCap: 24 * GiB },
    chv,
  };
  queryClient.setQueryData(['host-setup'], devState.hostSetup);
}

// seedMeterUsage is the top bar's CPU and disk popovers (?meters=cpu|disk) against three
// agents: one paused but still holding RAM and zram swap, and two running —
// so the popover has a largest-first list worth a screenshot,
// without a daemon or Incus to ask for one.
export function seedMeterUsage(queryClient: QueryClient): void {
  const GiB = 1024 ** 3;
  const memoryUsage: T.MemoryUsage = {
    hostTotal: 32 * GiB,
    agentsUsed: 12 * GiB,
    otherUsed: 3 * GiB,
    swapTotal: 20 * GiB,
    swapUsed: 17.5 * GiB,
    agentsSwap: 17.5 * GiB,
    zram: { swapBytes: 17.5 * GiB, realBytes: 3.6 * GiB },
    agents: [
      { ref: `${PROJECT}/agent-90`, title: 'Bump Electron to the next major, and every native module that breaks with it', state: 'paused', memory: 4 * GiB, swap: 17.5 * GiB },
      { ref: `${PROJECT}/agent-99`, title: 'PR agent', state: 'running', memory: 6 * GiB, swap: 0 },
      { ref: `${PROJECT}/agent-12`, title: 'Add a "New project" button to the Sidebar', state: 'running', memory: 2 * GiB, swap: 0 },
    ],
  };
  const cpuUsage: T.CPUUsage = {
    hostCPU: 62,
    hostCores: 8,
    otherCPU: 9,
    agents: [
      { ref: `${PROJECT}/agent-99`, title: 'PR agent', state: 'running', cpu: 41 },
      { ref: `${PROJECT}/agent-12`, title: 'Add a "New project" button to the Sidebar', state: 'running', cpu: 12 },
      { ref: `${PROJECT}/agent-90`, title: 'Bump Electron to the next major, and every native module that breaks with it', state: 'paused', cpu: 0 },
    ],
  };
  // The pool's machines and bases add up to the 15.5 GiB in use inside the
  // user's VM, measured on 2026-10-01.
  const diskUsage: T.DiskUsage = {
    total: 21.2 * GiB,
    categories: [
      {
        kind: 'machines',
        label: 'Agent machines',
        bytes: 9.8 * GiB,
        items: [
          { label: `${PROJECT}/agent-99`, bytes: 5.9 * GiB },
          { label: `${PROJECT}/agent-12`, bytes: 2.6 * GiB },
          { label: `${PROJECT}/agent-90`, bytes: 1.3 * GiB },
        ],
      },
      { kind: 'bases', label: 'Base images and saved bases', bytes: 5.7 * GiB, items: [{ label: 'Base image', bytes: 3.9 * GiB }, { label: `${PROJECT} base`, bytes: 1.8 * GiB }] },
      { kind: 'worktrees', label: 'Worktrees', bytes: 5.1 * GiB, items: [{ label: `${PROJECT}/agent-99`, bytes: 2.6 * GiB }, { label: `${PROJECT}/agent-12`, bytes: 2.5 * GiB }] },
      { kind: 'media', label: 'Media', bytes: 0.5 * GiB, items: [{ label: PROJECT, bytes: 0.5 * GiB }] },
      { kind: 'state', label: 'state.db and logs', bytes: 0.1 * GiB, items: [{ label: 'state.db', bytes: 0.1 * GiB }] },
    ],
  };
  devState.memoryUsage = memoryUsage;
  devState.cpuUsage = cpuUsage;
  devState.diskUsage = diskUsage;
  devState.homeDisk = { worktrees: 5.1 * GiB, media: 0.5 * GiB };
  queryClient.setQueryData(['memoryUsage'], memoryUsage);
  queryClient.setQueryData(['cpuUsage'], cpuUsage);
}

// seedVMDisk is the top bar's disk meter in VM mode (?meters=disk), with the
// numbers measured on the user's Cloud Hypervisor VM on 2026-10-01: pool.raw
// is 100 GiB taking 18.4 GiB of the host's disk, with 15.5 GiB in use inside;
// root.raw is 20 GiB taking 5.8 GiB. The meter reads 24 / 120 GiB. Unmeasured
// (?meters=disk-unmeasured), nothing is allocated, as a supervisor from before
// #161 made it: the meter reads — / 120 GiB.
export function seedVMDisk(queryClient: QueryClient, unmeasured = false): void {
  const GiB = 1024 ** 3;
  const pool = { size: 100 * GiB, allocated: unmeasured ? 0 : Math.round(18.4 * GiB) };
  const root = { size: 20 * GiB, allocated: unmeasured ? 0 : Math.round(5.8 * GiB) };
  const disk: T.VMDisk = { size: pool.size + root.size, allocated: pool.allocated + root.allocated, pool, root, hostFree: 310 * GiB };
  devState.vmPower = { state: 'running', memoryUsed: 11.3 * GiB, memoryGranted: 16 * GiB, memoryCap: 24 * GiB, cpus: 12, hostFree: disk.hostFree, disk };
  queryClient.setQueryData(['vmPower'], devState.vmPower);
}

// --- The agent queue (?queue=busy|alone|demo|off|settings|tasks) -----------
//
// Two projects sharing one 18 GiB budget (2.25 GiB reserved, so 15.75 GiB to
// split): "organic", whose agents run ~6 GiB each, and "agentbox" itself,
// whose agents run ~2 GiB each — both learned, not the installation's memory
// limit. Auto gives each active project at least one slot and splits the
// rest by memory, least-memory-first: with both active, agentbox gets 4
// (4×2=8 GiB) and organic 1 (1×6=6 GiB, 14 GiB together — a 5th agentbox slot
// would push it to 16, over budget); organic alone gets 2 (12 GiB; a 3rd
// would be 18, over budget).
const queueGiB = 1024 ** 3;
const queueBudget = 18 * queueGiB;
const queueReserve = 2.25 * queueGiB;
const organicPeakBytes = 6 * queueGiB;
const agentboxPeakBytes = 2 * queueGiB;

function organicProject(): T.Project {
  return {
    name: 'organic',
    displayName: 'Organic Web App',
    root: '/home/user/projects/organic',
    branch: 'main',
    envFiles: [],
    android: false,
    claudeAccount: '',
    claudeAccounts: [],
    githubAccount: '',
    autonomy: 'ask',
    agentModel: '',
    branchPrefix: 'agentbox/',
    finishNotices: 'lead',
    rolloverThreshold: 0,
    contextBudget: 0,
    consolidation: 0,
    consolidationModel: '',
    section: 's1',
    position: 1,
    nesting: false,
    agentPRs: false,
    syncBase: true,
    prWatch: '',
    prWatching: true,
    slots: 0,
    alwaysQueue: false,
    agentSize: '',
    createdAt: new Date().toISOString(),
  };
}

// slotAgent is one row of a ProjectSlots.agents table: what the slot maths'
// peak is learned from. cpu is a percentage of one core (100 = one core busy).
function slotAgent(name: string, title: string, memory: number, memoryPeak: number, cpu: number, cpuPeak: number): T.SlotAgent {
  return { name, title, state: 'running', memory, memoryPeak, cpu, cpuPeak };
}

// agentboxSlotAgents and organicSlotAgents are plausible per-agent numbers
// consistent with each project's learned peak (~1.5–2.5 GiB for agentbox,
// ~5–6.5 GiB for organic).
const agentboxSlotAgents: T.SlotAgent[] = [
  slotAgent('agent-1', 'Ship the release notes', 1.8 * queueGiB, 2.3 * queueGiB, 45, 120),
  slotAgent('agent-2', 'Rework the settings search', 1.5 * queueGiB, 1.9 * queueGiB, 30, 95),
  slotAgent('agent-3', 'Cut the base image build time', 2.1 * queueGiB, 2.5 * queueGiB, 60, 140),
  slotAgent('agent-4', 'Trim the daemon startup path', 1.6 * queueGiB, 2.0 * queueGiB, 25, 80),
];
const organicSlotAgentsBusy: T.SlotAgent[] = [slotAgent('agent-1', 'Rebuild the checkout flow', 5.6 * queueGiB, 6.2 * queueGiB, 80, 150)];
const organicSlotAgentsAlone: T.SlotAgent[] = [
  slotAgent('agent-1', 'Rebuild the checkout flow', 5.8 * queueGiB, 6.4 * queueGiB, 90, 150),
  slotAgent('agent-2', 'Speed up the search index', 5.2 * queueGiB, 5.9 * queueGiB, 70, 130),
];

function queueSlots(project: string, slots: number, peak: number, running: number, queued: number, agents: T.SlotAgent[]): T.ProjectSlots {
  return { project, slots, pinned: 0, peak, peakLearned: true, running, queued, agents };
}

function queueStatus(enabled: boolean, abSlots: number, abRunning: number, queued: T.QueuedAgent[], abAgents: T.SlotAgent[], orgSlots: number, orgRunning: number, orgAgents: T.SlotAgent[]): T.QueueStatus {
  return {
    enabled,
    budget: queueBudget,
    reserve: queueReserve,
    projects: [queueSlots(PROJECT, abSlots, agentboxPeakBytes, abRunning, queued.length, abAgents), queueSlots('organic', orgSlots, organicPeakBytes, orgRunning, 0, orgAgents)],
    queued,
    reserved: 0,
  };
}

const queueTaskGoals: Record<string, string> = {
  'agent-q5': 'Fix the flaky upload test',
  'agent-q6': 'Add pagination to the reminders page',
  'agent-q7': 'Write the onboarding email',
};

function queuedAgentFixture(name: string, position: number): T.QueuedAgent {
  return {
    ref: `${PROJECT}/${name}`,
    project: PROJECT,
    name,
    title: queueTaskGoals[name] ?? name,
    branch: `agentbox/${name}`,
    task: queueTaskGoals[name],
    taskId: `t-${name}`,
    position,
    queuedAt: new Date(Date.now() - position * 5_000).toISOString(),
    reserved: agentboxPeakBytes,
    waiting: 'queued: 6 agents in 2 projects reserve 15 of 18 GB; starts when ~4 GB is free',
  };
}

// queueTasks is agentbox's plan for the Tasks tab (?queue=tasks): three
// queued, one running, one unassigned, one done.
function queueTasks(running: string[], queued: string[]): T.Task[] {
  const now = new Date().toISOString();
  const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
  const task = (id: string, goal: string, status: string, agentName?: string): T.Task => ({ id, project: PROJECT, agent: agentName, status, goal, createdAt: now, updatedAt: now });
  return [
    task('t1', 'Ship the release notes', 'active', running[0]),
    ...queued.map((name, i) => task(`t${i + 2}`, queueTaskGoals[name] ?? name, 'open', name)),
    task('t5', 'Audit third-party licenses', 'open'),
    { ...task('t6', 'Bump the base image', 'done'), closedAt: now },
    // The Done list's three endings: implemented by a merged pull request,
    // done by hand, and abandoned.
    { ...task('t7', 'Split the Tasks tab into Open and Done', 'done'), closedAt: ago(40), pullUrl: 'https://github.com/leciric/agentbox/pull/155', pullNumber: 155 },
    { ...task('t8', 'Retry a failed image build from Setup', 'done'), closedAt: ago(60 * 26), pullUrl: 'https://github.com/leciric/agentbox/pull/149', pullNumber: 149 },
    { ...task('t9', 'Try Vulkan transcription on every GPU', 'abandoned'), closedAt: ago(60 * 50) },
  ];
}

// seedQueue seeds the agent queue scenarios: 'busy' is agentbox at 4 of 4
// slots with three queued and organic content with its own single slot;
// 'alone' is organic without agentbox to share the budget with, so Auto gives
// it two; 'demo' starts as 'busy' and then plays the queue draining, a slot
// at a time, on a timer, for a recording; 'off' is 'busy' with the
// installation's Agent queue switch off — the queue UI steps out of the way,
// but a queued agent made before it was turned off still shows its place.
export function seedQueue(queryClient: QueryClient, mode: 'busy' | 'alone' | 'demo' | 'off'): void {
  const enabled = mode !== 'off';
  queryClient.setQueryData(['settings'], { ...defaultsSettings, agentQueue: enabled });
  const abProject: T.Project = { ...buildFixtures().projects[0], slots: 0, alwaysQueue: false, section: 's1', position: 0 };
  const orgProject = organicProject();
  queryClient.setQueryData(['projects'], [abProject, orgProject]);
  queryClient.setQueryData(['sections'], [{ id: 's1', name: 'Work', position: 0, collapsed: false, createdAt: new Date().toISOString() }] satisfies T.Section[]);
  // Both projects, not only agentbox: the rail reads whichever one is open
  // (?queue=alone opens organic), and a query with nothing seeded would fall
  // through to the dev bridge's generic {} rather than the empty list or
  // fleet these expect.
  for (const name of [PROJECT, 'organic']) {
    queryClient.setQueryData(['agentEvents', name], []);
    queryClient.setQueryData(['questions', name], []);
    queryClient.setQueryData(['fleet', name], { project: name, agents: [], creating: [], idle: 0 } satisfies T.Fleet);
    queryClient.setQueryData(['projectChat', name], { project: name, ref: `${name}/lead`, started: true, chat: 'idle' } satisfies T.ProjectChat);
  }

  const running = (project: string, i: number, title: string) => agent({ ref: `${project}/agent-${i}`, project, title, chat: 'running', state: 'running' });
  const queuedAgentState = (name: string, position: number) => agent({ ref: `${PROJECT}/${name}`, project: PROJECT, title: queueTaskGoals[name] ?? name, state: 'queued', queuePosition: position, waiting: queuedAgentFixture(name, position).waiting, chat: undefined });

  if (mode === 'alone') {
    const agents = [running('organic', 1, 'Rebuild the checkout flow'), running('organic', 2, 'Speed up the search index')];
    queryClient.setQueryData(['agents'], agents);
    const status = queueStatus(true, 0, 0, [], [], 2, 2, organicSlotAgentsAlone);
    queryClient.setQueryData(['queue', PROJECT], status);
    queryClient.setQueryData(['queue', 'organic'], status);
    return;
  }

  const runningNames = [1, 2, 3, 4].map((i) => `agent-${i}`);
  const queuedNames = ['agent-q5', 'agent-q6', 'agent-q7'];
  let agents: T.Agent[] = [
    running(PROJECT, 1, 'Ship the release notes'),
    running(PROJECT, 2, 'Rework the settings search'),
    running(PROJECT, 3, 'Cut the base image build time'),
    running(PROJECT, 4, 'Trim the daemon startup path'),
    queuedAgentState('agent-q5', 1),
    queuedAgentState('agent-q6', 2),
    queuedAgentState('agent-q7', 3),
    running('organic', 1, 'Rebuild the checkout flow'),
  ];
  queryClient.setQueryData(['agents'], agents);
  let queued = queuedNames.map((name, i) => queuedAgentFixture(name, i + 1));
  let abAgents = agentboxSlotAgents;
  const publish = (abRunning: number) => {
    const status = queueStatus(enabled, 4, abRunning, queued, abAgents, 1, 1, organicSlotAgentsBusy);
    queryClient.setQueryData(['queue', PROJECT], status);
    queryClient.setQueryData(['queue', 'organic'], status);
    queryClient.setQueryData(['memoryTasks', PROJECT], queueTasks(runningNames, queued.map((q) => q.name)));
  };
  publish(4);

  if (mode !== 'demo') return;

  // Every ~4s: stop a running agentbox agent, then bring the agent at the
  // front of the queue up (initializing, then running), and shift the rest
  // up a place — until nothing is left queued. The per-agent usage table
  // follows: a stopped agent drops off it, a newly running one joins.
  let abRunning = 4;
  const drain = () => {
    if (queued.length === 0) return;
    const runner = agents.find((a) => a.project === PROJECT && a.state === 'running');
    if (runner) {
      agents = agents.map((a) => (a.ref === runner.ref ? { ...a, state: 'stopped', chat: 'off' } : a));
      abAgents = abAgents.filter((a) => a.name !== runner.name);
    }
    abRunning -= 1;
    queryClient.setQueryData(['agents'], agents);
    publish(abRunning);

    const next = queued[0];
    setTimeout(() => {
      agents = agents.map((a) => (a.ref === next.ref ? { ...a, state: 'initializing', queuePosition: undefined } : a));
      queryClient.setQueryData(['agents'], agents);
      setTimeout(() => {
        agents = agents.map((a) => (a.ref === next.ref ? { ...a, state: 'running', chat: 'running' } : a));
        queued = queued.slice(1).map((q, i) => ({ ...q, position: i + 1 }));
        agents = agents.map((a) => {
          const q = queued.find((qq) => qq.ref === a.ref);
          return q ? { ...a, queuePosition: q.position } : a;
        });
        abAgents = [...abAgents, slotAgent(next.name, next.title, 1.4 * queueGiB, 1.4 * queueGiB, 20, 20)];
        abRunning += 1;
        queryClient.setQueryData(['agents'], agents);
        publish(abRunning);
      }, 1_200);
    }, 800);

    setTimeout(drain, 4_000);
  };
  setTimeout(drain, 4_000);
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
      if (method === 'GET' && devState.update && path === '/v1/update') return { status: 200, body: JSON.stringify(devState.update), contentType: 'application/json' };
      if (method === 'GET' && devState.setup && path === '/v1/setup') return { status: 200, body: JSON.stringify(devState.setup), contentType: 'application/json' };
      if (method === 'GET' && devState.memoryUsage && path === '/v1/usage/memory') return { status: 200, body: JSON.stringify(devState.memoryUsage), contentType: 'application/json' };
      // The disk guard with room everywhere, so DiskGuardPill shows nothing
      // rather than reading the catch-all's answer as a guard.
      if (method === 'GET' && path === '/v1/disk') return { status: 200, body: JSON.stringify({ level: 'ok', disks: [], paused: [], since: '', message: '' }), contentType: 'application/json' };
      if (method === 'GET' && devState.diskUsage && path === '/v1/usage/disk') return { status: 200, body: JSON.stringify(devState.diskUsage), contentType: 'application/json' };
      if (method === 'GET' && devState.cpuUsage && path.startsWith('/v1/usage/cpu')) return { status: 200, body: JSON.stringify(devState.cpuUsage), contentType: 'application/json' };
      if (method === 'GET' && devState.job && path === `/v1/jobs/${devState.job.id}`) return { status: 200, body: JSON.stringify(devState.job), contentType: 'application/json' };
      if (method === 'GET' && /^\/v1\/jobs\/[^/]+\/log$/.test(path)) return { status: 200, body: devState.jobLog ?? '', contentType: 'text/plain' };
      // The ?media= scenarios' gallery, the project's and agent-99's.
      if (method === 'GET' && devState.media && path.startsWith(`/v1/projects/${PROJECT}/media`))
        return { status: 200, body: JSON.stringify(devState.media), contentType: 'application/json' };
      if (method === 'GET' && devState.media && path === `/v1/agents/${PROJECT}/agent-99/media`)
        return { status: 200, body: JSON.stringify(devState.media.filter((m) => m.agentName === 'agent-99')), contentType: 'application/json' };
      if (method === 'GET' && devState.notifications && path === '/v1/notifications')
        return { status: 200, body: JSON.stringify(devState.notifications), contentType: 'application/json' };
      if (method === 'GET' && devState.allMedia && path.startsWith('/v1/media?'))
        return { status: 200, body: JSON.stringify(devState.allMedia), contentType: 'application/json' };
      if (method === 'POST' && devState.notifications && path === '/v1/notifications/seen')
        return { status: 200, body: JSON.stringify(seeNotifications(body as T.SeeNotificationsRequest)), contentType: 'application/json' };
      if (method === 'POST' && path === '/v1/agents/stop') return stopAgents((body as T.StopAgentsRequest).refs ?? []);
      const started = method === 'POST' ? /^\/v1\/agents\/([^/]+)\/([^/]+)\/start$/.exec(path) : null;
      if (started && devState.agents) {
        const ref = `${decodeURIComponent(started[1])}/${decodeURIComponent(started[2])}`;
        devState.agents = devState.agents.map((a) => (a.ref === ref ? { ...a, state: 'running', chat: 'ready' } : a));
        return { status: 200, body: JSON.stringify(devState.agents.find((a) => a.ref === ref)), contentType: 'application/json' };
      }
      // No checkpoints: Timeline filters what it gets, and {} isn't a list.
      if (method === 'GET' && path.endsWith('/checkpoints')) return { status: 200, body: '[]', contentType: 'application/json' };
      if (method === 'GET' && path === '/v1/agents' && devState.agents) return { status: 200, body: JSON.stringify(devState.agents), contentType: 'application/json' };
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
    info: async () => ({ socket: '/run/user/1000/agentbox.sock', version: devState.appVersion ?? 'preview', electron: '38.1.0', packaged: false, platform: 'darwin' }),
    stream: { open: async () => 0, write: () => {}, close: () => {}, onOpened: () => () => {}, onData: () => () => {}, onExited: () => () => {} },
    cli: { status: async () => devState.cli ?? {}, install: async () => ({}) },
    hostSetup: { status: async () => devState.hostSetup ?? {}, run: async () => ({ restarted: false }), onOutput: () => () => {}, budget: async () => {} },
    vmMigrate: { status: async () => devState.migration ?? null, run: async () => {}, removeOld: async () => {}, onOutput: () => () => {} },
    vm: fakeVM(),
    hubs: { list: async () => [], login: async () => ({}), logout: async () => {}, environments: async () => [], addEnvironment: async () => ({}) },
    target: { get: async () => ({ kind: 'local' }), set: async (t: unknown) => t, onChange: () => () => {} },
    mediaUrl: (id: string) => mediaFiles.get(id) ?? '',
    report: { sections: async () => [], windowError: () => {}, onAppError: () => () => {}, openLogs: async () => '' },
    pickDirectory: async () => null,
    setLanguage: () => {},
    openPath: async () => '',
    showItem: async () => {},
    appUpdate: {
      supported: async () => false,
      state: async () => ({ state: 'idle' as const }),
      download: async () => ({ state: 'idle' as const }),
      install: async () => ({ state: 'idle' as const }),
      onState: () => () => {},
    },
    openExternal: async () => {},
    notify: async () => false,
    onNotificationClick: () => () => {},
    copyText: () => {},
    readText: async () => '',
  };
}

// seedNightly is the ?nightly=1 scenario: the app a nightly build, on the
// nightly channel with a newer nightly out, so the sidebar wears its sky and
// badge and Settings shows the channel. Switching to Stable in Settings offers
// the latest stable, lower than this nightly, the way internal/update's Offer
// does.
const nightlyVersion = '0.11.0-nightly.20260929.12';
function nightlyStatus(current: string, channel: string): T.UpdateStatus {
  const available =
    channel === 'nightly'
      ? { version: '0.11.0-nightly.20260930.13', url: 'https://downloads.agentbox.linting.dev/releases/v0.11.0-nightly.20260930.13/index.html' }
      : { version: '0.10.0', url: 'https://downloads.agentbox.linting.dev/releases/v0.10.0/index.html' };
  return { current, enabled: true, channel, nightly: true, available, checkedAt: new Date().toISOString() };
}
export function seedNightly(queryClient: QueryClient): void {
  devState.appVersion = nightlyVersion;
  devState.update = nightlyStatus(nightlyVersion, 'nightly');
  queryClient.setQueryData(['update'], devState.update);
  queryClient.setQueryData(['app-info'], { socket: '/run/user/1000/agentbox.sock', version: nightlyVersion, electron: '38.1.0', packaged: true, platform: 'linux' });
}
