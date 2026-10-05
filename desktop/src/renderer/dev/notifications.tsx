// The notifications scenarios, with fixtures: clickable notifications, the
// top bar's bell with their history, the all-projects Media view, and the
// viewer a notification opens. They render the app's own Sidebar, TopBar,
// NotificationBell, NoticeToast, AllMediaView, MediaViewer and MediaPlace,
// put together the way App does, over a dev bridge that answers
// /v1/notifications and /v1/media from here (fixtures.ts setNotifications).
//
//   ?notify=bell     Home, with the bell's history open
//   ?notify=media    the all-projects Media view
//   ?notify=viewer   a notification clicked: its agent's Media, its item open
//   ?notify=toast    the in-app toast, the whole of which is the link
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { lazy, Suspense, useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { AllMediaView, shownKinds } from '../components/AllMediaView';
import { HomeView } from '../components/HomeView';
import { MediaPlace } from '../components/MediaPlace';
import { MediaViewer } from '../components/MediaTab';
import { NoticeToast } from '../components/Notifications';
import { Sidebar } from '../components/Sidebar';
import { TopBar } from '../components/TopBar';
import { markSeen, noticeView } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { PROJECT, setMediaFile, setNotifications } from './fixtures';

const AgentView = lazy(() => import('../components/AgentView').then((m) => ({ default: m.AgentView })));
const ProjectView = lazy(() => import('../components/ProjectView').then((m) => ({ default: m.ProjectView })));

const LONG = 'a-project-with-a-name-so-long-it-should-never-be-allowed-to-widen-the-sidebar';
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

// The agents the mock's media and notifications come from, in three projects.
const mockAgents = [
  { ref: `${PROJECT}/agent-12`, title: 'Add a "New project" button to the Sidebar', pr: { number: 142, state: 'open' } },
  { ref: `${PROJECT}/agent-96`, title: 'Fix the agent rail overflowing on wide text', pr: { number: 139, state: 'merged' } },
  { ref: `${PROJECT}/agent-99`, title: 'PR agent', pr: { number: 78, state: 'open' } },
  { ref: `${PROJECT}/agent-97`, title: 'Long path agent' },
  { ref: 'organic/agent-31', title: 'Checkout page for the shop', pr: { number: 57, state: 'open' } },
  { ref: 'organic/agent-33', title: 'Weekly reminders digest email' },
  { ref: `${LONG}/agent-7`, title: 'An onboarding tour for first-time users, five steps long', pr: { number: 12, state: 'draft' } },
];

// What each screenshot shows: a made-up app window, so the gallery reads as
// one, with the topic written on it.
function screen(topic: string, hue: number, dark: boolean): string {
  const bg = dark ? `hsl(${hue} 25% 12%)` : `hsl(${hue} 40% 97%)`;
  const panel = dark ? `hsl(${hue} 20% 18%)` : '#ffffff';
  const line = dark ? `hsl(${hue} 15% 30%)` : `hsl(${hue} 20% 86%)`;
  const accent = `hsl(${hue} 70% 58%)`;
  const text = dark ? '#e7e7ee' : '#1d1d28';
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800" viewBox="0 0 1280 800">
<rect width="1280" height="800" fill="${bg}"/>
<rect width="1280" height="44" fill="${panel}"/><circle cx="24" cy="22" r="6" fill="#f87171"/><circle cx="44" cy="22" r="6" fill="#fbbf24"/><circle cx="64" cy="22" r="6" fill="#34d399"/>
<rect x="0" y="44" width="240" height="756" fill="${panel}" opacity="0.7"/>
${[0, 1, 2, 3, 4, 5].map((i) => `<rect x="24" y="${84 + i * 44}" width="${150 - (i % 3) * 25}" height="14" rx="7" fill="${i === 1 ? accent : line}"/>`).join('')}
<text x="288" y="120" font-family="Inter,sans-serif" font-size="40" font-weight="600" fill="${text}">${topic}</text>
<rect x="288" y="150" width="520" height="14" rx="7" fill="${line}"/>
${[0, 1, 2].map((i) => `<rect x="${288 + i * 312}" y="210" width="288" height="200" rx="16" fill="${panel}" stroke="${line}"/><rect x="${312 + i * 312}" y="240" width="${120 + i * 30}" height="14" rx="7" fill="${i === 0 ? accent : line}"/><rect x="${312 + i * 312}" y="270" width="200" height="10" rx="5" fill="${line}"/><rect x="${312 + i * 312}" y="292" width="160" height="10" rx="5" fill="${line}"/>`).join('')}
<rect x="288" y="450" width="912" height="290" rx="16" fill="${panel}" stroke="${line}"/>
<rect x="1040" y="680" width="136" height="40" rx="10" fill="${accent}"/>
</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const topics = [
  ['sidebar-new-project-after', 'New project button'],
  ['rail-overflow-fixed', 'Rail with a long branch'],
  ['checkout-step-2', 'Checkout: shipping'],
  ['digest-email-preview', 'Weekly digest'],
  ['onboarding-tour-step-3', 'Tour, step 3 of 5'],
  ['pr-agent-settings', 'Settings → Agents'],
  ['checkout-mobile', 'Checkout on a phone'],
  ['login-flow', 'Sign in'],
];

// mockMedia is every project's screenshots and recordings over the last few
// days, newest first. The newest few are unseen.
export function mockMedia(): T.MediaItem[] {
  const dark = document.documentElement.dataset.appearance !== 'light';
  const now = Date.now();
  // Minutes ago, spread over today, yesterday and earlier in the week.
  const ago = [4, 11, 26, 48, 95, 140, 210, 330, DAY / MIN + 40, DAY / MIN + 95, DAY / MIN + 180, DAY / MIN + 300, DAY / MIN + 420, 2 * DAY / MIN + 60, 2 * DAY / MIN + 200, 2 * DAY / MIN + 380, 4 * DAY / MIN + 30, 4 * DAY / MIN + 90, 4 * DAY / MIN + 250, 5 * DAY / MIN + 100];
  const order = [0, 4, 6, 0, 1, 4, 5, 2, 1, 6, 4, 0, 2, 5, 1, 4, 2, 0, 6, 1];
  return ago.map((m, i) => {
    const a = mockAgents[order[i]];
    const name = a.ref.split('/')[1];
    const [file, topic] = topics[(i + order[i]) % topics.length];
    const kind = i % 3 === 1 ? 'recording' : 'screenshot';
    const id = `n${String(i).padStart(3, '0')}`;
    if (kind === 'screenshot') setMediaFile(id, screen(topic, [262, 200, 30, 150, 340, 280, 190, 20][(i + order[i]) % 8], dark));
    return {
      id,
      agent: a.ref,
      agentName: name,
      agentTitle: a.title,
      kind,
      name: file,
      file: `${file}.${kind === 'recording' ? 'mp4' : 'png'}`,
      mime: kind === 'recording' ? 'video/mp4' : 'image/png',
      size: 180_000 + i * 53_000,
      source: 'agent',
      meta: kind === 'recording' ? { duration: 18 + i * 7 } : { width: 1280, height: 800 },
      createdAt: new Date(now - m * MIN).toISOString(),
      unseen: i < 4,
    } as T.MediaItem;
  });
}

// mockNotices is the bell's history: the newest dozen media items (the
// newest four unseen, as mockMedia marks them), with finishes and questions
// among them.
function mockNotices(media: T.MediaItem[]): T.Notification[] {
  const now = Date.now();
  const at = (m: number) => new Date(now - m * MIN).toISOString();
  const agent = (ref: string) => {
    const a = mockAgents.find((x) => x.ref === ref)!;
    const [project, name] = ref.split('/');
    return { project, agent: name, ref, title: a.title };
  };
  const fromMedia: T.Notification[] = media.slice(0, 12).map((m, i) => ({ id: `nm-${m.id}`, kind: 'media', ...agent(m.agent), at: m.createdAt, seen: i >= 4, media: m, text: m.name }));
  const others: T.Notification[] = [
    { id: 'q1', kind: 'question', ...agent('organic/agent-33'), at: at(7), seen: false, question: 'q1', text: 'Should the digest go out on Mondays or Fridays?' },
    { id: 'f1', kind: 'finished', ...agent(`${PROJECT}/agent-12`), at: at(14), seen: false, status: 'done', pr: mockPR(`${PROJECT}/agent-12`), text: 'The Projects header has a + button that opens Add project. PR #142 opened.' },
    { id: 'f2', kind: 'finished', ...agent(`${LONG}/agent-7`), at: at(90), seen: true, status: 'partial', text: 'Steps 1–3 of the tour work; step 4 needs the settings page from #11.' },
    { id: 'f3', kind: 'finished', ...agent(`${PROJECT}/agent-96`), at: at(DAY / MIN + 60), seen: true, status: 'done', text: 'The rail no longer widens with a long branch name. Merged as #139.' },
    { id: 'q2', kind: 'question', ...agent(`${PROJECT}/agent-99`), at: at(DAY / MIN + 200), seen: true, question: 'q2', text: 'Should the reminders page paginate?' },
    { id: 'f4', kind: 'finished', ...agent('organic/agent-31'), at: at(2 * DAY / MIN + 30), seen: true, status: 'blocked', text: 'Needs a Stripe test key: STRIPE_SECRET_KEY.' },
  ];
  return [...fromMedia, ...others].sort((a, b) => b.at.localeCompare(a.at));
}

// seedNotifications adds the projects and agents the scenarios name to the
// fixtures, with their pull requests in each project's fleet, each agent's
// own media list, the bell's history and the all-projects media.
export function seedNotifications(queryClient: QueryClient, media: T.MediaItem[]): void {
  const notices = mockNotices(media);
  setNotifications(notices, media);
  queryClient.setQueryData(['notifications'], notices);
  queryClient.setQueryData(['allMedia', shownKinds], media);
  queryClient.setQueryData<T.Project[]>(['projects'], (ps) => {
    if (!ps || ps.some((p) => p.name === 'organic')) return ps;
    const base = ps.find((p) => p.name === PROJECT)!;
    return [...ps, { ...base, name: 'organic', displayName: '', root: '/home/user/projects/organic' }];
  });
  queryClient.setQueryData<T.Agent[]>(['agents'], (as) => {
    if (!as) return as;
    const base = as.find((a) => a.ref === `${PROJECT}/agent-12`)!;
    const extra = mockAgents
      .filter((m) => !as.some((a) => a.ref === m.ref))
      .map((m) => ({ ...base, ref: m.ref, project: m.ref.split('/')[0], name: m.ref.split('/')[1], title: m.title, chat: 'ready' }) as T.Agent);
    return [...as, ...extra];
  });
  for (const project of new Set(mockAgents.map((a) => a.ref.split('/')[0]))) {
    queryClient.setQueryData<T.Fleet>(['fleet', project], (fleet) => {
      const f = fleet ?? { project, agents: [], creating: [], idle: 0 };
      const ours = mockAgents.filter((a) => a.ref.startsWith(`${project}/`));
      const agents: T.FleetAgent[] = f.agents.map((a) => ({ ...a, pr: a.pr ?? mockPR(a.ref) }));
      for (const a of ours)
        if (!agents.some((x) => x.ref === a.ref)) agents.push({ ref: a.ref, project, name: a.ref.split('/')[1], title: a.title, pr: mockPR(a.ref) } as T.FleetAgent);
      return { ...f, agents };
    });
    // The top bar's usage meter and the rail read these: nothing to say.
    for (const key of ['questions', 'agentEvents']) if (!queryClient.getQueryData([key, project])) queryClient.setQueryData([key, project], []);
  }
  queryClient.setQueryData(['claudeLimits'], []);
  for (const a of mockAgents) queryClient.setQueryData(['media', a.ref], media.filter((m) => m.agent === a.ref));
}

function mockPR(ref: string): T.PullRequest | undefined {
  const p = mockAgents.find((x) => x.ref === ref)?.pr;
  return p && ({ number: p.number, title: 'The pull request', state: p.state === 'draft' ? 'open' : p.state, draft: p.state === 'draft', checks: 'passing', url: `https://github.com/leciric/agentbox/pull/${p.number}` } as T.PullRequest);
}

// NotificationsPreview puts the pieces together as App does: the sidebar, the
// top bar with its bell, the page, and the viewer a notification opens.
export function NotificationsPreview({ scenario }: { scenario: string }) {
  const queryClient = useQueryClient();
  const [view, setView] = useState<View>(scenario === 'media' ? { kind: 'media' } : { kind: 'home' });
  const [viewing, setViewing] = useState<{ ref: string; id: string; from: string; at: string } | null>(null);
  const agents = queryClient.getQueryData<T.Agent[]>(['agents']);

  const openNotice = (n: T.Notification) => {
    void markSeen(queryClient, { ids: [n.id] });
    const next = noticeView(n, agents);
    setView(next);
    if (n.media && next.kind === 'agent') setViewing({ ref: n.ref, id: n.media.id, from: n.media.id, at: n.at });
  };

  useEffect(() => {
    const notices = queryClient.getQueryData<T.Notification[]>(['notifications']) ?? [];
    const shot = notices.find((n) => n.media?.kind === 'screenshot' && !n.seen)!;
    if (scenario === 'viewer') openNotice(shot);
    if (scenario === 'bell') requestAnimationFrame(() => document.querySelector<HTMLButtonElement>('[data-bell]')?.click());
    if (scenario !== 'toast') return;
    const projectName = projectLabel(shot.project, queryClient.getQueryData<T.Project[]>(['projects']));
    const id = toast.custom((t) => <NoticeToast notice={shot} projectName={projectName} onOpen={() => (toast.dismiss(t), openNotice(shot))} />, { duration: Infinity });
    return () => void toast.dismiss(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const items = viewing ? (queryClient.getQueryData<T.MediaItem[]>(['media', viewing.ref]) ?? []) : [];
  const index = viewing ? items.findIndex((m) => m.id === viewing.id) : -1;
  const select = (next: View) => {
    setViewing(null);
    setView(next);
  };

  return (
    <div className="flex h-screen w-screen" style={{ background: 'var(--color-ink)', font: '13px var(--font-sans)' }} data-preview-notify={scenario}>
      <Sidebar view={view} onSelect={select} onAddProject={() => {}} onNewAgent={() => {}} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar view={view} onSelect={select} onOpenNav={() => {}} onNewAgent={() => {}} onOpenNotice={openNotice} />
        <main className="min-h-0 flex-1">
          <Suspense>
            {view.kind === 'home' && <HomeView onSelect={select} onAddProject={() => {}} onNewAgent={() => {}} />}
            {view.kind === 'media' && <AllMediaView onSelect={select} />}
            {view.kind === 'project' && <ProjectView key={view.project} name={view.project} tab={view.tab} onSelect={select} onNewAgent={() => {}} />}
            {view.kind === 'agent' && <AgentView key={view.ref} agentRef={view.ref} tab={view.tab} onTab={(tab) => setView({ ...view, tab })} onSelect={select} />}
          </Suspense>
        </main>
      </div>
      <MediaViewer
        items={items}
        index={index}
        onIndex={(i) => items[i] && setViewing((v) => v && { ...v, id: items[i].id })}
        onClose={() => setViewing(null)}
        onDelete={() => {}}
        context={(item) => <MediaPlace item={item} fromNotice={item.id === viewing?.from ? viewing.at : undefined} onSelect={select} />}
      />
    </div>
  );
}
