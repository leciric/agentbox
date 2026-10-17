// Concept: evidence first. What an agent shows it checked (its desktop
// recording, its screenshots, the pages it published) is today two clicks
// away, in the right panel's Media tab or the project's Media. Here it leads:
//
// - The lead chat: when an agent finishes, the lead's message carries an
//   evidence card. The recording plays muted in a thumbnail, naming the check
//   it is on; three screenshots; the PR with its checks; Review and Merge.
// - The review modal opens on Proof: the recording at the top with a chapter
//   per thing the agent checked (click one to jump), the screenshots as a
//   strip under it, each tied to its chapter, then the agent's summary.
//   Changes (the diff) and the PR come second, as tabs.
//
// Navigation stays lead-first (Home, each project's lead, its agents under
// it), and review stays a modal over the chat.
//
// Chapters don't exist today: an agent would mark them while recording
// (`agentbox media record chapter "<what it checks>"`), and a screenshot
// taken during a recording would carry its time. The fixture media in
// media/ is the dev preview itself, recorded at 1024×640.
//
// ?concept=evidence and, for screenshots:
//   &modal=agent-412|agent-415|agent-417   that agent's review open
//   &tab=proof|changes|pr                  on that tab (default: proof)
//   &shot=<n>                              screenshot n (1-based) enlarged
//   &merged=agent-412                      that card already merged
// Mock only: nothing here ships or is translated.
import {
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  Crown,
  Film,
  FileCode,
  GitBranch,
  GitMerge,
  GitPullRequest,
  Globe,
  House,
  Images,
  Pause,
  Play,
  Plus,
  Send,
  X,
} from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { AgentAvatar } from '../../components/state';
import { Button } from '../../components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '../../components/ui/dialog';
import { cn } from '../../lib/utils';

// ——— The fixtures ———

const media = (name: string) => new URL(`./media/${name}`, import.meta.url).href;

interface Chapter {
  at: number; // seconds into the recording
  title: string;
  note: string;
}
interface Shot {
  src: string;
  caption: string;
  chapter: number; // index into chapters
}
interface DiffFile {
  path: string;
  add: number;
  del: number;
  hunk: string[]; // lines starting with '+', '-' or ' '
}
interface Evidence {
  agent: string;
  ai: string;
  title: string;
  branch: string;
  finished: string;
  summary: string;
  lead: string; // what the lead says above the card
  video: string;
  poster: string;
  length: number;
  chapters: Chapter[];
  shots: Shot[];
  page?: { title: string; url: string };
  pr: { number: number; checks: { name: string; ok: boolean; time: string }[] };
  files: DiffFile[];
}

const evidence: Evidence[] = [
  {
    agent: 'agent-412',
    ai: 'claude',
    title: 'Settings tabs no longer clip at 1024px',
    branch: 'agentbox/settings-tabs-clip',
    finished: '10:42',
    lead: 'agent-412 finished item 8 of the UX report: Settings’ tabs fit at 1024px, in pt-BR and at 800×600. Its recording walks all four.',
    summary:
      'The section list was a fixed 248px grid column with nowrap labels, so pt-BR’s longer names pushed the page past the window. It is now minmax(180px, 248px) and labels wrap to two lines. Checked dark and light, en-US and pt-BR, at 1024×640 and 800×600.',
    video: media('settings.webm'),
    poster: media('settings-poster.jpg'),
    length: 17,
    chapters: [
      { at: 0, title: 'General at 1024×640', note: 'Nothing past the right edge; the search box keeps its width.' },
      { at: 5.2, title: 'Models in pt-BR', note: 'The longest labels wrap to two lines instead of clipping.' },
      { at: 9.5, title: 'Resources, light theme', note: 'Same columns in light; the meters keep their labels.' },
      { at: 13.5, title: 'Accounts at 800×600', note: 'The narrowest window the app allows: no horizontal scroll.' },
    ],
    shots: [
      { src: media('settings-1.webp'), caption: 'General, 1024×640', chapter: 0 },
      { src: media('settings-2.webp'), caption: 'Models in pt-BR', chapter: 1 },
      { src: media('settings-3.webp'), caption: 'Resources, light', chapter: 2 },
      { src: media('settings-4.webp'), caption: 'Accounts, 800×600', chapter: 3 },
    ],
    pr: {
      number: 371,
      checks: [
        { name: 'PR title', ok: true, time: '4s' },
        { name: 'Desktop', ok: true, time: '2m 12s' },
        { name: 'Go tests and coverage', ok: true, time: 'skipped' },
      ],
    },
    files: [
      {
        path: 'desktop/src/renderer/components/SettingsPage.tsx',
        add: 6,
        del: 4,
        hunk: [
          ' export function SettingsPage({ section }: { section: Section }) {',
          '   return (',
          '-    <div className="grid h-full grid-cols-[248px_1fr]">',
          '+    <div className="grid h-full grid-cols-[minmax(180px,248px)_minmax(0,1fr)]">',
          '       <nav className="overflow-y-auto border-r border-line p-2">',
          '-        {sections.map((s) => <NavRow key={s.id} {...s} className="whitespace-nowrap" />)}',
          '+        {sections.map((s) => (',
          '+          <NavRow key={s.id} {...s} className="min-w-0 text-balance leading-tight" />',
          '+        ))}',
          '       </nav>',
        ],
      },
      {
        path: 'desktop/src/renderer/dev/scenarios.json',
        add: 14,
        del: 0,
        hunk: [' [', '+  { "id": "settings-1024-pt", "query": "settings=models&lang=pt-BR", "width": 1024, "height": 640 },', '+  { "id": "settings-800", "query": "settings=accounts", "width": 800, "height": 600 },'],
      },
    ],
  },
  {
    agent: 'agent-415',
    ai: 'codex',
    title: 'Markdown renders in Ctrl+K and sidebar previews',
    branch: 'agentbox/markdown-previews',
    finished: '10:51',
    lead: 'agent-415 finished item 5: previews render markdown instead of showing it raw. One check fails, a lint rule on an unused import; I’ve asked it to fix that.',
    summary:
      'Previews went through a plain-text truncate. They now go through Markdown’s inline renderer (no blocks, no images), clamped to two lines, so **bold** and `code` read as such. Checked the lead chat, a 340px column and the sidebar.',
    video: media('markdown.webm'),
    poster: media('markdown-poster.jpg'),
    length: 14,
    chapters: [
      { at: 0, title: 'Rich blocks in the lead chat', note: 'Headings, code and tables render as before.' },
      { at: 5.7, title: 'The same in a 340px column', note: 'Long code lines scroll inside their block.' },
      { at: 9.8, title: 'Sidebar previews in the project', note: 'Inline markdown, two lines, no raw asterisks.' },
    ],
    shots: [
      { src: media('markdown-1.webp'), caption: 'Lead chat', chapter: 0 },
      { src: media('markdown-2.webp'), caption: '340px column', chapter: 1 },
      { src: media('markdown-3.webp'), caption: 'Sidebar previews', chapter: 2 },
    ],
    pr: {
      number: 372,
      checks: [
        { name: 'PR title', ok: true, time: '3s' },
        { name: 'Desktop', ok: false, time: '1m 48s' },
        { name: 'Go tests and coverage', ok: true, time: 'skipped' },
      ],
    },
    files: [
      {
        path: 'desktop/src/renderer/components/v2/SidebarV2.tsx',
        add: 3,
        del: 2,
        hunk: [
          ' import { cn } from "../../lib/utils";',
          '+import { InlineMarkdown } from "../chat/Markdown";',
          '+import { useMemo } from "react";',
          '   <span className="line-clamp-2 text-[12px] text-subtle">',
          '-    {plain(preview)}',
          '+    <InlineMarkdown text={preview} />',
          '   </span>',
        ],
      },
    ],
  },
  {
    agent: 'agent-417',
    ai: 'claude',
    title: 'Draft: this week’s demo as a page',
    branch: 'agentbox/weekly-demo-draft',
    finished: '11:03',
    lead: 'agent-417 drafted the weekly demo as a Hatch page, from this week’s merged PRs and their recordings. Have a look before it’s shared.',
    summary:
      'Gathered the recordings of the 9 PRs merged since Monday, ordered by area, and published them as one private Hatch page with a caption each. The Media view groups by PR for this week.',
    video: media('pages.webm'),
    poster: media('pages-poster.jpg'),
    length: 13,
    chapters: [
      { at: 0, title: 'The page arrives in the lead chat', note: 'Its stack at the composer’s corner; Pages tab updated.' },
      { at: 4.7, title: 'Media, grouped by this week’s PRs', note: 'Each merged PR with its recording and screenshots.' },
      { at: 8.9, title: 'Home’s overview links it', note: 'One “This week” entry, private until shared.' },
    ],
    shots: [
      { src: media('pages-1.webp'), caption: 'The page arriving', chapter: 0 },
      { src: media('pages-2.webp'), caption: 'Media by PR', chapter: 1 },
      { src: media('pages-3.webp'), caption: 'Home', chapter: 2 },
    ],
    page: { title: 'AgentBox — week of 12 October', url: 'usehatch.dev/p/k3x9w2' },
    pr: {
      number: 373,
      checks: [
        { name: 'PR title', ok: true, time: '3s' },
        { name: 'Desktop', ok: true, time: '2m 40s' },
      ],
    },
    files: [
      {
        path: 'desktop/src/renderer/components/AllMediaView.tsx',
        add: 42,
        del: 9,
        hunk: [' export function AllMediaView() {', '-  const groups = byAgent(items);', '+  const groups = week ? byMergedPR(items, week) : byAgent(items);', '+  // This week: one group per PR merged since Monday, its recording first.'],
      },
    ],
  },
];

const byAgent = (agent: string | null) => evidence.find((e) => e.agent === agent);
const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`;
const chapterAt = (e: Evidence, t: number) => e.chapters.reduce((n, c, i) => (t >= c.at - 0.05 ? i : n), 0);
const totals = (e: Evidence) => ({ add: e.files.reduce((n, f) => n + f.add, 0), del: e.files.reduce((n, f) => n + f.del, 0) });
const failing = (e: Evidence) => e.pr.checks.filter((c) => !c.ok);

// ——— The concept ———

const params = new URLSearchParams(location.search);

export function EvidenceConcept() {
  const [open, setOpen] = useState<{ agent: string; tab: Tab; shot: number | null } | null>(() => {
    const agent = params.get('modal');
    if (!byAgent(agent)) return null;
    const shot = Number(params.get('shot'));
    return { agent: agent!, tab: (params.get('tab') as Tab) || 'proof', shot: shot > 0 ? shot - 1 : null };
  });
  const [merged, setMerged] = useState<string[]>(() => params.get('merged')?.split(',') ?? []);
  const merge = (agent: string) => setMerged((m) => [...m, agent]);
  const waiting = evidence.filter((e) => !merged.includes(e.agent));

  return (
    <div className="flex h-screen min-w-0 bg-chat text-primary">
      <Sidebar waiting={waiting.length} onReview={() => waiting[0] && setOpen({ agent: waiting[0].agent, tab: 'proof', shot: null })} merged={merged} />
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-5">
          <Crown className="size-4 text-amber-300" />
          <span className="font-medium text-title">agentbox</span>
          <span className="text-subtle">Lead</span>
          {waiting.length > 0 && (
            <button
              type="button"
              onClick={() => setOpen({ agent: waiting[0].agent, tab: 'proof', shot: null })}
              className="ml-auto inline-flex items-center gap-1.5 rounded-full border border-line-strong bg-surface px-2.5 py-1 text-[12px] text-secondary hover:bg-surface-strong hover:text-title"
            >
              <Film className="size-3.5" />
              {waiting.length} to review
            </button>
          )}
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto flex max-w-3xl flex-col gap-6 px-5 py-6">
            <UserMessage>Take items 5 and 8 from the UX report, and draft this week’s demo from what we merged.</UserMessage>
            <LeadMessage time="10:21">
              Started three agents: <b>agent-412</b> on Settings’ clipped tabs, <b>agent-415</b> on raw markdown in previews, and <b>agent-417</b> on the weekly demo draft. Each records what it checks; I’ll bring you the proof as they finish.
            </LeadMessage>
            {evidence.map((e) => (
              <LeadMessage key={e.agent} time={e.finished}>
                <p className="mb-3">{e.lead}</p>
                <EvidenceCard
                  e={e}
                  merged={merged.includes(e.agent)}
                  onOpen={(shot) => setOpen({ agent: e.agent, tab: 'proof', shot })}
                  onMerge={() => merge(e.agent)}
                />
              </LeadMessage>
            ))}
          </div>
        </div>
        <div className="mx-auto w-full max-w-3xl px-5 pb-5">
          <div className="flex items-center gap-2 rounded-2xl border border-line-strong bg-composer px-4 py-3 text-subtle">
            <span className="flex-1">Ask the lead…</span>
            <Send className="size-4" />
          </div>
        </div>
      </main>
      {open && (
        <ReviewModal
          key={open.agent}
          e={byAgent(open.agent)!}
          tab={open.tab}
          shot={open.shot}
          queue={evidence}
          merged={merged.includes(open.agent)}
          onTab={(tab) => setOpen({ ...open, tab })}
          onShot={(shot) => setOpen({ ...open, shot })}
          onGo={(agent) => setOpen({ agent, tab: 'proof', shot: null })}
          onMerge={() => {
            // Merged, then on to the next agent still waiting, as the carousel does.
            merge(open.agent);
            const next = waiting.find((w) => w.agent !== open.agent);
            setTimeout(() => (next ? setOpen({ agent: next.agent, tab: 'proof', shot: null }) : setOpen(null)), 900);
          }}
          onClose={() => setOpen(null)}
        />
      )}
    </div>
  );
}

// ——— The sidebar: lead-first, as UI v2 ———

function Sidebar({ waiting, onReview, merged }: { waiting: number; onReview: () => void; merged: string[] }) {
  const row = 'flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-[13px]';
  return (
    <aside className="hidden w-64 shrink-0 flex-col border-r border-line bg-rail p-2 md:flex">
      <div className={cn(row, 'text-secondary')}>
        <House className="size-4" /> Home
      </div>
      <div className="mt-3 px-2 pb-1 text-[11px] font-medium uppercase tracking-wide text-faint">Projects</div>
      <div className={cn(row, 'bg-surface-strong text-title')}>
        <Crown className="size-4 text-amber-300" />
        <span className="flex-1 truncate">agentbox</span>
        {waiting > 0 && (
          <button type="button" onClick={onReview} className="rounded-full border border-line-strong bg-surface px-1.5 text-[10.5px] tabular-nums text-secondary hover:text-title">
            {waiting} to review
          </button>
        )}
      </div>
      <div className="ml-4 flex flex-col border-l border-line pl-1">
        {evidence.map((e) => (
          <div key={e.agent} className={cn(row, 'text-secondary')}>
            <span className={cn('size-1.5 shrink-0 rounded-full', merged.includes(e.agent) ? 'bg-ghost' : 'bg-sky-400')} />
            <span className="min-w-0 flex-1 truncate">{e.title}</span>
            <Film className="size-3.5 shrink-0 text-faint" />
          </div>
        ))}
        <div className={cn(row, 'text-secondary')}>
          <span className="size-1.5 shrink-0 rounded-full bg-emerald-400" />
          <span className="min-w-0 flex-1 truncate">End-to-end QA of the whole app</span>
        </div>
      </div>
      <div className={cn(row, 'mt-1 text-secondary')}>
        <Crown className="size-4 text-faint" />
        <span className="flex-1 truncate">hatch</span>
        <ChevronDown className="size-3.5 text-faint" />
      </div>
      <div className={cn(row, 'mt-auto text-subtle')}>
        <Plus className="size-4" /> New project
      </div>
    </aside>
  );
}

function UserMessage({ children }: { children: ReactNode }) {
  return <div className="ml-auto max-w-[80%] rounded-2xl bg-surface-strong px-4 py-2.5 text-title">{children}</div>;
}

function LeadMessage({ time, children }: { time: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 gap-3">
      <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-surface-strong">
        <Crown className="size-3.5 text-amber-300" />
      </span>
      <div className="min-w-0 flex-1 leading-relaxed text-primary">
        <div className="mb-1 text-[11.5px] text-faint">Lead · {time}</div>
        {children}
      </div>
    </div>
  );
}

// ——— The evidence card in the lead chat ———

function EvidenceCard({ e, merged, onOpen, onMerge }: { e: Evidence; merged: boolean; onOpen: (shot: number | null) => void; onMerge: () => void }) {
  const video = useRef<HTMLVideoElement>(null);
  const [t, setT] = useState(0);
  const { add, del } = totals(e);
  const fails = failing(e);
  const ch = chapterAt(e, t);
  return (
    <div className="@container overflow-hidden rounded-xl border border-line-strong bg-surface" data-evidence-card={e.agent}>
      <div className="flex flex-col @xl:flex-row">
        <button type="button" onClick={() => onOpen(null)} className="group relative aspect-[16/10] shrink-0 overflow-hidden bg-black @xl:w-[46%]" data-evidence-video>
          <video
            ref={video}
            src={e.video}
            poster={e.poster}
            autoPlay
            muted
            loop
            playsInline
            onTimeUpdate={(ev) => setT(ev.currentTarget.currentTime)}
            className="size-full object-cover transition group-hover:scale-[1.02]"
          />
          <span className="absolute inset-x-0 bottom-0 flex items-end gap-2 bg-gradient-to-t from-black/85 to-transparent px-3 pb-2 pt-8 text-left text-[11.5px] text-white">
            <span className="min-w-0 flex-1 truncate">
              <span className="text-white/60">
                {ch + 1}/{e.chapters.length}
              </span>{' '}
              {e.chapters[ch].title}
            </span>
            <span className="shrink-0 tabular-nums text-white/70">{clock(e.length)}</span>
          </span>
          {/* Progress per chapter, as stories do: one bar each. */}
          <span className="absolute inset-x-2 top-2 flex gap-1">
            {e.chapters.map((c, i) => {
              const end = e.chapters[i + 1]?.at ?? e.length;
              const p = Math.max(0, Math.min(1, (t - c.at) / (end - c.at)));
              return (
                <span key={c.at} className="h-0.5 flex-1 overflow-hidden rounded-full bg-white/25">
                  <span className="block h-full bg-white" style={{ width: `${p * 100}%` }} />
                </span>
              );
            })}
          </span>
          <span className="absolute left-1/2 top-1/2 flex size-11 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-black/55 text-white opacity-0 backdrop-blur transition group-hover:opacity-100">
            <Play className="ml-0.5 size-5" fill="currentColor" />
          </span>
        </button>
        <div className="flex min-w-0 flex-1 flex-col gap-2.5 p-3.5">
          <div className="flex min-w-0 items-center gap-2">
            <AgentAvatar ai={e.ai} mood="idle" seed={e.agent} className="size-5 shrink-0" />
            <span className="min-w-0 flex-1 truncate font-medium text-title">{e.title}</span>
          </div>
          <ul className="flex flex-col gap-1 text-[12.5px]">
            {e.chapters.map((c, i) => (
              <li key={c.at} className={cn('flex min-w-0 items-center gap-1.5', i === ch ? 'text-title' : 'text-secondary')}>
                <Check className="size-3.5 shrink-0 text-emerald-400" />
                <span className="truncate">{c.title}</span>
              </li>
            ))}
          </ul>
          {e.page && (
            <span className="inline-flex min-w-0 items-center gap-1.5 text-[12px] text-secondary">
              <Globe className="size-3.5 shrink-0 text-faint" />
              <span className="truncate">{e.page.title}</span>
            </span>
          )}
          <div className="mt-auto flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-subtle">
            <span className={cn('inline-flex items-center gap-1', merged ? 'text-violet-300' : 'text-secondary')}>
              {merged ? <GitMerge className="size-3.5" /> : <GitPullRequest className="size-3.5" />}#{e.pr.number}
            </span>
            <span className="tabular-nums">
              <span className="text-emerald-400">+{add}</span> <span className="text-rose-400">−{del}</span>
            </span>
            {fails.length ? (
              <span className="inline-flex items-center gap-1 text-rose-300">
                <CircleAlert className="size-3.5" />
                {fails[0].name} failing
              </span>
            ) : (
              <span className="inline-flex items-center gap-1">
                <CircleCheck className="size-3.5 text-emerald-400" />
                checks pass
              </span>
            )}
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t border-line p-2.5">
        <div className="flex min-w-0 flex-1 gap-1.5">
          {e.shots.slice(0, 3).map((s, i) => (
            <button
              key={s.src}
              type="button"
              title={s.caption}
              onClick={() => onOpen(i)}
              className="h-12 w-20 shrink-0 overflow-hidden rounded-md border border-line-strong bg-black transition hover:border-line-heavy"
            >
              <img src={s.src} alt={s.caption} className="size-full object-cover object-top" />
            </button>
          ))}
          {e.shots.length > 3 && (
            <button type="button" onClick={() => onOpen(3)} className="flex h-12 w-12 shrink-0 items-center justify-center rounded-md border border-line text-[12px] text-subtle hover:text-title">
              +{e.shots.length - 3}
            </button>
          )}
        </div>
        <Button size="sm" onClick={() => onOpen(null)} data-evidence-review>
          Review proof
        </Button>
        {merged ? (
          <span className="inline-flex h-8 items-center gap-1.5 px-2 text-[12.5px] text-violet-300">
            <GitMerge className="size-4" /> Merged
          </span>
        ) : (
          <Button size="sm" variant="primary" onClick={onMerge} disabled={fails.length > 0} title={fails.length ? `${fails[0].name} is failing` : undefined} data-evidence-merge>
            <GitMerge className="size-4" /> Merge
          </Button>
        )}
      </div>
    </div>
  );
}

// ——— The review modal, on Proof ———

type Tab = 'proof' | 'changes' | 'pr';

function ReviewModal({
  e,
  tab,
  shot,
  queue,
  merged,
  onTab,
  onShot,
  onGo,
  onMerge,
  onClose,
}: {
  e: Evidence;
  tab: Tab;
  shot: number | null;
  queue: Evidence[];
  merged: boolean;
  onTab: (t: Tab) => void;
  onShot: (n: number | null) => void;
  onGo: (agent: string) => void;
  onMerge: () => void;
  onClose: () => void;
}) {
  const { add, del } = totals(e);
  const fails = failing(e);
  const at = queue.findIndex((q) => q.agent === e.agent);
  const next = queue[at + 1];
  const prev = at > 0 ? queue[at - 1] : undefined;
  const tabs: [Tab, ReactNode][] = [
    [
      'proof',
      <>
        <Film className="size-3.5" /> Proof <span className="tabular-nums text-subtle">{e.chapters.length}</span>
      </>,
    ],
    [
      'changes',
      <>
        <FileCode className="size-3.5" /> Changes{' '}
        <span className="tabular-nums">
          <span className="text-emerald-400">+{add}</span> <span className="text-rose-400">−{del}</span>
        </span>
      </>,
    ],
    [
      'pr',
      <>
        <GitPullRequest className="size-3.5" /> #{e.pr.number}
        {fails.length > 0 && <CircleAlert className="size-3.5 text-rose-300" />}
      </>,
    ],
  ];
  return (
    <Dialog open onOpenChange={(o) => !o && (shot !== null ? onShot(null) : onClose())}>
      <DialogContent className="flex h-[88vh] max-w-6xl flex-col gap-0 overflow-hidden p-0" data-evidence-modal={e.agent}>
        <header className="flex shrink-0 flex-wrap items-center gap-3 border-b border-line px-5 py-3 pr-14">
          <AgentAvatar ai={e.ai} mood="idle" seed={e.agent} className="size-6 shrink-0" />
          <div className="min-w-0 flex-1">
            <DialogTitle className="truncate text-base">{e.title}</DialogTitle>
            <div className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-subtle">
              {e.agent} <GitBranch className="size-3 shrink-0" /> <span className="truncate">{e.branch}</span>
            </div>
          </div>
          <div role="tablist" className="flex shrink-0 items-center gap-0.5 rounded-lg border border-line bg-surface-faint p-0.5 text-[12px]">
            {tabs.map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={tab === id}
                onClick={() => onTab(id)}
                className={cn('inline-flex items-center gap-1.5 rounded-md px-2.5 py-1', tab === id ? 'bg-surface-strong text-title' : 'text-secondary hover:text-title')}
                data-evidence-tab={id}
              >
                {label}
              </button>
            ))}
          </div>
        </header>
        <div className="relative min-h-0 flex-1 overflow-y-auto">
          {tab === 'proof' && <Proof e={e} onShot={onShot} />}
          {tab === 'changes' && <Changes e={e} />}
          {tab === 'pr' && <PR e={e} />}
          {shot !== null && <Lightbox e={e} n={shot} onShot={onShot} />}
        </div>
        <footer className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line px-5 py-3">
          <Button size="sm" variant="ghost" disabled={!prev} onClick={() => prev && onGo(prev.agent)}>
            <ChevronLeft className="size-4" />
          </Button>
          <span className="text-[12px] tabular-nums text-subtle">
            {at + 1} of {queue.length}
          </span>
          <Button size="sm" variant="ghost" disabled={!next} onClick={() => next && onGo(next.agent)}>
            <ChevronRight className="size-4" />
          </Button>
          <span className="ml-auto" />
          {fails.length > 0 && !merged && <span className="text-[12px] text-rose-300">{fails[0].name} is failing</span>}
          <Button size="sm">Request changes</Button>
          <Button size="sm" onClick={() => (next ? onGo(next.agent) : onClose())}>
            Mark reviewed
          </Button>
          {merged ? (
            <span className="inline-flex h-8 items-center gap-1.5 px-2 text-[12.5px] text-violet-300">
              <GitMerge className="size-4" /> Merged #{e.pr.number}
            </span>
          ) : (
            <Button size="sm" variant="primary" disabled={fails.length > 0} onClick={onMerge} data-evidence-modal-merge>
              <GitMerge className="size-4" /> Merge #{e.pr.number}
            </Button>
          )}
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function Proof({ e, onShot }: { e: Evidence; onShot: (n: number) => void }) {
  const video = useRef<HTMLVideoElement>(null);
  const [t, setT] = useState(0);
  const [playing, setPlaying] = useState(true);
  const ch = chapterAt(e, t);
  const seek = (s: number) => {
    const v = video.current;
    if (!v) return;
    v.currentTime = s;
    void v.play();
  };
  const toggle = () => {
    const v = video.current;
    if (v) void (v.paused ? v.play() : v.pause());
  };
  useEffect(() => {
    const at = Number(params.get('t'));
    if (at && video.current) video.current.currentTime = at;
  }, []);
  return (
    <div className="grid min-h-full gap-5 p-5 lg:grid-cols-[minmax(0,1fr)_17rem]">
      <div className="flex min-w-0 flex-col gap-4">
        {/* At most half the window's height, so the strip shows under it. */}
        <div className="mx-auto w-full overflow-hidden rounded-xl border border-line-strong bg-black" style={{ maxWidth: 'calc(50vh * 1.6)' }}>
          <button type="button" onClick={toggle} className="relative block aspect-[16/10] w-full" data-evidence-player>
            <video
              ref={video}
              src={e.video}
              poster={e.poster}
              autoPlay
              muted
              playsInline
              onTimeUpdate={(ev) => setT(ev.currentTarget.currentTime)}
              onPlay={() => setPlaying(true)}
              onPause={() => setPlaying(false)}
              onEnded={() => setPlaying(false)}
              className="size-full object-contain"
            />
            <span className="absolute left-3 top-3 rounded-md bg-black/65 px-2 py-1 text-[12px] text-white backdrop-blur">
              <span className="text-white/60">
                {ch + 1}/{e.chapters.length}
              </span>{' '}
              {e.chapters[ch].title}
            </span>
          </button>
          <div className="flex items-center gap-3 bg-black px-3 py-2 text-white">
            <button type="button" onClick={toggle} className="text-white/85 hover:text-white">
              {playing ? <Pause className="size-4" fill="currentColor" /> : <Play className="size-4" fill="currentColor" />}
            </button>
            {/* The scrubber, cut into its chapters. */}
            <div className="flex h-5 flex-1 items-center gap-1">
              {e.chapters.map((c, i) => {
                const end = e.chapters[i + 1]?.at ?? e.length;
                const p = Math.max(0, Math.min(1, (t - c.at) / (end - c.at)));
                return (
                  <button
                    key={c.at}
                    type="button"
                    title={c.title}
                    onClick={(ev) => {
                      const r = ev.currentTarget.getBoundingClientRect();
                      seek(c.at + ((ev.clientX - r.left) / r.width) * (end - c.at));
                    }}
                    className="group flex h-full items-center"
                    style={{ flex: end - c.at }}
                  >
                    <span className={cn('block h-1 w-full overflow-hidden rounded-full bg-white/25 transition-all group-hover:h-1.5', i === ch && 'h-1.5')}>
                      <span className="block h-full bg-white" style={{ width: `${p * 100}%` }} />
                    </span>
                  </button>
                );
              })}
            </div>
            <span className="text-[11.5px] tabular-nums text-white/70">
              {clock(t)} / {clock(e.length)}
            </span>
          </div>
        </div>
        <div>
          <div className="mb-2 flex items-center gap-1.5 text-[12px] font-medium text-secondary">
            <Images className="size-3.5" /> Screenshots
          </div>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {e.shots.map((s, i) => (
              <button
                key={s.src}
                type="button"
                onClick={() => onShot(i)}
                className={cn(
                  'w-44 shrink-0 overflow-hidden rounded-lg border bg-surface text-left transition hover:border-line-heavy',
                  s.chapter === ch ? 'border-sky-400/70' : 'border-line-strong',
                )}
                data-evidence-shot={i + 1}
              >
                <img src={s.src} alt={s.caption} className="aspect-[16/10] w-full object-cover object-top" />
                <span className="block truncate px-2 py-1.5 text-[11.5px] text-secondary">{s.caption}</span>
              </button>
            ))}
          </div>
        </div>
        {e.page && (
          <div className="flex min-w-0 items-center gap-3 rounded-lg border border-line bg-surface px-3 py-2.5">
            <Globe className="size-4 shrink-0 text-faint" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-title">{e.page.title}</span>
              <span className="block truncate text-[11.5px] text-subtle">{e.page.url} · private</span>
            </span>
            <Button size="sm">Open page</Button>
          </div>
        )}
        <p className="leading-relaxed text-secondary">{e.summary}</p>
      </div>
      <aside className="min-w-0">
        <div className="mb-2 text-[12px] font-medium text-secondary">What it checked</div>
        <ol className="flex flex-col gap-1">
          {e.chapters.map((c, i) => (
            <li key={c.at}>
              <button
                type="button"
                onClick={() => seek(c.at)}
                className={cn('flex w-full gap-2.5 rounded-lg px-2.5 py-2 text-left transition', i === ch ? 'bg-surface-strong' : 'hover:bg-surface')}
                data-evidence-chapter={i + 1}
              >
                <span className={cn('mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full', i === ch ? 'bg-emerald-400/20 text-emerald-300' : 'text-emerald-400')}>
                  <Check className="size-3.5" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-baseline gap-2">
                    <span className={cn('min-w-0 flex-1 text-[13px]', i === ch ? 'text-title' : 'text-primary')}>{c.title}</span>
                    <span className="shrink-0 text-[11px] tabular-nums text-faint">{clock(c.at)}</span>
                  </span>
                  <span className="mt-0.5 block text-[12px] leading-snug text-subtle">{c.note}</span>
                </span>
              </button>
            </li>
          ))}
        </ol>
      </aside>
    </div>
  );
}

function Lightbox({ e, n, onShot }: { e: Evidence; n: number; onShot: (n: number | null) => void }) {
  const s = e.shots[n];
  useEffect(() => {
    const key = (ev: KeyboardEvent) => {
      if (ev.key === 'ArrowRight' && n < e.shots.length - 1) onShot(n + 1);
      if (ev.key === 'ArrowLeft' && n > 0) onShot(n - 1);
    };
    addEventListener('keydown', key);
    return () => removeEventListener('keydown', key);
  }, [e, n, onShot]);
  if (!s) return null;
  return (
    <div className="absolute inset-0 z-10 flex flex-col bg-black/95 p-4" data-evidence-lightbox={n + 1}>
      <div className="flex items-center gap-3 pb-3 text-[12.5px] text-white/80">
        <span className="tabular-nums text-white/50">
          {n + 1}/{e.shots.length}
        </span>
        <span className="min-w-0 flex-1 truncate">
          {s.caption} <span className="text-white/45">· {e.chapters[s.chapter].title}</span>
        </span>
        <button type="button" onClick={() => onShot(null)} className="rounded-md p-1 text-white/70 hover:bg-white/10 hover:text-white" aria-label="Close">
          <X className="size-4" />
        </button>
      </div>
      <div className="relative flex min-h-0 flex-1 items-center justify-center">
        <img src={s.src} alt={s.caption} className="max-h-full max-w-full rounded-md object-contain shadow-2xl" />
        {n > 0 && (
          <button type="button" onClick={() => onShot(n - 1)} className="absolute left-0 rounded-full bg-white/10 p-2 text-white hover:bg-white/20">
            <ChevronLeft className="size-5" />
          </button>
        )}
        {n < e.shots.length - 1 && (
          <button type="button" onClick={() => onShot(n + 1)} className="absolute right-0 rounded-full bg-white/10 p-2 text-white hover:bg-white/20">
            <ChevronRight className="size-5" />
          </button>
        )}
      </div>
    </div>
  );
}

function Changes({ e }: { e: Evidence }) {
  return (
    <div className="flex flex-col gap-4 p-5">
      {e.files.map((f) => (
        <div key={f.path} className="overflow-hidden rounded-lg border border-line">
          <div className="flex min-w-0 items-center gap-2 border-b border-line bg-surface px-3 py-2 text-[12px]">
            <FileCode className="size-3.5 shrink-0 text-faint" />
            <span className="min-w-0 flex-1 truncate font-mono text-secondary">{f.path}</span>
            <span className="tabular-nums">
              <span className="text-emerald-400">+{f.add}</span> <span className="text-rose-400">−{f.del}</span>
            </span>
          </div>
          <pre className="overflow-x-auto bg-sunken py-2 font-mono text-[12px] leading-relaxed">
            {f.hunk.map((l, i) => (
              <div key={i} className={cn('px-3', l[0] === '+' && 'bg-emerald-500/10 text-emerald-200', l[0] === '-' && 'bg-rose-500/10 text-rose-200', l[0] === ' ' && 'text-subtle')}>
                {l}
              </div>
            ))}
          </pre>
        </div>
      ))}
    </div>
  );
}

function PR({ e }: { e: Evidence }) {
  return (
    <div className="flex max-w-2xl flex-col gap-4 p-5">
      <div className="flex items-center gap-2 text-title">
        <GitPullRequest className="size-4 text-emerald-400" /> #{e.pr.number} {e.title}
      </div>
      <ul className="flex flex-col divide-y divide-line rounded-lg border border-line">
        {e.pr.checks.map((c) => (
          <li key={c.name} className="flex items-center gap-2 px-3 py-2 text-[12.5px]">
            {c.ok ? <CircleCheck className="size-4 text-emerald-400" /> : <CircleAlert className="size-4 text-rose-300" />}
            <span className="flex-1 text-primary">{c.name}</span>
            <span className="tabular-nums text-subtle">{c.time}</span>
          </li>
        ))}
      </ul>
      <p className="leading-relaxed text-secondary">{e.summary}</p>
    </div>
  );
}
