// ?concept=timeline: what an agent did, shown as a timeline of its steps
// (task, plan, edits, tests, desktop checks, Pages, the PR) over one
// continuous desktop recording, with each step's media inline. Scrubbing the
// timeline scrubs the recording. Lead-first (UI v2): the timeline sits in the
// right panel and in the review modal as a tab, and the lead's "Today" story
// stitches every agent's into one reel that plays as a slideshow.
//
//   &view=lead|today|agent-446   what the middle shows (default: lead)
//   &panel=agent-446             whose timeline the right panel shows; &panel=0 closes it
//   &review=agent-446            the review modal open, on its Timeline tab
//   &play=1                      the Today reel playing full screen (&slide=n)
//   &t=1140                      the recording's second to start at
//
// Mock only: nothing here ships or is translated.
import {
  Bookmark,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Clapperboard,
  FileCode,
  FileText,
  FlaskConical,
  GitMerge,
  GitPullRequest,
  ListTodo,
  Maximize2,
  MonitorPlay,
  Paperclip,
  Pause,
  Play,
  Route,
  Send,
  X,
} from 'lucide-react';
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AgentCharacter } from '../../components/AgentCharacter';
import { cn } from '../../lib/utils';
import { agentByName, agents, clock, length, type Media, mmss, shotSvg, type Step, type StepKind, stepAt, type TAgent, today } from './timelineData';

const params = new URLSearchParams(location.search);
const dark = () => document.documentElement.dataset.appearance !== 'light';

const kindIcon: Record<StepKind, typeof ListTodo> = {
  task: ListTodo,
  plan: Route,
  edit: FileCode,
  test: FlaskConical,
  desktop: MonitorPlay,
  page: FileText,
  pr: GitPullRequest,
};

// One hue per kind of step, the same on the scrubber, the rail and the reel.
const kindTone: Record<StepKind, string> = {
  task: 'bg-zinc-400',
  plan: 'bg-sky-400',
  edit: 'bg-violet-400',
  test: 'bg-emerald-400',
  desktop: 'bg-amber-400',
  page: 'bg-pink-400',
  pr: 'bg-brand-400',
};
const kindText: Record<StepKind, string> = {
  task: 'text-zinc-300',
  plan: 'text-sky-300',
  edit: 'text-violet-300',
  test: 'text-emerald-300',
  desktop: 'text-amber-300',
  page: 'text-pink-300',
  pr: 'text-brand-300',
};
const kindName: Record<StepKind, string> = { task: 'Task', plan: 'Plan', edit: 'Edit', test: 'Tests', desktop: 'Desktop', page: 'Page', pr: 'PR' };

const stateBadge: Record<TAgent['state'], [string, string]> = {
  review: ['Ready to review', 'bg-sky-500/15 text-sky-300'],
  merged: ['Merged', 'bg-violet-500/15 text-violet-300'],
  working: ['Working', 'bg-emerald-500/15 text-emerald-300'],
  draft: ['Draft', 'bg-zinc-500/15 text-zinc-300'],
};

function Avatar({ a, className }: { a: TAgent; className?: string }) {
  return <AgentCharacter ai={a.ai} mood={a.state === 'working' ? 'working' : 'idle'} seed={a.name} className={cn('size-6 shrink-0', className)} />;
}

// usePlayer is a recording's playhead: t in seconds, and play at `speed`
// times real time, stopping at `until`.
function usePlayer(a: TAgent, start = 0, speed = 24, until = length(a)) {
  const [t, setT] = useState(start);
  const [playing, setPlaying] = useState(false);
  useEffect(() => setT(start), [a, start]);
  useEffect(() => {
    if (!playing) return;
    let last = performance.now();
    let raf = requestAnimationFrame(function tick(now) {
      const dt = (now - last) / 1000;
      last = now;
      setT((v) => {
        const next = v + dt * speed;
        if (next >= until) {
          setPlaying(false);
          return until;
        }
        return next;
      });
      raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, [playing, speed, until]);
  // Play at the end starts over.
  const play = (on: boolean) => {
    if (on && t >= until) setT(0);
    setPlaying(on);
  };
  return { t, setT, playing, setPlaying: play };
}

// Frame is the desktop recording at second t: the terminal while the agent
// plans, edits and tests, the app (with its cursor) while it checks its work
// on the desktop, its Hatch page, and its pull request.
function Frame({ a, t, className, chrome = true }: { a: TAgent; t: number; className?: string; chrome?: boolean }) {
  const step = a.steps[stepAt(a, t)]!;
  const p = Math.min(1, Math.max(0, (t - step.at) / step.dur));
  let inner: ReactNode;
  if (step.kind === 'desktop') {
    const shots = (step.media ?? []).filter((m): m is Extract<Media, { kind: 'shot' }> => m.kind === 'shot');
    const shot = [...shots].reverse().find((m) => m.at <= t) ?? shots[0];
    // The cursor wanders between made-up targets, clicking at each.
    const pts = [[0.3, 0.4], [0.72, 0.82], [0.5, 0.3], [0.85, 0.9], [0.4, 0.6], [0.62, 0.5]] as const;
    const seg = p * (pts.length - 1);
    const k = Math.min(pts.length - 2, Math.floor(seg));
    const f = seg - k;
    const e = f < 0.5 ? 2 * f * f : 1 - (-2 * f + 2) ** 2 / 2;
    const x = pts[k]![0] + (pts[k + 1]![0] - pts[k]![0]) * e;
    const y = pts[k]![1] + (pts[k + 1]![1] - pts[k]![1]) * e;
    inner = (
      <>
        {shot && <img src={shotSvg(shot.screen, dark())} alt="" className="absolute inset-0 size-full object-cover" draggable={false} />}
        {f > 0.92 && <span className="absolute size-6 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white/80" style={{ left: `${x * 100}%`, top: `${y * 100}%` }} />}
        <svg viewBox="0 0 12 18" className="absolute size-3.5 drop-shadow" style={{ left: `${x * 100}%`, top: `${y * 100}%` }}>
          <path d="M0 0 L0 15 L4 11 L7 17 L9 16 L6 10 L11 10 Z" fill="#fff" stroke="#000" strokeWidth="1" />
        </svg>
      </>
    );
  } else if (step.kind === 'page') {
    const page = step.media?.find((m) => m.kind === 'page');
    inner = (
      <div className="absolute inset-0 flex flex-col bg-white text-zinc-900">
        <div className="flex items-center gap-2 border-b border-zinc-200 bg-zinc-100 px-3 py-1.5 text-[9px] text-zinc-500">
          <span className="size-1.5 rounded-full bg-zinc-300" />
          usehatch.dev/p/{page?.kind === 'page' ? page.id : ''}
        </div>
        <div className="flex-1 overflow-hidden px-[8%] py-[5%]">
          <p className="text-[clamp(8px,2.4cqw,22px)] font-semibold">{page?.kind === 'page' ? page.title : ''}</p>
          <div className="mt-[3%] grid grid-cols-3 gap-[3%]">
            {(['card', 'confirm', 'merged'] as const).map((s) => (
              <img key={s} src={shotSvg({ app: 'phone', stage: s }, false)} alt="" className="aspect-[16/10] w-full rounded border border-zinc-200 object-cover" />
            ))}
          </div>
          <div className="mt-[3%] space-y-1">
            {[90, 70, 80].map((w) => <div key={w} className="h-1 rounded bg-zinc-200" style={{ width: `${w}%` }} />)}
          </div>
        </div>
      </div>
    );
  } else if (step.kind === 'pr') {
    const pr = a.pr!;
    inner = (
      <div className="absolute inset-0 flex flex-col bg-[#0d1117] px-[6%] py-[5%] text-[#e6edf3]">
        <p className="text-[clamp(8px,2.2cqw,20px)] font-semibold">{pr.title} <span className="font-normal text-[#7d8590]">#{pr.number}</span></p>
        <span className={cn('mt-[2%] w-fit rounded-full px-2 py-0.5 text-[clamp(7px,1.3cqw,12px)] font-medium text-white', a.state === 'merged' ? 'bg-[#8957e5]' : a.state === 'draft' ? 'bg-[#6e7681]' : 'bg-[#238636]')}>
          {a.state === 'merged' ? 'Merged' : a.state === 'draft' ? 'Draft' : 'Open'}
        </span>
        <div className="mt-[4%] space-y-1.5 rounded-md border border-[#30363d] p-[3%] text-[clamp(7px,1.3cqw,12px)]">
          {(step.lines ?? []).slice(1).map((l) => <p key={l}>{l}</p>)}
          <p className="text-[#7d8590]">{pr.files} files · <span className="text-[#3fb950]">+{pr.plus}</span> <span className="text-[#f85149]">−{pr.minus}</span></p>
        </div>
      </div>
    );
  } else {
    // A terminal: Claude Code, Codex or OpenCode at work, its lines arriving.
    const lines = step.lines ?? [];
    const shown = Math.max(1, Math.ceil(p * lines.length));
    inner = (
      <div className="absolute inset-0 flex flex-col bg-[#0e0e14] font-mono text-[clamp(7px,1.35cqw,13px)] leading-relaxed text-zinc-300">
        <div className="flex items-center gap-1.5 border-b border-white/10 px-3 py-1.5 text-[clamp(6px,1.1cqw,11px)] text-zinc-500">
          <span className="size-1.5 rounded-full bg-rose-400/70" />
          <span className="size-1.5 rounded-full bg-amber-400/70" />
          <span className="size-1.5 rounded-full bg-emerald-400/70" />
          <span className="ml-1">{a.name} — {a.ai}</span>
        </div>
        <div className="min-h-0 flex-1 space-y-0.5 overflow-hidden px-[4%] py-[3%]">
          <p className={kindText[step.kind]}>● {step.title}</p>
          {lines.slice(0, shown).map((l, i) => (
            <p
              key={i}
              className={cn(
                'whitespace-pre-wrap break-all',
                step.kind === 'edit' && l.startsWith('+') && 'bg-emerald-500/10 text-emerald-300',
                step.kind === 'edit' && l.startsWith('-') && 'bg-rose-500/10 text-rose-300',
                /FAIL|fail [1-9]/.test(l) && 'text-rose-300',
                /^(ok|PASS|ℹ tests)|✔/.test(l) && 'text-emerald-300',
              )}
            >
              {l}
            </p>
          ))}
          {shown < lines.length || a.state === 'working' ? <span className="inline-block h-[1em] w-[0.5em] animate-pulse bg-zinc-400 align-middle" /> : null}
        </div>
      </div>
    );
  }
  return (
    <div className={cn('relative aspect-[16/10] w-full overflow-hidden rounded-lg bg-black ring-1 ring-line [container-type:inline-size]', className)}>
      {inner}
      {chrome && (
        <div className="pointer-events-none absolute inset-x-0 bottom-0 flex items-center gap-2 bg-gradient-to-t from-black/70 to-transparent px-2.5 pb-1.5 pt-5 text-[10.5px] tabular-nums text-white/85">
          <span className="flex items-center gap-1"><span className="size-1.5 rounded-full bg-rose-500" />REC</span>
          <span>{clock(a, t)}</span>
          <span className="ml-auto">{mmss(t)} / {mmss(length(a))}</span>
        </div>
      )}
    </div>
  );
}

// Scrubber is the timeline as a bar: one segment per step, coloured by its
// kind, with ticks where screenshots were taken and a line under kept clips.
// Dragging it moves the playhead; hovering shows that second's frame.
function Scrubber({ a, t, onT, tall, preview = true, labels }: { a: TAgent; t: number; onT: (t: number) => void; tall?: boolean; preview?: boolean; labels?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const L = length(a);
  const at = useCallback((x: number) => {
    const r = ref.current!.getBoundingClientRect();
    return Math.min(L, Math.max(0, ((x - r.left) / r.width) * L));
  }, [L]);
  const media = a.steps.flatMap((s) => s.media ?? []);
  return (
    <div className="relative select-none" data-scrubber={a.name}>
      {labels && (
        <div className="relative mb-1 h-4">
          {a.steps.map((s, i) => (
            <span
              key={i}
              className={cn('absolute top-0 flex items-center gap-1 truncate pl-0.5 text-[10.5px]', i === stepAt(a, t) ? 'text-title' : 'text-faint')}
              style={{ left: `${(s.at / L) * 100}%`, width: `${(s.dur / L) * 100}%` }}
            >
              {s.dur / L > 0.06 && kindName[s.kind]}
            </span>
          ))}
        </div>
      )}
      <div
        ref={ref}
        className={cn('relative cursor-pointer touch-none', tall ? 'h-7' : 'h-4')}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          onT(at(e.clientX));
        }}
        onPointerMove={(e) => {
          setHover(at(e.clientX));
          if (e.buttons) onT(at(e.clientX));
        }}
        onPointerLeave={() => setHover(null)}
      >
        <div className={cn('absolute inset-x-0 flex gap-px overflow-hidden rounded', tall ? 'top-1.5 h-3' : 'top-1 h-2')}>
          {a.steps.map((s, i) => (
            <div key={i} className={cn('h-full', kindTone[s.kind], i === stepAt(a, t) ? 'opacity-100' : 'opacity-45')} style={{ width: `${(s.dur / L) * 100}%` }} />
          ))}
        </div>
        {media.map((m, i) =>
          m.kind === 'clip' ? (
            <div key={i} className="absolute bottom-0 h-0.5 rounded bg-rose-400" style={{ left: `${(m.from / L) * 100}%`, width: `${((m.to - m.from) / L) * 100}%` }} />
          ) : (
            <div key={i} className={cn('absolute top-0 w-0.5 rounded-full', m.kind === 'page' ? 'bg-pink-300' : 'bg-white', tall ? 'h-6' : 'h-4')} style={{ left: `${(m.at / L) * 100}%` }} />
          ),
        )}
        <div className="pointer-events-none absolute -top-0.5 bottom-0 w-0.5 -translate-x-1/2 rounded bg-title shadow" style={{ left: `${(t / L) * 100}%` }}>
          <span className="absolute -left-1 -top-1 size-2.5 rounded-full bg-title" />
        </div>
        {preview && hover !== null && (
          <div className="pointer-events-none absolute bottom-full z-20 mb-2 w-44 -translate-x-1/2" style={{ left: `clamp(88px, ${(hover / L) * 100}%, calc(100% - 88px))` }}>
            <Frame a={a} t={hover} chrome={false} className="shadow-xl" />
            <p className="mt-1 truncate rounded bg-overlay px-1.5 py-0.5 text-center text-[10.5px] text-secondary shadow">
              {clock(a, hover)} · {a.steps[stepAt(a, hover)]!.title}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

function PlayButton({ playing, onClick, className }: { playing: boolean; onClick: () => void; className?: string }) {
  return (
    <button onClick={onClick} className={cn('flex size-7 shrink-0 items-center justify-center rounded-full bg-surface-strong text-title hover:bg-brand-500 hover:text-white', className)} aria-label={playing ? 'Pause' : 'Play'}>
      {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5 translate-x-px" />}
    </button>
  );
}

// MediaThumb is one kept thing, inline in its step: a clip (its first frame
// and length), a screenshot, or a Hatch page.
function MediaThumb({ a, m, onJump, className }: { a: TAgent; m: Media; onJump: (t: number) => void; className?: string }) {
  if (m.kind === 'page') {
    return (
      <button onClick={() => onJump(m.at)} className={cn('flex min-w-0 items-center gap-2 rounded-lg border border-line bg-surface-faint p-2 text-left hover:border-pink-400/50', className)}>
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-pink-500/15 text-pink-300"><FileText className="size-4" /></span>
        <span className="min-w-0">
          <span className="block truncate text-[12px] text-title">{m.title}</span>
          <span className="block truncate text-[10.5px] text-faint">usehatch.dev/p/{m.id}</span>
        </span>
      </button>
    );
  }
  const t = m.kind === 'clip' ? m.from : m.at;
  return (
    <button onClick={() => onJump(t)} className={cn('group relative min-w-0 text-left', className)} title={m.caption}>
      {m.kind === 'clip' ? (
        <div className="relative">
          <Frame a={a} t={m.from + 1} chrome={false} />
          <span className="absolute inset-0 flex items-center justify-center">
            <span className="flex size-7 items-center justify-center rounded-full bg-black/60 text-white group-hover:bg-brand-500"><Play className="size-3.5 translate-x-px" /></span>
          </span>
          <span className="absolute bottom-1 right-1 rounded bg-black/70 px-1 text-[10px] tabular-nums text-white">{mmss(m.to - m.from)}</span>
        </div>
      ) : (
        <img src={shotSvg(m.screen, dark())} alt="" className="aspect-[16/10] w-full rounded-lg object-cover ring-1 ring-line group-hover:ring-brand-400" />
      )}
      <span className="mt-1 line-clamp-2 block text-[10.5px] leading-snug text-subtle">{m.caption}</span>
    </button>
  );
}

// RailTimeline is the vertical timeline in the right panel: the recording
// and its scrubber pinned on top, the steps below it, each with its media.
// The step the playhead is in is lit, and scrolled to as it plays.
function RailTimeline({ a, start = 0 }: { a: TAgent; start?: number }) {
  const { t, setT, playing, setPlaying } = usePlayer(a, start);
  const active = stepAt(a, t);
  const rows = useRef<(HTMLLIElement | null)[]>([]);
  useEffect(() => {
    if (playing) rows.current[active]?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [active, playing]);
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-rail-timeline={a.name}>
      <div className="shrink-0 space-y-2 border-b border-line-faint p-3">
        <Frame a={a} t={t} />
        <div className="flex items-center gap-2">
          <PlayButton playing={playing} onClick={() => setPlaying(!playing)} />
          <div className="min-w-0 flex-1"><Scrubber a={a} t={t} onT={setT} /></div>
        </div>
      </div>
      <ol className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
        {a.steps.map((s, i) => {
          const Icon = kindIcon[s.kind];
          const on = i === active;
          return (
            <li key={i} ref={(el) => { rows.current[i] = el; }} className="relative flex gap-2.5 pb-3">
              {i < a.steps.length - 1 && <span className="absolute bottom-0 left-[13px] top-7 w-px bg-line" />}
              <button onClick={() => setT(s.at)} className={cn('relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full ring-1', on ? `${kindTone[s.kind]} text-black ring-transparent` : 'bg-surface-raised text-subtle ring-line')}>
                <Icon className="size-3.5" />
              </button>
              <div className={cn('min-w-0 flex-1 rounded-lg px-2 py-1', on && 'bg-surface')}>
                <button onClick={() => setT(s.at)} className="block w-full text-left">
                  <span className="flex items-baseline gap-2">
                    <span className={cn('min-w-0 flex-1 truncate text-[12.5px]', on ? 'text-title' : 'text-secondary')}>{s.title}</span>
                    <span className="shrink-0 text-[10.5px] tabular-nums text-faint">{clock(a, s.at)}</span>
                  </span>
                  <span className="flex items-center gap-1.5 text-[10.5px] text-faint">
                    <span className={kindText[s.kind]}>{kindName[s.kind]}</span>· {mmss(s.dur)}
                    {s.result === 'fail' && <span className="text-rose-300">· failed</span>}
                    {s.result === 'pass' && <span className="text-emerald-300">· passed</span>}
                  </span>
                </button>
                {s.media && (
                  <div className={cn('mt-2 grid gap-2', s.media.length > 1 ? 'grid-cols-2' : 'grid-cols-1')}>
                    {s.media.map((m, k) => <MediaThumb key={k} a={a} m={m} onJump={setT} className={m.kind === 'clip' ? 'col-span-2' : ''} />)}
                  </div>
                )}
              </div>
            </li>
          );
        })}
        {a.state === 'working' && (
          <li className="flex items-center gap-2.5 pl-1 text-[11.5px] text-subtle">
            <span className="size-2 animate-pulse rounded-full bg-emerald-400" /> Still working — the timeline grows as it goes
          </li>
        )}
      </ol>
    </div>
  );
}

// ReviewTimeline is the review modal's Timeline tab: the recording large,
// the steps as a labelled horizontal track, and the step under the playhead
// with its media beside it.
function ReviewTimeline({ a, start = 0 }: { a: TAgent; start?: number }) {
  const { t, setT, playing, setPlaying } = usePlayer(a, start);
  const step = a.steps[stepAt(a, t)]!;
  const Icon = kindIcon[step.kind];
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-6 py-4" data-review-timeline={a.name}>
      <div className="flex h-[min(46vh,430px)] shrink-0 gap-4">
        <div className="flex min-w-0 flex-1 justify-center">
          <Frame a={a} t={t} className="h-full w-auto max-w-full" />
        </div>
        <div className="flex min-h-0 w-[260px] shrink-0 flex-col gap-2 overflow-y-auto">
          <div className="flex items-center gap-2">
            <span className={cn('flex size-7 items-center justify-center rounded-full text-black', kindTone[step.kind])}><Icon className="size-3.5" /></span>
            <span className="text-[11px] text-faint">{kindName[step.kind]} · {clock(a, step.at)} · {mmss(step.dur)}</span>
          </div>
          <p className="text-[14px] font-medium text-title">{step.title}</p>
          {step.lines && <pre className="max-h-28 overflow-hidden whitespace-pre-wrap break-all rounded-md bg-sunken p-2 font-mono text-[10.5px] leading-relaxed text-subtle">{step.lines.slice(0, 4).join('\n')}</pre>}
          {step.media && (
            <div className="grid grid-cols-2 gap-2">
              {step.media.map((m, k) => <MediaThumb key={k} a={a} m={m} onJump={setT} className={m.kind === 'clip' ? 'col-span-2' : ''} />)}
            </div>
          )}
        </div>
      </div>
      <div className="flex items-end gap-3">
        <PlayButton playing={playing} onClick={() => setPlaying(!playing)} className="mb-0.5 size-8" />
        <div className="min-w-0 flex-1"><Scrubber a={a} t={t} onT={setT} tall labels /></div>
        <span className="mb-1.5 shrink-0 text-[11px] tabular-nums text-faint">{mmss(t)} / {mmss(length(a))}</span>
      </div>
      {/* The filmstrip: every step as a card, in order, the lit one under the playhead. */}
      <div className="flex gap-2 overflow-x-auto pb-1">
        {a.steps.map((s, i) => {
          const SIcon = kindIcon[s.kind];
          const shot = s.media?.find((m) => m.kind === 'shot');
          return (
            <button key={i} onClick={() => setT(s.at)} className={cn('w-36 shrink-0 rounded-lg border p-1.5 text-left', s === step ? 'border-brand-400 bg-surface' : 'border-line bg-surface-faint hover:border-line-strong')}>
              {shot?.kind === 'shot' ? <img src={shotSvg(shot.screen, dark())} alt="" className="aspect-[16/10] w-full rounded object-cover" /> : <Frame a={a} t={s.at + s.dur * 0.9} chrome={false} className="rounded" />}
              <span className="mt-1 flex items-center gap-1 text-[10.5px]">
                <SIcon className={cn('size-3 shrink-0', kindText[s.kind])} />
                <span className="truncate text-secondary">{s.title}</span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ---- The Today reel ---------------------------------------------------------

// A slide of the reel: an agent's title card, then each thing it kept.
type Slide = { a: TAgent; kind: 'intro' } | { a: TAgent; kind: 'media'; step: Step; m: Media };

const slides: Slide[] = agents.flatMap((a) => [
  { a, kind: 'intro' as const },
  ...a.steps.flatMap((step) => (step.media ?? []).filter((m) => m.kind !== 'shot' || !(step.media ?? []).some((c) => c.kind === 'clip')).map((m) => ({ a, kind: 'media' as const, step, m }))),
]);

// TodayReel stitches the day's timelines into one page, an agent a chapter.
function TodayReel({ onPlay, onReview }: { onPlay: (slide: number) => void; onReview: (a: TAgent) => void }) {
  return (
    <div className="min-h-0 flex-1 overflow-y-auto" data-today-reel>
      <div className="mx-auto max-w-[880px] px-6 py-6">
        <header className="flex flex-wrap items-end gap-x-4 gap-y-3">
          <div className="min-w-[320px] flex-1">
            <p className="text-[11px] uppercase tracking-wider text-faint">Today · {today.date}</p>
            <h1 className="mt-1 text-[22px] font-semibold text-title">What your agents did</h1>
            <p className="mt-1 text-[12.5px] text-subtle">
              {agents.length} agents · {today.prs} pull requests ({today.merged} merged) · {mmss(today.recorded)} of recording · {today.shots} screenshots
            </p>
          </div>
          <button className="flex items-center gap-1.5 rounded-lg border border-line px-3 py-1.5 text-[12px] text-secondary hover:text-title" title="Collect this into the week’s presentation">
            <Bookmark className="size-3.5" /> Add to this week’s demo
          </button>
          <button onClick={() => onPlay(0)} className="flex items-center gap-1.5 rounded-lg bg-brand-500 px-3 py-1.5 text-[12px] font-medium text-white hover:bg-brand-400" data-reel-play>
            <Play className="size-3.5" /> Play the day · {slides.length} slides
          </button>
        </header>
        <div className="mt-6 space-y-4">
          {agents.map((a) => {
            const [label, tone] = stateBadge[a.state];
            const media = a.steps.flatMap((s) => (s.media ?? []).map((m) => ({ s, m })));
            const first = slides.findIndex((s) => s.a === a);
            return (
              <section key={a.name} className="rounded-xl border border-line bg-surface-faint p-4">
                <div className="flex items-center gap-2.5">
                  <Avatar a={a} className="size-7" />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[13.5px] font-medium text-title">{a.task}</p>
                    <p className="text-[11px] text-faint">{a.name} · {a.started}–{clock(a, length(a))} · {a.steps.length} steps{a.pr && ` · #${a.pr.number} +${a.pr.plus} −${a.pr.minus}`}</p>
                  </div>
                  <span className={cn('rounded-md px-1.5 py-0.5 text-[10.5px] font-medium', tone)}>{label}</span>
                  {a.state === 'review' && (
                    <button onClick={() => onReview(a)} className="rounded-md bg-surface-strong px-2 py-1 text-[11.5px] text-title hover:bg-brand-500 hover:text-white">Review</button>
                  )}
                  <button onClick={() => onPlay(first)} className="flex size-7 items-center justify-center rounded-full text-subtle hover:bg-surface-strong hover:text-title" aria-label="Play this chapter"><Play className="size-3.5" /></button>
                </div>
                <div className="mt-3"><ChapterStrip a={a} /></div>
                <div className="mt-3 flex gap-2.5 overflow-x-auto pb-1">
                  {media.map(({ m }, k) => (
                    <div key={k} className={cn('shrink-0', m.kind === 'clip' ? 'w-64' : 'w-40')}>
                      <MediaThumb a={a} m={m} onJump={() => onPlay(Math.max(first, slides.findIndex((s) => s.kind === 'media' && s.m === m)))} />
                    </div>
                  ))}
                  {media.length === 0 && <p className="text-[11.5px] text-faint">Nothing kept yet.</p>}
                </div>
              </section>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// ChapterStrip is a chapter's scrubber with the frame under it shown on hover.
function ChapterStrip({ a }: { a: TAgent }) {
  const [t, setT] = useState(length(a));
  return <Scrubber a={a} t={t} onT={setT} labels />;
}

// Slideshow plays the reel full screen: a title card per agent, its clips
// played through (fast), its screenshots and pages held for a few seconds,
// with story-style progress bars and ←/→/space/Esc.
function Slideshow({ start, onClose }: { start: number; onClose: () => void }) {
  const [i, setI] = useState(start);
  const [paused, setPaused] = useState(false);
  const [p, setP] = useState(0);
  const at = useRef(0); // p, for picking up where a pause left off
  at.current = p;
  const slide = slides[i]!;
  const hold = slide.kind === 'media' && slide.m.kind === 'clip' ? 7 : slide.kind === 'intro' ? 3 : 4;
  useEffect(() => setP(0), [i]);
  useEffect(() => {
    if (paused) return;
    let last = performance.now();
    let v = at.current;
    let raf = requestAnimationFrame(function tick(now) {
      v += (now - last) / 1000 / hold;
      last = now;
      if (v >= 1) {
        if (i + 1 < slides.length) setI(i + 1);
        else setP(1);
        return;
      }
      setP(v);
      raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, [paused, hold, i]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
      else if (e.key === 'ArrowRight') setI((k) => Math.min(slides.length - 1, k + 1));
      else if (e.key === 'ArrowLeft') setI((k) => Math.max(0, k - 1));
      else if (e.key === ' ') setPaused((v) => !v);
      else return;
      e.preventDefault();
    };
    addEventListener('keydown', key);
    return () => removeEventListener('keydown', key);
  }, [onClose]);
  const a = slide.a;
  const [label, tone] = stateBadge[a.state];
  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-black text-white" data-slideshow={i}>
      <div className="flex gap-1 px-4 pt-3">
        {slides.map((s, k) => (
          <button key={k} onClick={() => setI(k)} className={cn('h-1 overflow-hidden rounded-full bg-white/20', s.kind === 'intro' ? 'w-3 shrink-0' : 'flex-1')} aria-label={`Slide ${k + 1}`}>
            <span className="block h-full bg-white" style={{ width: k < i ? '100%' : k === i ? `${p * 100}%` : '0%' }} />
          </button>
        ))}
      </div>
      <div className="flex items-center gap-2.5 px-4 py-3">
        <Avatar a={a} className="size-7" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13.5px] font-medium">{a.task}</p>
          <p className="text-[11px] text-white/55">{a.name} · {today.date}</p>
        </div>
        <span className={cn('rounded-md px-1.5 py-0.5 text-[10.5px] font-medium', tone)}>{label}</span>
        <button className="flex items-center gap-1.5 rounded-lg bg-white/10 px-2.5 py-1 text-[11.5px] hover:bg-white/20"><Bookmark className="size-3.5" /> Keep for the weekly demo</button>
        <button onClick={() => setPaused(!paused)} className="flex size-8 items-center justify-center rounded-full hover:bg-white/10" aria-label={paused ? 'Play' : 'Pause'}>{paused ? <Play className="size-4" /> : <Pause className="size-4" />}</button>
        <button onClick={onClose} className="flex size-8 items-center justify-center rounded-full hover:bg-white/10" aria-label="Close"><X className="size-4" /></button>
      </div>
      <div className="relative flex min-h-0 flex-1 items-center justify-center px-16">
        <button onClick={() => setI(Math.max(0, i - 1))} className="absolute left-4 flex size-10 items-center justify-center rounded-full bg-white/10 hover:bg-white/20" aria-label="Previous"><ChevronLeft className="size-5" /></button>
        <div className="flex aspect-[16/10] max-h-full w-full max-w-[1100px] items-center justify-center">
          {slide.kind === 'intro' ? (
            <div className="text-center">
              <Avatar a={a} className="mx-auto size-16" />
              <p className="mt-5 text-[30px] font-semibold leading-tight">{a.task}</p>
              <p className="mt-3 text-[14px] text-white/60">
                {a.steps.length} steps · {mmss(length(a))} · {a.pr ? `#${a.pr.number}, ${a.pr.files} files, +${a.pr.plus} −${a.pr.minus}` : 'no pull request yet'}
              </p>
              <div className="mx-auto mt-6 flex max-w-md justify-center gap-1">
                {a.steps.map((s, k) => {
                  const I = kindIcon[s.kind];
                  return <span key={k} className={cn('flex size-7 items-center justify-center rounded-full text-black', kindTone[s.kind])} title={s.title}><I className="size-3.5" /></span>;
                })}
              </div>
            </div>
          ) : slide.m.kind === 'clip' ? (
            <Frame a={a} t={slide.m.from + (slide.m.to - slide.m.from) * p} className="rounded-xl ring-white/10" />
          ) : slide.m.kind === 'shot' ? (
            <img src={shotSvg(slide.m.screen, true)} alt="" className="aspect-[16/10] w-full rounded-xl object-cover ring-1 ring-white/10" />
          ) : (
            <Frame a={a} t={slide.m.at} chrome={false} className="rounded-xl" />
          )}
        </div>
        <button onClick={() => setI(Math.min(slides.length - 1, i + 1))} className="absolute right-4 flex size-10 items-center justify-center rounded-full bg-white/10 hover:bg-white/20" aria-label="Next"><ChevronRight className="size-5" /></button>
      </div>
      <div className="px-4 pb-5 pt-3 text-center">
        {slide.kind === 'media' && (
          <>
            <p className="text-[15px]">{slide.m.kind === 'page' ? slide.m.title : slide.m.caption}</p>
            <p className="mt-1 text-[11.5px] text-white/50">{kindName[slide.step.kind]} · {slide.step.title} · {clock(a, slide.step.at)}</p>
          </>
        )}
        <p className="mt-2 text-[10.5px] text-white/35">{i + 1} / {slides.length} · ←/→ to step · space to pause · Esc to close</p>
      </div>
    </div>
  );
}

// ---- The shell: UI v2, lead first -------------------------------------------

type View = 'lead' | 'today' | string;

function Sidebar({ view, onView }: { view: View; onView: (v: View) => void }) {
  const row = (on: boolean) => cn('flex w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left', on ? 'bg-surface-strong text-title' : 'text-secondary hover:bg-surface');
  return (
    <aside className="flex w-[264px] shrink-0 flex-col border-r border-line bg-rail" data-sidebar-v2>
      <div className="flex h-11 items-center gap-2 px-3.5">
        <span className="flex size-6 items-center justify-center rounded-md bg-gradient-to-br from-brand-400 to-brand-600 text-[11px] font-semibold text-white">a</span>
        <span className="text-[13.5px] font-semibold text-title">agentbox</span>
        <ChevronDown className="size-3.5 text-faint" />
      </div>
      <nav className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2 pb-3">
        <button onClick={() => onView('lead')} className={row(view === 'lead')}>
          <span className="flex size-6 items-center justify-center rounded-full bg-brand-500/20 text-[10px] font-semibold text-brand-300">L</span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[12.5px]">Lead</span>
            <span className="block truncate text-[11px] text-faint">2 ready to review</span>
          </span>
        </button>
        <button onClick={() => onView('today')} className={row(view === 'today')} data-row-today>
          <span className="flex size-6 items-center justify-center rounded-full bg-amber-500/15 text-amber-300"><Clapperboard className="size-3.5" /></span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[12.5px]">Today</span>
            <span className="block truncate text-[11px] text-faint">{agents.length} agents · {mmss(today.recorded)} recorded</span>
          </span>
        </button>
        <p className="mt-3 px-2.5 pb-1 text-[10.5px] font-medium uppercase tracking-wider text-faint">Agents</p>
        {agents.map((a) => (
          <button key={a.name} onClick={() => onView(a.name)} className={row(view === a.name)}>
            <Avatar a={a} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[12.5px]">{a.task}</span>
              <span className="flex items-center gap-1.5 truncate text-[11px] text-faint">
                {a.name}
                {/* A sliver of the timeline: the row's kinds of step, in order. */}
                <span className="flex h-1 w-14 gap-px overflow-hidden rounded-full">
                  {a.steps.map((s, k) => <span key={k} className={cn('h-full', kindTone[s.kind])} style={{ flex: s.dur }} />)}
                </span>
              </span>
            </span>
          </button>
        ))}
      </nav>
    </aside>
  );
}

function Composer({ placeholder }: { placeholder: string }) {
  return (
    <div className="mx-auto w-full max-w-[720px] px-4 pb-4">
      <div className="flex items-center gap-2 rounded-2xl border border-line bg-composer px-3 py-2.5 shadow-sm">
        <Paperclip className="size-4 text-subtle" />
        <span className="min-w-0 flex-1 truncate text-[13px] text-faint">{placeholder}</span>
        <span className="text-[11px] text-faint">Opus 5.5</span>
        <span className="flex size-7 items-center justify-center rounded-full bg-brand-500 text-white"><Send className="size-3.5" /></span>
      </div>
    </div>
  );
}

// LeadChat is the project's chat; an agent's report arrives as a card with
// its timeline as a strip, which opens the panel's timeline or the review.
function LeadChat({ onPanel, onReview, onToday }: { onPanel: (a: TAgent) => void; onReview: (a: TAgent) => void; onToday: () => void }) {
  const ready = agents.filter((a) => a.state === 'review');
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto grid max-w-[720px] gap-4 px-5 py-6 text-[13px] leading-relaxed text-secondary">
          <div className="ml-auto max-w-[80%] rounded-2xl rounded-br-md bg-surface-strong px-3.5 py-2 text-primary">What’s ready? Show me before I merge anything.</div>
          <p>Two are ready to review, one merged this morning, and the QA sweep is still going. Each one’s timeline is below: hover it to see the desktop at that moment.</p>
          {ready.map((a) => (
            <div key={a.name} className="rounded-xl border border-line bg-surface-faint p-3" data-chat-card={a.name}>
              <div className="flex items-center gap-2">
                <Avatar a={a} />
                <p className="min-w-0 flex-1 truncate text-[13px] text-title">{a.task}</p>
                <span className="text-[11px] text-faint">#{a.pr?.number}</span>
              </div>
              <div className="mt-2.5"><ChapterStrip a={a} /></div>
              <div className="mt-2.5 flex items-center gap-2">
                <button onClick={() => onReview(a)} className="rounded-md bg-brand-500 px-2.5 py-1 text-[11.5px] font-medium text-white hover:bg-brand-400">Review</button>
                <button onClick={() => onPanel(a)} className="rounded-md px-2.5 py-1 text-[11.5px] text-subtle hover:bg-surface hover:text-title">Timeline in the panel</button>
              </div>
            </div>
          ))}
          <p>
            Or{' '}
            <button onClick={onToday} className="text-brand-300 underline-offset-2 hover:underline">play today’s story</button>
            : every agent’s clips and screenshots as one reel.
          </p>
        </div>
      </div>
      <Composer placeholder="Message the lead…" />
    </div>
  );
}

function AgentChat({ a }: { a: TAgent }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto grid max-w-[720px] gap-2 px-5 py-6 text-[12px] text-subtle">
          <p className="mb-2 text-[13px] text-secondary">{a.steps[0]!.lines?.join(' ')}</p>
          {a.steps.slice(1).map((s, i) => {
            const I = kindIcon[s.kind];
            return <p key={i} className="flex items-center gap-2 font-mono"><I className={cn('size-3', kindText[s.kind])} /> {s.title}</p>;
          })}
        </div>
      </div>
      <Composer placeholder={`Message ${a.name}…`} />
    </div>
  );
}

function RightPanel({ a, onAgent, onClose }: { a: TAgent; onAgent: (a: TAgent) => void; onClose: () => void }) {
  const tabs = ['Timeline', 'Desktop', 'Files', 'Media'];
  return (
    <aside className="flex w-[400px] shrink-0 flex-col border-l border-line bg-surface-faint" data-right-panel>
      <div className="flex h-10 shrink-0 items-stretch border-b border-line-faint bg-rail pr-1.5">
        {tabs.map((t, i) => (
          <span key={t} className={cn('relative flex items-center px-3 text-[12px]', i === 0 ? 'text-title' : 'text-subtle')}>
            {t}
            {i === 0 && <span className="absolute inset-x-0 top-0 h-0.5 bg-brand-500" />}
          </span>
        ))}
        <span className="flex-1" />
        <button onClick={onClose} className="my-auto flex size-7 items-center justify-center rounded-md text-subtle hover:bg-surface hover:text-title" aria-label="Close the panel"><X className="size-3.5" /></button>
      </div>
      <div className="flex shrink-0 items-center gap-2 border-b border-line-faint px-3 py-2">
        <Avatar a={a} className="size-5" />
        <select value={a.name} onChange={(e) => onAgent(agentByName(e.target.value)!)} className="min-w-0 flex-1 truncate bg-transparent text-[12px] text-title outline-none">
          {agents.map((x) => <option key={x.name} value={x.name}>{x.name} · {x.task}</option>)}
        </select>
      </div>
      <RailTimeline key={a.name} a={a} start={Number(params.get('t') ?? 0)} />
    </aside>
  );
}

// ReviewModal is v2's review modal, with Timeline as a tab beside the
// walkthrough and the files.
function ReviewModal({ a, onClose }: { a: TAgent; onClose: () => void }) {
  const [tab, setTab] = useState('Timeline');
  const pr = a.pr!;
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-scrim p-6 backdrop-blur-sm" data-review-modal={a.name}>
      <div className="flex h-[min(780px,100%)] w-[min(1120px,100%)] flex-col overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-2xl">
        <header className="flex items-center gap-3 border-b border-line-faint px-6 pt-4">
          <Avatar a={a} className="size-7" />
          <div className="min-w-0 flex-1 pb-3">
            <p className="truncate text-[14.5px] font-semibold text-title">{pr.title}</p>
            <p className="text-[11.5px] text-faint">{a.name} · #{pr.number} · {pr.files} files · <span className="text-emerald-300">+{pr.plus}</span> <span className="text-rose-300">−{pr.minus}</span> · checks {pr.checks}</p>
          </div>
          <button className="mb-3 flex size-7 items-center justify-center rounded-md text-subtle hover:bg-surface" aria-label="Full screen"><Maximize2 className="size-3.5" /></button>
          <button onClick={onClose} className="mb-3 flex size-7 items-center justify-center rounded-md text-subtle hover:bg-surface" aria-label="Close"><X className="size-4" /></button>
        </header>
        <div className="flex gap-1 border-b border-line-faint px-5">
          {['Walkthrough', 'Timeline', 'Files changed', 'Reviewed'].map((t) => (
            <button key={t} onClick={() => setTab(t)} className={cn('relative px-2.5 py-2 text-[12.5px]', tab === t ? 'text-title' : 'text-subtle hover:text-secondary')}>
              {t}
              {tab === t && <span className="absolute inset-x-1 bottom-[-1px] h-0.5 rounded bg-brand-500" />}
            </button>
          ))}
        </div>
        {tab === 'Timeline' ? (
          <ReviewTimeline a={a} start={Number(params.get('t') ?? a.steps.find((s) => s.kind === 'desktop')?.at ?? 0)} />
        ) : (
          <div className="flex flex-1 items-center justify-center text-[12px] text-faint">{tab}: as in UI v2 today</div>
        )}
        <footer className="flex items-center gap-2 border-t border-line bg-modal px-6 py-3">
          <span className="text-[11.5px] text-faint">Ready since {clock(a, length(a))}</span>
          <span className="flex-1" />
          <button className="rounded-lg border border-line px-3 py-1.5 text-[12px] text-secondary hover:text-title">Request changes</button>
          <span className="flex overflow-hidden rounded-lg bg-brand-500 text-white">
            <button className="flex items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium hover:bg-brand-400"><GitMerge className="size-3.5" /> Squash and merge</button>
            <button className="border-l border-white/20 px-1.5 hover:bg-brand-400" aria-label="Merge method"><ChevronDown className="size-3.5" /></button>
          </span>
        </footer>
      </div>
    </div>
  );
}

export function TimelineConcept() {
  const [view, setView] = useState<View>(params.get('view') ?? 'lead');
  const panelParam = params.get('panel');
  const [panel, setPanel] = useState<TAgent | null>(panelParam === '0' ? null : (agentByName(panelParam) ?? agents[0]!));
  const [review, setReview] = useState<TAgent | null>(agentByName(params.get('review')) ?? null);
  const [play, setPlay] = useState<number | null>(params.get('play') ? Number(params.get('slide') ?? 0) : null);
  const viewAgent = useMemo(() => agentByName(view), [view]);
  const onView = (v: View) => {
    setView(v);
    const a = agentByName(v);
    if (a && panel) setPanel(a);
  };
  return (
    <div className="flex h-screen w-screen overflow-hidden bg-ink text-primary" style={{ font: '13px var(--font-sans)' }} data-concept="timeline">
      <Sidebar view={view} onView={onView} />
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-11 shrink-0 items-center gap-2 border-b border-line-faint px-4">
          {viewAgent ? <Avatar a={viewAgent} /> : view === 'today' ? <Clapperboard className="size-4 text-amber-300" /> : null}
          <p className="min-w-0 flex-1 truncate text-[13px] font-medium text-title">{viewAgent ? viewAgent.task : view === 'today' ? 'Today' : 'Lead'}</p>
          {!panel && (
            <button onClick={() => setPanel(viewAgent ?? agents[0]!)} className="rounded-md px-2 py-1 text-[11.5px] text-subtle hover:bg-surface hover:text-title">Timeline ⌘D</button>
          )}
        </header>
        {view === 'today' ? (
          <TodayReel onPlay={setPlay} onReview={setReview} />
        ) : viewAgent ? (
          <AgentChat a={viewAgent} />
        ) : (
          <LeadChat onPanel={setPanel} onReview={setReview} onToday={() => setView('today')} />
        )}
      </main>
      {panel && view !== 'today' && <RightPanel a={panel} onAgent={setPanel} onClose={() => setPanel(null)} />}
      {review && <ReviewModal a={review} onClose={() => setReview(null)} />}
      {play !== null && <Slideshow start={play} onClose={() => setPlay(null)} />}
    </div>
  );
}
