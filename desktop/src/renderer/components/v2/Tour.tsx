// Tour plays lib/tours.ts's tours over the real UI: a spotlight on each
// step's anchor, dimming the rest, and a card beside it with the step's words,
// a made-up example where the real thing may not be on screen, a progress
// bar, and Back, Next and Skip (← → and Esc). With nothing to point at — a
// narrow window whose sidebar is a drawer, no projects yet — the card sits in
// the middle with its example. A step that needs a lead's chat, or its panel,
// opens it; the panel the tour opened it closes again.
//
// TourHost starts what a launch should play (onLaunch); HelpMenu, at the
// sidebar's top, and the shortcut sheet (Ctrl+/) replay them.
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ArrowRight, Check, ChevronDown, CircleHelp, Film, Hand, Image as ImageIcon, Keyboard, MessageCircleQuestion, Monitor, Pin, Sparkles, SquareTerminal, FolderTree, Globe, EyeOff } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { setPanelOpen } from '../../lib/rightPanel';
import { setSheetOpen } from '../../lib/shortcuts';
import { goTo, onLaunch, placeCard, setTourState, skip, startTour, stepsOf, tours, useTourState, welcomeTour, type Box, type Example } from '../../lib/tours';
import { launchArrival } from '../../lib/uiV2';
import { cn, shortcut } from '../../lib/utils';
import { Button } from '../ui/button';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';

const pad = 6;

// visibleBox is the first of selectors on screen, as a box.
function visibleBox(selectors: string[] | undefined): Box | null {
  for (const s of selectors ?? []) {
    for (const el of document.querySelectorAll(s)) {
      const r = el.getBoundingClientRect();
      if (r.width < 2 || r.height < 2 || r.bottom < 0 || r.right < 0 || r.top > innerHeight || r.left > innerWidth) continue;
      // Behind a drawer or a dialog's scrim: not on screen for the tour.
      const x = Math.min(innerWidth - 1, Math.max(0, r.left + r.width / 2));
      const y = Math.min(innerHeight - 1, Math.max(0, r.top + Math.min(r.height / 2, 20)));
      const hit = document.elementsFromPoint(x, y).find((h) => !h.closest('[data-tour]'));
      if (hit && !el.contains(hit) && !hit.contains(el)) continue;
      // Cut to what the scroller it's in shows (a long project in the sidebar).
      let top = Math.max(r.top, 0);
      let bottom = Math.min(r.bottom, innerHeight);
      for (let p = el.parentElement; p; p = p.parentElement) {
        if (!/(auto|scroll|hidden)/.test(getComputedStyle(p).overflowY)) continue;
        const c = p.getBoundingClientRect();
        top = Math.max(top, c.top);
        bottom = Math.min(bottom, c.bottom);
        break;
      }
      if (bottom - top < 2) continue;
      return { left: r.left, top, width: r.width, height: bottom - top };
    }
  }
  return null;
}

const same = (a: Box | null, b: Box | null) =>
  a === b || (!!a && !!b && Math.abs(a.left - b.left) < 0.5 && Math.abs(a.top - b.top) < 0.5 && Math.abs(a.width - b.width) < 0.5 && Math.abs(a.height - b.height) < 0.5);

// TourHost plays the run lib/tours.ts has, if any, and starts the one this
// launch should.
export function TourHost({ view, onSelect, onReleaseNotes }: { view: View; onSelect: (view: View) => void; onReleaseNotes?: () => void }) {
  const state = useTourState();
  useEffect(() => {
    setTourState((s) => onLaunch(s, launchArrival()));
  }, []);
  if (!state.playing) return null;
  return <TourPlayer key={state.playing.tours.join()} view={view} onSelect={onSelect} onReleaseNotes={onReleaseNotes} />;
}

function TourPlayer({ view, onSelect, onReleaseNotes }: { view: View; onSelect: (view: View) => void; onReleaseNotes?: () => void }) {
  const t = useT();
  const state = useTourState();
  const playing = state.playing!;
  const steps = stepsOf(playing);
  const index = Math.min(playing.step, steps.length - 1);
  const { tour, step } = steps[index];
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });

  // The project a step about a lead's chat shows: the one open, else the one
  // with the most agents.
  const project =
    view.kind === 'project'
      ? view.project
      : view.kind === 'agent'
        ? view.ref.split('/')[0]
        : [...(projects.data ?? [])].sort((a, b) => count(b.name) - count(a.name))[0]?.name;
  function count(name: string) {
    return agents.data?.filter((a) => a.project === name).length ?? 0;
  }

  const opened = useRef<string | null>(null);
  useEffect(() => {
    if (step.go && project) {
      if (view.kind !== 'project' || view.project !== project) onSelect({ kind: 'project', project, tab: 'chat' });
      const lead = `${project}/lead`;
      if (step.go === 'panel' && opened.current !== lead) {
        setPanelOpen(lead, true);
        opened.current = lead;
      }
    }
    if (step.go !== 'panel' && opened.current) {
      setPanelOpen(opened.current, false);
      opened.current = null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step.id, project]);
  // Toasts wait while the tour plays (styles.css): they'd sit over its card.
  useEffect(() => {
    document.body.dataset.touring = '';
    return () => {
      delete document.body.dataset.touring;
      if (opened.current) setPanelOpen(opened.current, false);
    };
  }, []);

  // Where the anchor is, followed as the app moves under it.
  const [box, setBox] = useState<Box | null>(null);
  useLayoutEffect(() => {
    // undefined until the first measure, which always sets the box: a step
    // whose anchor isn't on screen clears the last step's, rather than
    // spotlighting what that step pointed at.
    let last: Box | null | undefined;
    const measure = () => {
      const next = visibleBox(step.anchor);
      if (last === undefined || !same(next, last)) {
        last = next;
        setBox(next);
      }
    };
    measure();
    const timer = setInterval(measure, 200);
    addEventListener('resize', measure);
    return () => {
      clearInterval(timer);
      removeEventListener('resize', measure);
    };
  }, [step]);

  const cardRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 380, height: 320 });
  useLayoutEffect(() => {
    const el = cardRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ width: el.offsetWidth, height: el.offsetHeight }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const [vp, setVp] = useState({ width: innerWidth, height: innerHeight });
  useEffect(() => {
    const on = () => setVp({ width: innerWidth, height: innerHeight });
    addEventListener('resize', on);
    return () => removeEventListener('resize', on);
  }, []);
  const hole = box && { left: box.left - pad, top: box.top - pad, width: box.width + pad * 2, height: box.height + pad * 2 };
  const at = placeCard(hole, size, vp);

  const next = useCallback(() => setTourState((s) => goTo(s, (s.playing?.step ?? 0) + 1)), []);
  const back = useCallback(() => setTourState((s) => goTo(s, (s.playing?.step ?? 0) - 1)), []);
  const close = useCallback(() => setTourState(skip), []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key === 'ArrowRight') next();
      else if (e.key === 'ArrowLeft') back();
      else if (e.key === 'Escape') close();
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    addEventListener('keydown', onKey, true);
    return () => removeEventListener('keydown', onKey, true);
  }, [next, back, close]);
  useEffect(() => {
    cardRef.current?.focus({ preventScroll: true });
  }, [index]);

  const last = index === steps.length - 1;
  const showExample = step.example && (step.exampleAlways || !box);
  const whatsNew = tour.id !== welcomeTour.id;
  const label = whatsNew ? t('tour.whatsNewLabel', { name: t(tour.title) }) : t(tour.title);

  return (
    <div className="fixed inset-0 z-[70]" data-tour={tour.id} data-tour-step={step.id}>
      {/* The dimming, with a hole cut where the anchor is: a mask, since a
          shadow wide enough to cover the window is clipped by Chromium. */}
      <svg className="absolute inset-0 size-full animate-fade-in" aria-hidden>
        <defs>
          <mask id="tour-hole">
            <rect width="100%" height="100%" fill="white" />
            {hole && <rect rx="12" fill="black" className="transition-all duration-300 ease-out" style={{ x: hole.left, y: hole.top, width: hole.width, height: hole.height }} />}
          </mask>
        </defs>
        <rect width="100%" height="100%" style={{ fill: "var(--ab-scrim)" }} mask="url(#tour-hole)" />
      </svg>
      {hole && (
        <div
          className="pointer-events-none absolute rounded-xl ring-2 ring-brand-400/70 transition-all duration-300 ease-out"
          style={hole}
          data-tour-spot
        />
      )}
      {/* Clicks around the card do nothing: the tour is read, then left. */}
      <div className="absolute inset-0" onMouseDown={(e) => e.preventDefault()} />
      <div
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="tour-title"
        aria-describedby="tour-body"
        tabIndex={-1}
        className="absolute flex max-h-[calc(100vh-24px)] w-[min(400px,calc(100vw-24px))] flex-col overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_30px_90px_-30px_var(--ab-shadow-deep)] outline-none transition-[left,top] duration-300 ease-out"
        style={{ left: at.left, top: at.top }}
        data-tour-card
      >
        <div className="h-0.5 w-full bg-surface-raised" aria-hidden>
          <div className="h-full bg-brand-400 transition-[width] duration-300" style={{ width: `${((index + 1) / steps.length) * 100}%` }} />
        </div>
        <div className="min-h-0 overflow-y-auto px-5 pb-4 pt-4">
          <div className="mb-2 flex items-center gap-2 text-[11px] text-subtle">
            {whatsNew ? <Sparkles className="size-3.5 text-brand-300" /> : <CircleHelp className="size-3.5 text-brand-300" />}
            <span className="min-w-0 flex-1 truncate font-medium uppercase tracking-[0.08em]">{label}</span>
            <span className="shrink-0 tabular-nums" data-tour-progress>
              {t('tour.progress', { step: index + 1, total: steps.length })}
            </span>
          </div>
          <h2 id="tour-title" className="text-[16px] font-semibold tracking-tight text-title">
            {t(step.title)}
          </h2>
          <p id="tour-body" className="mt-1.5 text-[13px] leading-relaxed text-secondary">
            {t.rich(step.body, richTags)}
          </p>
          {showExample && (
            <div className="mt-3.5">
              <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-faint">{t('tour.example')}</div>
              {examples[step.example!](t, !!box)}
            </div>
          )}
        </div>
        <div className="flex items-center gap-2 border-t border-line px-4 py-3">
          {/* The release notes never open over a tour: the last step of a What's new tour offers them. */}
          {last && whatsNew && onReleaseNotes && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                next();
                onReleaseNotes();
              }}
              data-tour-release-notes
            >
              {t('tour.releaseNotes')}
            </Button>
          )}
          {!last && (
            <Button variant="ghost" size="sm" onClick={close} data-tour-skip>
              {t('tour.skip')}
            </Button>
          )}
          <div className="ml-auto flex items-center gap-2">
            {index > 0 && (
              <Button variant="outline" size="sm" onClick={back} data-tour-back>
                <ArrowLeft />
                {t('common.back')}
              </Button>
            )}
            <Button variant="primary" size="sm" onClick={next} data-tour-next>
              {last ? (
                <>
                  <Check />
                  {t('tour.finish')}
                </>
              ) : (
                <>
                  {t('common.next')}
                  <ArrowRight />
                </>
              )}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

const richTags = {
  b: (c: ReactNode) => <strong className="font-semibold text-title">{c}</strong>,
  k: (c: ReactNode) => <kbd className="rounded border border-line-strong bg-surface-raised px-1 py-px font-mono text-[11px] text-primary">{c}</kbd>,
};

// ——— Made-up examples ———

type Tr = ReturnType<typeof useT>;

function Frame({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('rounded-xl border border-line bg-surface-faint p-3 text-[12px]', className)} data-tour-example>{children}</div>;
}

function Dot({ tone }: { tone: 'working' | 'needs' | 'done' | 'idle' }) {
  return <span className={cn('size-1.5 shrink-0 rounded-full', tone === 'working' ? 'bg-brand-400' : tone === 'needs' ? 'bg-amber-400' : tone === 'done' ? 'bg-emerald-400' : 'bg-faint')} />;
}

function Head({ children }: { children: ReactNode }) {
  return <div className="px-1 pb-1 text-[9.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{children}</div>;
}

// Each is given whether the step's anchor is on screen, to leave out what it shows.
const examples: Record<Example, (t: Tr, anchored: boolean) => ReactNode> = {
  sidebar: (t) => (
    <Frame className="grid gap-0.5">
      <Head>
        <span className="inline-flex items-center gap-1">
          <Pin className="size-2.5" />
          {t('v2.sidebar.pinned')}
        </span>
      </Head>
      <div className="rounded-lg bg-surface-raised px-2 py-1.5">
        <div className="font-medium text-title">shop-web</div>
        <div className="truncate text-[11px] text-subtle">{t('tour.ex.leadLine')}</div>
      </div>
      <div className="ml-3 grid gap-0.5 border-l border-line pl-2">
        <div className="flex items-center gap-1.5 py-0.5 text-secondary"><Dot tone="working" /><span className="font-mono text-[11px]">fix-login-redirect</span><span className="ml-auto text-[10px] text-subtle">{t('tour.ex.working')}</span></div>
        <div className="flex items-center gap-1.5 py-0.5 text-secondary"><Dot tone="needs" /><span className="font-mono text-[11px]">checkout-tests</span><span className="ml-auto text-[10px] text-amber-300">{t('tour.ex.needsYou')}</span></div>
      </div>
      <div className="mt-1.5 rounded-md border border-dashed border-line px-2 py-1 text-[11px] text-subtle">
        {t('v2.sidebar.idle')} · 3 · {t('v2.sidebar.idleHint')}
      </div>
      <div className="flex items-center gap-1 px-2 pt-1 text-[11px] text-subtle">
        <EyeOff className="size-3" />
        {t('v2.sidebar.hidden')} · 1
      </div>
    </Frame>
  ),
  prompt: (t) => (
    <Frame className="grid gap-2">
      <div className="text-[10.5px] text-subtle">{t('tour.ex.toLead', { project: 'shop-web' })}</div>
      <div className="ml-6 rounded-xl rounded-br-sm bg-brand-500/15 px-3 py-2 text-primary">{t('tour.ex.prompt')}</div>
      <div className="mr-6 rounded-xl rounded-bl-sm bg-surface-raised px-3 py-2 text-secondary">{t('tour.ex.reply')}</div>
    </Frame>
  ),
  mention: (t) => (
    <Frame className="grid gap-2">
      <div className="ml-6 rounded-xl rounded-br-sm bg-brand-500/15 px-3 py-2 text-primary">
        {t.rich('tour.ex.mention', { at: (c) => <span className="rounded bg-brand-500/25 px-1 font-medium text-title">{c}</span> })}
      </div>
      <div className="text-[11px] text-subtle">{t('tour.ex.mentionHint')}</div>
    </Frame>
  ),
  cards: (t) => (
    <div className="grid gap-2" data-tour-example>
      <div className="rounded-xl border border-amber-400/30 bg-amber-400/5 p-3 text-[12px]">
        <div className="flex items-center gap-1.5 text-[11px] font-medium text-amber-300">
          <MessageCircleQuestion className="size-3.5" />
          {t('tour.ex.asks', { agent: 'fix-login-redirect' })}
        </div>
        <div className="mt-1 text-primary">{t('tour.ex.question')}</div>
        <div className="mt-2 flex gap-1.5">
          <span className="rounded-md bg-surface-raised px-2 py-0.5 text-secondary">{t('tour.ex.answerYes')}</span>
          <span className="rounded-md bg-surface-raised px-2 py-0.5 text-secondary">{t('tour.ex.answerNo')}</span>
        </div>
      </div>
      <div className="rounded-xl border border-line bg-surface-faint p-3 text-[12px]">
        <div className="flex items-center gap-1.5 text-[11px] font-medium text-secondary">
          <Hand className="size-3.5" />
          {t('tour.ex.wants', { agent: 'checkout-tests' })}
        </div>
        <code className="mt-1 block truncate font-mono text-[11px] text-primary">npm install --save-dev @playwright/test</code>
        <div className="mt-2 flex gap-1.5">
          <span className="rounded-md bg-emerald-500/15 px-2 py-0.5 text-emerald-300">{t('tour.ex.allow')}</span>
          <span className="rounded-md bg-surface-raised px-2 py-0.5 text-secondary">{t('tour.ex.deny')}</span>
        </div>
      </div>
    </div>
  ),
  review: (t) => (
    <Frame className="grid gap-2 p-2.5">
      <div className="flex items-center gap-2 text-[11px]">
        <span className="rounded-md bg-surface-raised px-1.5 py-0.5 font-medium text-title">{t('tour.ex.toReviewTab', { count: 3 })}</span>
        <span className="px-1.5 py-0.5 text-subtle">{t('tour.ex.reviewedTab')}</span>
        <span className="ml-auto tabular-nums text-subtle">{t('tour.ex.position')}</span>
      </div>
      <div>
        <div className="font-mono text-[11.5px] text-title">fix-login-redirect</div>
        <div className="text-[11px] text-subtle">{t('tour.ex.verdict')}</div>
      </div>
      <div className="relative flex aspect-[16/8] items-center justify-center overflow-hidden rounded-lg border border-line bg-gradient-to-br from-brand-500/20 via-surface-raised to-emerald-500/10">
        <div className="absolute left-3 right-3 top-3 h-2 rounded bg-surface-strong" />
        <div className="absolute left-3 top-7 h-12 w-1/3 rounded bg-surface-strong/70" />
        <span className="flex size-8 items-center justify-center rounded-full bg-black/40 text-white"><Film className="size-4" /></span>
        <span className="absolute bottom-1.5 left-2 text-[10px] text-secondary">{t('tour.ex.recording')}</span>
      </div>
      <div className="flex gap-1">
        {[Film, ImageIcon, ImageIcon].map((Icon, i) => (
          <span key={i} className={cn('flex h-6 w-9 items-center justify-center rounded border bg-surface-raised', i === 0 ? 'border-brand-400' : 'border-line')}>
            <Icon className="size-3 text-subtle" />
          </span>
        ))}
        <span className="ml-1 self-center text-[10.5px] text-subtle">{t('tour.ex.pr')}</span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
        <span className="rounded-md border border-line-strong px-2 py-0.5 text-secondary">← {t('common.back')}</span>
        <span className="ml-auto rounded-md border border-line-strong px-2 py-0.5 text-secondary">{t('review.requestChanges')}</span>
        <span className="inline-flex items-center gap-0.5 rounded-md bg-brand-500 px-2 py-0.5 font-medium text-white">
          {t('tour.ex.merge')}
          <ChevronDown className="size-3" />
        </span>
        <span className="rounded-md border border-line-strong px-2 py-0.5 text-secondary">{t('common.next')} →</span>
      </div>
    </Frame>
  ),
  panel: (t, anchored) => (
    <Frame className="grid gap-1.5">
      <div className={cn('grid grid-cols-2 gap-1.5', anchored && 'hidden')}>
        {(
          [
            [FolderTree, 'panel.tab.files', 'F'],
            [Monitor, 'panel.tab.desktop', 'D'],
            [SquareTerminal, 'panel.tab.terminal', 'T'],
            [Globe, 'panel.tab.browser', 'B'],
          ] as const
        ).map(([Icon, key, k]) => (
          <div key={k} className="flex items-center gap-1.5 rounded-lg border border-line bg-surface px-2 py-1.5">
            <Icon className="size-3.5 text-secondary" />
            <span className="min-w-0 flex-1 truncate text-primary">{t(key)}</span>
            <kbd className="rounded border border-line px-1 font-mono text-[9.5px] text-faint">{k}</kbd>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-1.5 rounded-lg bg-surface-raised px-2 py-1.5 text-[11px]">
        <Monitor className="size-3.5 text-subtle" />
        <span className="text-subtle">{t('tour.ex.whose')}</span>
        <span className="font-mono text-title">fix-login-redirect</span>
        <span className="ml-auto inline-flex items-center gap-1 text-emerald-300"><Dot tone="done" />{t('tour.ex.machineOn')}</span>
      </div>
    </Frame>
  ),
  layout: (t) => (
    <Frame className="flex flex-wrap items-center gap-2">
      <span className="text-subtle">{t('tour.ex.layoutPath')}</span>
      <span className="ml-auto inline-flex rounded-lg border border-line-strong p-0.5 text-[11.5px]">
        <span className="rounded-md bg-surface-raised px-2 py-0.5 font-medium text-title">{t('settings.layout.new')}</span>
        <span className="rounded-md px-2 py-0.5 text-secondary ring-1 ring-inset ring-brand-400/60">{t('settings.layout.classic')}</span>
      </span>
    </Frame>
  ),
};

// ——— Help ———

// HelpMenu is the "?" at the sidebar's top: the tour again, each What's new tour,
// and the keyboard shortcuts.
export function HelpMenu() {
  const t = useT();
  return (
    <Menu>
      <Tip label={t('tour.help.menu')}>
        <MenuTrigger asChild>
          <button
            type="button"
            aria-label={t('tour.help.menu')}
            className="flex size-7 shrink-0 items-center justify-center rounded-md text-subtle transition-colors hover:bg-surface-strong hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
            data-v2-help
          >
            <CircleHelp className="size-4" />
          </button>
        </MenuTrigger>
      </Tip>
      <MenuContent align="start">
        <TourMenuItems />
        <MenuSeparator />
        <MenuItem icon={Keyboard} onSelect={() => setSheetOpen(true)} data-help-keys>
          {t('tour.help.keys', { keys: shortcut('/') })}
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

function TourMenuItems() {
  const t = useT();
  return (
    <>
      <MenuItem icon={CircleHelp} onSelect={() => startTour(welcomeTour.id)} data-help-tour>
        {t('tour.help.take')}
      </MenuItem>
      <MenuLabel>{t('tour.whatsNew')}</MenuLabel>
      {tours.map((tour) => (
        <MenuItem key={tour.id} icon={Sparkles} onSelect={() => startTour(tour.id)} data-help-whats-new={tour.id}>
          {t(tour.title)}
        </MenuItem>
      ))}
    </>
  );
}

// TourButtons are the shortcut sheet's way to the tours.
export function TourButtons() {
  const t = useT();
  const play = (id: string) => {
    setSheetOpen(false);
    startTour(id);
  };
  return (
    <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4" data-sheet-tours>
      <Button size="sm" variant="secondary" onClick={() => play(welcomeTour.id)} data-sheet-tour>
        <CircleHelp />
        {t('tour.help.take')}
      </Button>
      <span className="ml-1 text-[12px] text-subtle">{t('tour.whatsNew')}:</span>
      {tours.map((tour) => (
        <Button key={tour.id} size="sm" variant="ghost" onClick={() => play(tour.id)}>
          {t(tour.title)}
        </Button>
      ))}
    </div>
  );
}

