// A mock, for review before it's built: notifications you can click, the top
// bar's bell with their history, an all-projects Media view, and the viewer a
// notification opens. Everything here is dev-only and fixture-fed; nothing in
// the app imports it. The real Sidebar and TopBar are used as they are, with
// the new pieces portalled into them where they would go (Inject, below).
//
//   ?notify=bell     Home, with the bell's history open
//   ?notify=media    the all-projects Media view
//   ?notify=viewer   a notification clicked: its agent's Media, its item open
//   ?notify=toast    the in-app toast, the whole of which is the link
import { useQuery, type QueryClient } from '@tanstack/react-query';
import {
  Bell,
  Check,
  CheckCheck,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  ExternalLink,
  FolderGit2,
  FolderOpen,
  GitPullRequest,
  Image as ImageIcon,
  Images,
  Layers,
  MessageCircleQuestion,
  Trash,
  Video,
} from 'lucide-react';
import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { HomeView } from '../components/HomeView';
import { FilterChip, MediaCard } from '../components/MediaTab';
import { Sidebar } from '../components/Sidebar';
import { TopBar } from '../components/TopBar';
import { Badge, type BadgeVariant } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '../components/ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from '../components/ui/popover';
import { Tip } from '../components/ui/tooltip';
import { clock, kindInfo, mediaUrl } from '../lib/media';
import { projectLabel } from '../lib/projectName';
import type { AgentPlaceName } from '../lib/tabs';
import { cn, timeAgo } from '../lib/utils';
import { PROJECT, setMediaFile } from './fixtures';

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
type MockAgent = (typeof mockAgents)[number];
const agentOf = (ref: string) => mockAgents.find((a) => a.ref === ref)!;

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
    const [project, name] = a.ref.split('/');
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
      project,
    } as T.MediaItem;
  });
}

// A notification as the history keeps it: what happened, to which agent of
// which project, and the media item it's about, if any.
interface Notice {
  id: string;
  kind: 'media' | 'finished' | 'question';
  agent: string; // ref
  at: string;
  seen: boolean;
  media?: T.MediaItem;
  status?: 'done' | 'partial' | 'blocked' | 'failed';
  text: string;
}

function mockNotices(media: T.MediaItem[]): Notice[] {
  const now = Date.now();
  const at = (m: number) => new Date(now - m * MIN).toISOString();
  const fromMedia: Notice[] = media.slice(0, 12).map((m, i) => ({ id: `nm-${m.id}`, kind: 'media', agent: m.agent, at: m.createdAt, seen: i >= 4, media: m, text: m.name }));
  const others: Notice[] = [
    { id: 'q1', kind: 'question', agent: 'organic/agent-33', at: at(7), seen: false, text: 'Should the digest go out on Mondays or Fridays?' },
    { id: 'f1', kind: 'finished', agent: `${PROJECT}/agent-12`, at: at(14), seen: false, status: 'done', text: 'The Projects header has a + button that opens Add project. PR #142 opened.' },
    { id: 'f2', kind: 'finished', agent: `${LONG}/agent-7`, at: at(90), seen: true, status: 'partial', text: 'Steps 1–3 of the tour work; step 4 needs the settings page from #11.' },
    { id: 'f3', kind: 'finished', agent: `${PROJECT}/agent-96`, at: at(DAY / MIN + 60), seen: true, status: 'done', text: 'The rail no longer widens with a long branch name. Merged as #139.' },
    { id: 'q2', kind: 'question', agent: `${PROJECT}/agent-99`, at: at(DAY / MIN + 200), seen: true, text: 'Should the reminders page paginate?' },
    { id: 'f4', kind: 'finished', agent: 'organic/agent-31', at: at(2 * DAY / MIN + 30), seen: true, status: 'blocked', text: 'Needs a Stripe test key: STRIPE_SECRET_KEY.' },
  ];
  return [...fromMedia, ...others].sort((a, b) => b.at.localeCompare(a.at));
}

// seedNotifications adds the projects and agents the mock names to the
// fixtures, and gives each agent its own media list.
export function seedNotifications(queryClient: QueryClient, media: T.MediaItem[]): void {
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
  // The top bar's usage meter reads these; no limits read yet.
  queryClient.setQueryData(['claudeLimits'], []);
  // Projects the fixtures have no questions or events for: none yet.
  for (const project of new Set(mockAgents.map((a) => a.ref.split('/')[0])))
    for (const key of ['questions', 'agentEvents']) if (!queryClient.getQueryData([key, project])) queryClient.setQueryData([key, project], []);
  for (const a of mockAgents) queryClient.setQueryData(['media', a.ref], media.filter((m) => m.agent === a.ref));
}

// Inject portals children next to an element the real components render, so
// the mock can show where the new pieces go without changing those components.
function Inject({ find, place, children }: { find: () => Element | null | undefined; place: 'after' | 'prepend'; children: ReactNode }) {
  const [host, setHost] = useState<HTMLElement | null>(null);
  useEffect(() => {
    let el: HTMLElement | null = null;
    let frame = 0;
    const attach = () => {
      const target = find();
      if (!target) {
        frame = requestAnimationFrame(attach);
        return;
      }
      el = document.createElement('div');
      el.style.display = 'contents';
      if (place === 'after') target.after(el);
      else target.prepend(el);
      setHost(el);
    };
    attach();
    return () => {
      cancelAnimationFrame(frame);
      el?.remove();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return host ? createPortal(children, host) : null;
}

function dayLabel(iso: string, now = Date.now()): string {
  const start = new Date(now);
  start.setHours(0, 0, 0, 0);
  const t = new Date(iso).getTime();
  if (t >= start.getTime()) return 'Today';
  if (t >= start.getTime() - DAY) return 'Yesterday';
  return new Date(iso).toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' });
}

function byDay<V>(list: V[], at: (v: V) => string): [string, V[]][] {
  const groups = new Map<string, V[]>();
  for (const v of list) {
    const day = dayLabel(at(v));
    groups.set(day, [...(groups.get(day) ?? []), v]);
  }
  return [...groups];
}

function usePlace(ref: string) {
  const projects = useQuery<T.Project[]>({ queryKey: ['projects'], enabled: false });
  const [project, name] = ref.split('/');
  return { project, name, projectName: projectLabel(project, projects.data), agent: agentOf(ref) as MockAgent | undefined };
}

// ---- The bell ----

const statusIcon = {
  done: { icon: CircleCheck, tone: 'text-emerald-400 bg-emerald-400/10', verb: 'finished' },
  partial: { icon: CircleDashed, tone: 'text-amber-300 bg-amber-400/10', verb: 'finished part of it' },
  blocked: { icon: CircleAlert, tone: 'text-rose-300 bg-rose-400/10', verb: 'is blocked' },
  failed: { icon: CircleAlert, tone: 'text-rose-300 bg-rose-400/10', verb: 'failed' },
};

function NoticeArt({ notice }: { notice: Notice }) {
  if (notice.media) {
    return (
      <span className="relative flex h-9 w-14 shrink-0 items-center justify-center overflow-hidden rounded-md border border-line bg-well">
        {notice.media.kind === 'screenshot' ? (
          <img src={mediaUrl(notice.media)} alt="" className="size-full object-cover" />
        ) : (
          <>
            <Video className="size-4 text-rose-300" />
            <span className="absolute bottom-0.5 right-0.5 rounded bg-black/70 px-1 font-mono text-[9px] text-white">{clock(notice.media.meta.duration ?? 0)}</span>
          </>
        )}
      </span>
    );
  }
  const { icon: Icon, tone } = notice.kind === 'question' ? { icon: MessageCircleQuestion, tone: 'text-amber-300 bg-amber-400/10' } : statusIcon[notice.status!];
  return (
    <span className={cn('flex h-9 w-14 shrink-0 items-center justify-center rounded-md', tone)}>
      <Icon className="size-4" />
    </span>
  );
}

function headline(notice: Notice, name: string): ReactNode {
  const who = <span className="font-mono text-[12.5px]">{name}</span>;
  if (notice.kind === 'media') return <>{who} saved a {kindInfo(notice.media!.kind).one}</>;
  if (notice.kind === 'question') return <>{who} asks you</>;
  return <>{who} {statusIcon[notice.status!].verb}</>;
}

function NoticeRow({ notice, onOpen }: { notice: Notice; onOpen: () => void }) {
  const { name, projectName, agent } = usePlace(notice.agent);
  return (
    <button
      onClick={onOpen}
      data-notice={notice.kind}
      className={cn('group flex w-full items-start gap-3 rounded-lg px-2.5 py-2 text-left transition hover:bg-surface', !notice.seen && 'bg-brand-500/[0.06]')}
    >
      <NoticeArt notice={notice} />
      <span className="min-w-0 flex-1">
        <span className={cn('flex items-center gap-1.5 text-[13px]', notice.seen ? 'text-secondary' : 'font-medium text-title')}>
          <span className="truncate">{headline(notice, name)}</span>
          <span className="ml-auto shrink-0 text-[11px] font-normal text-subtle">{timeAgo(notice.at)}</span>
        </span>
        <span className="mt-0.5 block truncate text-[12px] text-muted">{notice.text}</span>
        <span className="mt-1 flex min-w-0 items-center gap-1 text-[11px] text-subtle">
          <FolderGit2 className="size-3 shrink-0" />
          <span className="max-w-[45%] shrink-0 truncate">{projectName}</span>
          <ChevronRight className="size-3 shrink-0 text-faint" />
          <span className="truncate">{agent?.title || name}</span>
        </span>
      </span>
      <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', notice.seen ? 'bg-transparent' : 'bg-brand-400')} aria-label={notice.seen ? undefined : 'Unseen'} />
    </button>
  );
}

function NotificationBell({
  notices,
  defaultOpen,
  onOpen,
  onReadAll,
  onAllMedia,
}: {
  notices: Notice[];
  defaultOpen: boolean;
  onOpen: (notice: Notice) => void;
  onReadAll: () => void;
  onAllMedia: () => void;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const unseen = notices.filter((n) => !n.seen).length;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          aria-label={unseen ? `Notifications, ${unseen} new` : 'Notifications'}
          data-bell
          className={cn(
            'relative flex size-8 items-center justify-center rounded-full border border-line bg-surface-faint text-muted transition hover:bg-surface-raised hover:text-primary',
            open && 'bg-surface-raised text-primary',
          )}
        >
          <Bell className="size-4" />
          {unseen > 0 && (
            <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-500 px-1 text-[10px] font-semibold tabular-nums text-white ring-2 ring-[var(--color-ink)]">
              {unseen > 99 ? '99+' : unseen}
            </span>
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[min(420px,calc(100vw-2rem))] p-0" data-bell-history onOpenAutoFocus={(e) => e.preventDefault()}>
        <div className="flex items-center gap-2 border-b border-line px-4 py-3">
          <span className="text-[14px] font-medium text-title">Notifications</span>
          {unseen > 0 && <span className="rounded-full bg-brand-500/15 px-2 py-0.5 text-[11px] font-medium text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">{unseen} new</span>}
          <Button size="sm" variant="ghost" className="ml-auto" disabled={unseen === 0} onClick={onReadAll}>
            <CheckCheck />
            Mark all read
          </Button>
        </div>
        <div className="max-h-[min(560px,70vh)] overflow-y-auto p-1.5">
          {byDay(notices, (n) => n.at).map(([day, list]) => (
            <div key={day}>
              <div className="px-2.5 pb-1 pt-2.5 text-[11px] font-medium uppercase tracking-wide text-subtle">{day}</div>
              {list.map((n) => (
                <NoticeRow
                  key={n.id}
                  notice={n}
                  onOpen={() => {
                    setOpen(false);
                    onOpen(n);
                  }}
                />
              ))}
            </div>
          ))}
        </div>
        <div className="flex items-center border-t border-line px-4 py-2 text-[12px] text-subtle">
          Kept for 30 days
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto"
            onClick={() => {
              setOpen(false);
              onAllMedia();
            }}
          >
            <Images />
            All media
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

// ---- The all-projects Media view ----

function AllMediaView({ items, seen, onSeen, onOpen }: { items: T.MediaItem[]; seen: ReadonlySet<string>; onSeen: (ids: string[]) => void; onOpen: (item: T.MediaItem, list: T.MediaItem[]) => void }) {
  const projects = useQuery<T.Project[]>({ queryKey: ['projects'], enabled: false });
  const [project, setProject] = useState('');
  const [agent, setAgent] = useState('');
  const [kind, setKind] = useState('');
  const [unseenOnly, setUnseenOnly] = useState(false);

  const count = (pick: (m: T.MediaItem) => string) => {
    const counts = new Map<string, number>();
    for (const m of items) counts.set(pick(m), (counts.get(pick(m)) ?? 0) + 1);
    return counts;
  };
  const projectCounts = count((m) => m.agent.split('/')[0]);
  const inProject = items.filter((m) => !project || m.agent.startsWith(`${project}/`));
  const agentCounts = new Map<string, number>();
  for (const m of inProject) agentCounts.set(m.agent, (agentCounts.get(m.agent) ?? 0) + 1);
  const visible = inProject.filter((m) => (!agent || m.agent === agent) && (!kind || m.kind === kind) && (!unseenOnly || !seen.has(m.id)));
  const unseen = items.filter((m) => !seen.has(m.id)).length;

  return (
    <div className="h-full overflow-y-auto px-4 py-6 md:px-8" data-all-media>
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <h1 className="text-[20px] font-semibold text-title">Media</h1>
            <p className="mt-0.5 text-[13px] text-muted">Every agent's screenshots and recordings, in every project, newest first.</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            {unseen > 0 && (
              <Button size="sm" variant="ghost" onClick={() => onSeen(items.map((m) => m.id))}>
                <Check />
                Mark {unseen} seen
              </Button>
            )}
          </div>
        </div>

        <div className="panel flex flex-col gap-1.5 rounded-xl p-2">
          <div className="flex flex-wrap items-center gap-1" data-media-projects>
            <FilterChip icon={Layers} active={!project} count={items.length} onClick={() => (setProject(''), setAgent(''))}>
              All projects
            </FilterChip>
            {[...projectCounts].map(([name, n]) => (
              <FilterChip key={name} icon={FolderGit2} active={project === name} count={n} onClick={() => (setProject(project === name ? '' : name), setAgent(''))}>
                <span className="max-w-[220px] truncate">{projectLabel(name, projects.data)}</span>
              </FilterChip>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-1" data-media-agents>
            <FilterChip active={!agent} count={inProject.length} onClick={() => setAgent('')}>
              All agents
            </FilterChip>
            {[...agentCounts].map(([ref, n]) => (
              <FilterChip key={ref} active={agent === ref} count={n} onClick={() => setAgent(agent === ref ? '' : ref)}>
                {!project && <span className="max-w-[110px] truncate text-subtle">{projectLabel(ref.split('/')[0], projects.data)} /</span>}
                {ref.split('/')[1]}
              </FilterChip>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-1" data-media-kinds>
            <FilterChip active={!kind} count={inProject.length} onClick={() => setKind('')}>
              Everything
            </FilterChip>
            <FilterChip icon={ImageIcon} active={kind === 'screenshot'} count={inProject.filter((m) => m.kind === 'screenshot').length} onClick={() => setKind(kind === 'screenshot' ? '' : 'screenshot')}>
              Screenshots
            </FilterChip>
            <FilterChip icon={Video} active={kind === 'recording'} count={inProject.filter((m) => m.kind === 'recording').length} onClick={() => setKind(kind === 'recording' ? '' : 'recording')}>
              Recordings
            </FilterChip>
            <span className="mx-1 h-4 w-px bg-line" />
            <FilterChip active={unseenOnly} count={unseen} onClick={() => setUnseenOnly(!unseenOnly)}>
              <span className="size-1.5 rounded-full bg-brand-400" />
              Unseen
            </FilterChip>
          </div>
        </div>

        {byDay(visible, (m) => m.createdAt).map(([day, list]) => (
          <section key={day} className="flex flex-col gap-2.5">
            <h2 className="flex items-baseline gap-2 px-1 text-[13px] font-medium text-secondary">
              {day}
              <span className="text-[12px] font-normal text-subtle">{list.length}</span>
            </h2>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {list.map((m) => (
                <div key={m.id} className={cn('relative rounded-xl', !seen.has(m.id) && 'ring-2 ring-brand-400/60 ring-offset-2 ring-offset-[var(--color-ink)]')} data-unseen={!seen.has(m.id) || undefined}>
                  <MediaCard item={m} label={`${projectLabel(m.agent.split('/')[0], projects.data)} · ${m.agentName}`} onOpen={() => onOpen(m, visible)} />
                  {!seen.has(m.id) && <span className="pointer-events-none absolute -right-1.5 -top-1.5 rounded-full bg-brand-500 px-1.5 py-px text-[10px] font-semibold text-white">New</span>}
                </div>
              ))}
            </div>
          </section>
        ))}
        {visible.length === 0 && <div className="py-16 text-center text-[13px] text-muted">Nothing matches these filters.</div>}
      </div>
    </div>
  );
}

// ---- The viewer, with where its item came from ----

const kindVariant: Record<string, BadgeVariant> = { screenshot: 'brand', recording: 'danger' };
const prVariant: Record<string, BadgeVariant> = { open: 'success', merged: 'brand', draft: 'default' };

function PlaceLinks({ item, fromNotice, onGo }: { item: T.MediaItem; fromNotice?: string; onGo: (view: MockView) => void }) {
  const { project, name, projectName, agent } = usePlace(item.agent);
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 border-b border-line bg-surface-faint px-5 py-2 text-[12.5px]" data-viewer-links>
      <button className="flex min-w-0 max-w-[30%] items-center gap-1.5 rounded-md px-1.5 py-0.5 text-muted transition hover:bg-surface-raised hover:text-primary" onClick={() => onGo({ kind: 'project', project })} title={`Open ${projectName}`}>
        <FolderGit2 className="size-3.5 shrink-0" />
        <span className="truncate">{projectName}</span>
      </button>
      <ChevronRight className="size-3.5 shrink-0 text-faint" />
      <button className="flex min-w-0 max-w-[45%] items-center gap-1.5 rounded-md px-1.5 py-0.5 text-secondary transition hover:bg-surface-raised hover:text-primary" onClick={() => onGo({ kind: 'agent', ref: item.agent, tab: 'chat' })} title={`Open ${name}`}>
        <span className="font-mono text-[12px]">{name}</span>
        {agent?.title && <span className="truncate text-muted">· {agent.title}</span>}
      </button>
      {agent?.pr && (
        <button
          className="flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-muted transition hover:bg-surface-raised hover:text-primary"
          onClick={() => toast(`Opens github.com/…/pull/${agent.pr.number}`)}
          title="Open the pull request on GitHub"
        >
          <GitPullRequest className="size-3.5" />#{agent.pr.number}
          <Badge variant={prVariant[agent.pr.state]} className="ml-0.5">
            {agent.pr.state}
          </Badge>
          <ExternalLink className="size-3 text-subtle" />
        </button>
      )}
      {fromNotice && <span className="ml-auto shrink-0 text-[11.5px] text-subtle">From a notification · {fromNotice}</span>}
    </div>
  );
}

function MockViewer({
  items,
  index,
  onIndex,
  onClose,
  onGo,
  fromNotice,
}: {
  items: T.MediaItem[];
  index: number;
  onIndex: (i: number) => void;
  onClose: () => void;
  onGo: (view: MockView) => void;
  fromNotice?: string;
}) {
  const item = index >= 0 ? items[index] : undefined;
  return (
    <Dialog open={item !== undefined} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-[min(1240px,calc(100vw-4rem))] gap-0 overflow-hidden p-0" onOpenAutoFocus={(e) => e.preventDefault()} data-mock-viewer>
        {item && (
          <>
            <div className="flex items-center gap-3 border-b border-line px-5 py-3 pr-14">
              <Badge variant={kindVariant[item.kind]}>{kindInfo(item.kind).one}</Badge>
              <DialogTitle className="truncate text-[15px]">{item.name}</DialogTitle>
              <span className="shrink-0 text-xs text-subtle">from the agent · {new Date(item.createdAt).toLocaleString()}</span>
              <div className="ml-auto flex shrink-0 items-center gap-0.5">
                <Tip label="Previous">
                  <Button size="icon-sm" variant="ghost" aria-label="Previous" disabled={index === 0} onClick={() => onIndex(index - 1)}>
                    <ChevronLeft />
                  </Button>
                </Tip>
                <Tip label="Next">
                  <Button size="icon-sm" variant="ghost" aria-label="Next" disabled={index === items.length - 1} onClick={() => onIndex(index + 1)}>
                    <ChevronRight />
                  </Button>
                </Tip>
                <Tip label="Show in folder">
                  <Button size="icon-sm" variant="ghost" aria-label="Show in folder">
                    <FolderOpen />
                  </Button>
                </Tip>
                <Tip label="Delete">
                  <Button size="icon-sm" variant="danger" aria-label="Delete">
                    <Trash />
                  </Button>
                </Tip>
              </div>
            </div>
            <PlaceLinks item={item} fromNotice={fromNotice} onGo={onGo} />
            <div className="flex max-h-[70vh] min-h-[46vh] items-center justify-center overflow-auto bg-well">
              {item.kind === 'screenshot' ? (
                <img src={mediaUrl(item)} alt={item.name} className="max-h-[70vh] w-auto max-w-full object-contain" />
              ) : (
                <div className="flex aspect-video w-full max-w-4xl flex-col items-center justify-center gap-2 bg-black text-white/70">
                  <Video className="size-8" />
                  <span className="font-mono text-xs">{clock(item.meta.duration ?? 0)}</span>
                </div>
              )}
            </div>
            <div className="flex items-center gap-3 border-t border-line px-5 py-2 text-xs text-subtle">
              {item.meta.width ? (
                <span className="tabular-nums">
                  {item.meta.width}×{item.meta.height}
                </span>
              ) : null}
              <span className="ml-auto">
                {index + 1} of {items.length}
              </span>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ---- The toast ----

function NoticeToast({ notice, onOpen }: { notice: Notice; onOpen: () => void }) {
  const { name, projectName, agent } = usePlace(notice.agent);
  return (
    <button
      onClick={onOpen}
      data-toast-notice
      className="flex w-[356px] items-start gap-3 rounded-xl border border-line-strong bg-overlay p-3 text-left shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur transition hover:border-line-vivid"
    >
      <NoticeArt notice={notice} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-title">{headline(notice, name)}</span>
        <span className="mt-0.5 block truncate text-[12px] text-muted">{notice.text}</span>
        <span className="mt-1 block truncate text-[11px] text-subtle">
          {projectName} · {agent?.title}
        </span>
      </span>
      <span className="shrink-0 self-center text-[11.5px] font-medium text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">View</span>
    </button>
  );
}

// ---- The shell ----

type MockView = { kind: 'home' } | { kind: 'media' } | { kind: 'project'; project: string } | { kind: 'agent'; ref: string; tab?: AgentPlaceName };

export function NotificationsPreview({ scenario }: { scenario: string }) {
  const media = useMemo(() => mockMedia(), []);
  const [notices, setNotices] = useState(() => mockNotices(media));
  const [seen, setSeen] = useState<ReadonlySet<string>>(() => new Set(media.slice(6).map((m) => m.id)));
  const first = notices.find((n) => n.media && n.media.kind === 'screenshot' && !n.seen)!;
  const [view, setView] = useState<MockView>(scenario === 'media' ? { kind: 'media' } : scenario === 'viewer' ? { kind: 'agent', ref: first.agent, tab: 'media' } : { kind: 'home' });
  const [viewing, setViewing] = useState<{ list: T.MediaItem[]; id: string; from?: string } | null>(() =>
    scenario === 'viewer' ? { list: media.filter((m) => m.agent === first.agent), id: first.media!.id, from: timeAgo(first.at) } : null,
  );

  // Going where a notification points: its agent, at its media item or its chat.
  const open = (n: Notice) => {
    setNotices((ns) => ns.map((x) => (x.id === n.id ? { ...x, seen: true } : x)));
    if (n.media) {
      setSeen((s) => new Set([...s, n.media!.id]));
      setView({ kind: 'agent', ref: n.agent, tab: 'media' });
      setViewing({ list: media.filter((m) => m.agent === n.agent), id: n.media.id, from: timeAgo(n.at) });
    } else {
      setView({ kind: 'agent', ref: n.agent, tab: n.kind === 'question' ? 'chat' : 'overview' });
    }
  };

  useEffect(() => {
    if (scenario !== 'toast') return;
    const n = notices.find((x) => x.kind === 'media' && x.media?.kind === 'screenshot')!;
    const id = toast.custom((t) => <NoticeToast notice={n} onOpen={() => (toast.dismiss(t), open(n))} />, { duration: Infinity });
    return () => void toast.dismiss(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const appView: View =
    view.kind === 'agent' ? { kind: 'agent', ref: view.ref } : view.kind === 'project' ? { kind: 'project', project: view.project } : view.kind === 'home' ? { kind: 'home' } : ({ kind: 'media' } as unknown as View);
  const select = (v: View) => {
    if (v.kind === 'agent') setView({ kind: 'agent', ref: v.ref, tab: v.tab });
    else if (v.kind === 'project') setView({ kind: 'project', project: v.project });
    else setView({ kind: 'home' });
  };
  const index = viewing ? viewing.list.findIndex((m) => m.id === viewing.id) : -1;
  const unseenMedia = media.filter((m) => !seen.has(m.id)).length;

  return (
    <div className="flex h-screen w-screen" style={{ background: 'var(--color-ink)', font: '13px var(--font-sans)' }} data-preview-notify={scenario}>
      <Sidebar view={appView} onSelect={select} onAddProject={() => {}} onNewAgent={() => {}} />
      <Inject find={() => [...document.querySelectorAll('aside button, nav button, button')].find((b) => b.textContent?.startsWith('Main chat'))} place="after">
        <button
          onClick={() => setView({ kind: 'media' })}
          data-nav-media
          className={cn(
            'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] text-muted transition-colors hover:bg-surface hover:text-primary',
            view.kind === 'media' && 'bg-surface-raised text-title',
          )}
        >
          <Images className="size-4" />
          Media
          {unseenMedia > 0 && <span className="ml-auto rounded-full bg-brand-500/15 px-1.5 text-[11px] tabular-nums text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">{unseenMedia}</span>}
        </button>
      </Inject>
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar view={appView} onSelect={select} onOpenNav={() => {}} onNewAgent={() => {}} />
        <Inject find={() => document.querySelector('header > .ml-auto')} place="prepend">
          <NotificationBell
            notices={notices}
            defaultOpen={scenario === 'bell'}
            onOpen={open}
            onReadAll={() => setNotices((ns) => ns.map((n) => ({ ...n, seen: true })))}
            onAllMedia={() => setView({ kind: 'media' })}
          />
        </Inject>
        {view.kind === 'media' && (
          <Inject find={() => document.querySelector('header nav[aria-label="Breadcrumb"]')} place="prepend">
            <span className="truncate font-medium text-primary">Media</span>
          </Inject>
        )}
        <main className="min-h-0 flex-1">
          <Suspense>
            {view.kind === 'home' && <HomeView onSelect={select} onAddProject={() => {}} onNewAgent={() => {}} />}
            {view.kind === 'media' && (
              <AllMediaView
                items={media}
                seen={seen}
                onSeen={(ids) => setSeen((s) => new Set([...s, ...ids]))}
                onOpen={(m, list) => {
                  setSeen((s) => new Set([...s, m.id]));
                  setViewing({ list, id: m.id });
                }}
              />
            )}
            {view.kind === 'project' && <ProjectView key={view.project} name={view.project} tab="media" onSelect={select} onNewAgent={() => {}} />}
            {view.kind === 'agent' && <AgentView key={view.ref} agentRef={view.ref} tab={view.tab} onTab={(tab) => setView({ ...view, tab })} onSelect={select} />}
          </Suspense>
        </main>
      </div>
      <MockViewer
        items={viewing?.list ?? []}
        index={index}
        onIndex={(i) => setViewing((v) => v && { ...v, id: v.list[i].id })}
        onClose={() => setViewing(null)}
        onGo={(v) => {
          setViewing(null);
          setView(v);
        }}
        fromNotice={viewing?.from}
      />
    </div>
  );
}
