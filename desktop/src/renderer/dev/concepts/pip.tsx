// Concept: picture-in-picture desktops in the lead (?concept=pip).
//
// UI v2's real sidebar, top bar and review modal around a mocked lead chat,
// which gets three things:
// - a dock of live desktops at its right edge, one per agent at work: drag
//   one out to float it anywhere over the chat, click one to grow it, pin it
//   to open it in a column beside the chat; stack the dock to a deck;
// - mentions of an agent in the lead's messages that open inline on its
//   latest recording or screenshot (the message reporting it opens on it);
// - a Today button in the header: everything every agent captured today.
// Mock only: the strings aren't translated and the desktops are drawn
// (pipDesktops.tsx), not streamed.
import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowUpRight, ChevronLeft, ChevronRight, Film, Image as ImageIcon, Images, Layers, Maximize2, Minimize2, MousePointer2, PanelRightClose, PanelRightOpen, Play, Rows3, SendHorizontal, X } from 'lucide-react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { SidebarV2 } from '../../components/v2/SidebarV2';
import { TopBar } from '../../components/TopBar';
import { InTopBar } from '../../components/v2/ProjectHeader';
import { ReviewV2, useOpenReview, useProjectReview } from '../../components/v2/ReviewV2';
import { Button } from '../../components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '../../components/ui/dialog';
import { Tip } from '../../components/ui/tooltip';
import { devRequests, PROJECT } from '../fixtures';
import { landReview } from '../review';
import { agents, captures as seededCaptures, doneMessage, live, messages as seededMessages, testDone, type Capture, type Live, type Message } from './pipData';
import { Desktop, Player } from './pipDesktops';

let current: T.Agent[] = agents([]);
// installPip answers the sidebar's agents with the concept's own.
export function installPip(): void {
  devRequests.unshift((method, path) => (method === 'GET' && path === '/v1/agents' ? { status: 200, body: JSON.stringify(current), contentType: 'application/json' } : undefined));
}

type Spot = { x: number; y: number };
const TILE = 224;

const without = <V,>(rec: Record<string, V>, key: string) => Object.fromEntries(Object.entries(rec).filter(([k]) => k !== key));

function useNow() {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(id);
  }, []);
  return now;
}

const when = (m: number) => (m < 1 ? 'just now' : m < 60 ? `${Math.round(m)} min ago` : `${Math.floor(m / 60)} h ${Math.round(m % 60)} min ago`);

export function PipConcept() {
  const queryClient = useQueryClient();
  const view: View = { kind: 'project', project: PROJECT, tab: 'chat' };
  const [finished, setFinished] = useState<string[]>([]);
  const [gone, setGone] = useState<string[]>([]);
  const [caps, setCaps] = useState<Capture[]>(seededCaptures);
  const [msgs, setMsgs] = useState<Message[]>(seededMessages);
  const [pinned, setPinned] = useState<string | null>(null);
  const [big, setBig] = useState<string | null>(null);
  const [floats, setFloats] = useState<Record<string, Spot>>({});
  const [stacked, setStacked] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [gallery, setGallery] = useState<{ agent?: string; open?: string } | null>(null);
  const [seen, setSeen] = useState(caps.length - 2);
  const area = useRef<HTMLDivElement>(null);

  useEffect(() => {
    queryClient.setQueryData(['agents'], current);
    queryClient.setQueryData(['media', `${PROJECT}/agent-16`], []);
  }, [queryClient]);

  const finish = () => {
    if (finished.includes('agent-16')) return;
    setFinished((f) => [...f, 'agent-16']);
    setCaps((c) => [{ ...testDone }, ...c]);
    current = agents(['agent-16']);
    queryClient.setQueryData(['agents'], current);
    landReview(queryClient, {
      project: PROJECT,
      agent: 'agent-16',
      ref: `${PROJECT}/agent-16`,
      title: 'Fix the flaky reconnect test',
      branch: 'agentbox/flaky-reconnect',
      ai: 'claude',
      summary: 'TestReconnect reconnected before the old socket had closed, so one run in ten saw two sessions. It waits for the close now; 50 of 50 runs pass under -race.',
      finishedAt: new Date().toISOString(),
      fingerprint: '{"f":"agent-16"}',
    });
    window.setTimeout(() => setMsgs((m) => [...m, doneMessage]), 900);
    // It leaves the dock a while after: it isn't at work any more.
    window.setTimeout(() => setGone((g) => [...g, 'agent-16']), 14_000);
  };

  const working = live.filter((a) => !gone.includes(a.name));
  // One that has just finished comes to the top, stacked or not.
  const docked = working.filter((a) => a.name !== pinned && a.name !== big && !floats[a.name]).sort((a, b) => Number(finished.includes(b.name)) - Number(finished.includes(a.name)));
  const grow = (name: string) => {
    if (pinned === name) return;
    setFloats((f) => without(f, name));
    setBig(name);
  };
  const pin = (name: string) => {
    setBig(null);
    setFloats((f) => without(f, name));
    setPinned(name);
  };

  return (
    <ReviewV2>
      <div className="flex h-screen w-screen overflow-hidden bg-ink font-sans text-[13px]" data-concept="pip">
        <div className="hidden lg:flex">
          <SidebarV2
            view={view}
            onSelect={(v) => {
              // An agent at work, chosen in the sidebar, grows over the chat.
              if (v.kind === 'agent' && working.some((a) => `${PROJECT}/${a.name}` === v.ref)) grow(v.ref.split('/')[1]);
              else toast('Concept: only the lead is mocked here.');
            }}
            onAddProject={() => {}}
            onNewAgent={() => {}}
          />
        </div>
        <div className="flex min-w-0 flex-1 flex-col">
          <TopBar view={view} onSelect={() => {}} onOpenNav={() => {}} />
          <InTopBar end>
            <TodayButton count={caps.length} fresh={caps.length - seen} onClick={() => { setSeen(caps.length); setGallery({}); }} />
          </InTopBar>
          <div className="flex min-h-0 min-w-0 flex-1">
            <main ref={area} className="relative flex min-h-0 min-w-0 flex-1 flex-col">
              <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
                <Chat messages={msgs} captures={caps} finished={finished} working={working} onWatch={grow} onGallery={(agent, open) => setGallery({ agent, open })} dockWidth={hidden || docked.length === 0 ? 0 : TILE + 24} />
              </div>
              <Dock agents={docked} finished={finished} stacked={stacked} hidden={hidden} onStack={() => setStacked((s) => !s)} onHide={setHidden} onGrow={grow} onPin={pin} onFloat={(name, at) => setFloats((f) => ({ ...f, [name]: at }))} area={area} />
              {Object.entries(floats).map(([name, at]) => {
                const a = working.find((w) => w.name === name);
                return a && <Floating key={name} agent={a} at={at} finished={finished.includes(name)} area={area} onMove={(p) => setFloats((f) => ({ ...f, [name]: p }))} onDock={() => setFloats((f) => without(f, name))} onGrow={() => grow(name)} />;
              })}
              {big && working.some((a) => a.name === big) && <Big agent={working.find((a) => a.name === big)!} finished={finished.includes(big)} area={area} onPin={() => pin(big)} onShrink={() => setBig(null)} />}
            </main>
            {pinned && working.some((a) => a.name === pinned) && <Pinned agent={working.find((a) => a.name === pinned)!} working={working} finished={finished} captures={caps} onSwitch={pin} onUnpin={() => setPinned(null)} onGallery={(open) => setGallery({ agent: pinned, open })} />}
          </div>
        </div>
      </div>
      <Gallery state={gallery} captures={caps} finished={finished} working={working} onState={setGallery} onWatch={(name) => { setGallery(null); grow(name); }} />
      <button
        type="button"
        onClick={finish}
        data-pip-finish
        className="fixed bottom-3 left-[300px] z-40 rounded-lg border border-line-strong bg-overlay px-3 py-1.5 text-[12px] text-secondary shadow-md hover:text-title"
      >
        Finish agent-16
      </button>
    </ReviewV2>
  );
}

// ——— The header's Today ———

function TodayButton({ count, fresh, onClick }: { count: number; fresh: number; onClick: () => void }) {
  return (
    <Tip label="Everything your agents captured today">
      <Button size="sm" variant="ghost" className="relative gap-1.5 px-2.5" onClick={onClick} data-pip-today>
        <Images />
        Today
        <span className="rounded-full bg-surface-raised px-1.5 text-[11px] tabular-nums text-muted">{count}</span>
        {fresh > 0 && <span className="absolute right-1 top-1 size-2 rounded-full bg-brand-400 ring-2 ring-ink" />}
      </Button>
    </Tip>
  );
}

// ——— The lead's chat ———

function Chat({ messages, captures, finished, working, onWatch, onGallery, dockWidth }: { messages: Message[]; captures: Capture[]; finished: string[]; working: Live[]; onWatch: (name: string) => void; onGallery: (agent: string, open: string) => void; dockWidth: number }) {
  const end = useRef<HTMLDivElement>(null);
  useEffect(() => {
    void end.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [messages.length]);
  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto" data-pip-chat>
        {/* The text keeps its column, but gives way to the dock when the
            window is too narrow for both. */}
        <div className="mx-auto w-full max-w-4xl px-4 pb-36 pt-6 md:px-6" style={{ paddingRight: `max(1.5rem, calc(${dockWidth}px - (100% - 56rem) / 2))` }}>
          {messages.map((m) =>
            m.who === 'user' ? (
              <div key={m.id} className="flex flex-col items-end gap-1 pb-4 pt-1">
                <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-surface-raised px-3.5 py-2.5 text-sm leading-relaxed text-primary">
                  <Rich text={m.text} />
                </div>
              </div>
            ) : (
              <LeadMessage key={m.id} message={m} captures={captures} finished={finished} working={working} onWatch={onWatch} onGallery={onGallery} />
            ),
          )}
          <div ref={end} />
        </div>
      </div>
      <div className="pointer-events-none absolute inset-x-0 bottom-0 px-3 pb-3 md:px-6 md:pb-5">
        <div className="pointer-events-auto relative mx-auto w-full max-w-4xl">
          <div className="flex items-end gap-2 rounded-2xl border border-line-strong bg-overlay px-4 py-3 shadow-lg backdrop-blur-xl">
            <div className="min-h-6 flex-1 py-0.5 text-sm text-faint">Ask the lead…</div>
            <span className="flex size-8 items-center justify-center rounded-lg bg-surface-raised text-subtle">
              <SendHorizontal className="size-4" />
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

// Rich is the mock's markdown: bold, code, and mentions of agents.
function Rich({ text, mention }: { text: string; mention?: (name: string, key: number) => ReactNode }) {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`|agent-\d+)/g);
  return (
    <>
      {parts.map((p, i) =>
        p.startsWith('**') ? (
          <strong key={i} className="font-semibold text-title">
            {p.slice(2, -2)}
          </strong>
        ) : p.startsWith('`') ? (
          <code key={i} className="rounded bg-surface-raised px-1 py-0.5 font-mono text-[12.5px]">
            {p.slice(1, -1)}
          </code>
        ) : /^agent-\d+$/.test(p) && mention ? (
          mention(p, i)
        ) : (
          p
        ),
      )}
    </>
  );
}

function latestOf(agent: string, captures: Capture[], before?: number) {
  const mine = captures.filter((c) => c.agent === agent && (before === undefined || c.minutesAgo >= before)).sort((a, b) => a.minutesAgo - b.minutesAgo);
  return mine[0];
}

function LeadMessage({ message, captures, finished, working, onWatch, onGallery }: { message: Message; captures: Capture[]; finished: string[]; working: Live[]; onWatch: (name: string) => void; onGallery: (agent: string, open: string) => void }) {
  // The agent the message reports opens on what it captured; any other
  // mention opens on a click.
  const [open, setOpen] = useState<string | null>(message.expand ?? null);
  const shown = open && (open === message.expand && message.capture ? captures.find((c) => c.id === message.capture) : latestOf(open, captures, message.minutesAgo) ?? latestOf(open, captures));
  return (
    <div className="pb-5 text-sm leading-relaxed text-primary" data-pip-message={message.id}>
      <p>
        <Rich
          text={message.text}
          mention={(name, key) => {
            const has = captures.some((c) => c.agent === name);
            const isLive = working.some((a) => a.name === name) && !finished.includes(name);
            return (
              <button
                key={key}
                type="button"
                disabled={!has}
                onClick={() => setOpen(open === name ? null : name)}
                className={`mx-0.5 inline-flex items-center gap-1 rounded-md px-1.5 py-px align-baseline font-medium transition ${open === name ? 'bg-brand-500/20 text-brand-200' : 'bg-surface-raised text-title hover:bg-surface-strong'} disabled:cursor-default`}
                data-pip-mention={name}
              >
                <span className={`size-1.5 rounded-full ${isLive ? 'animate-pulse bg-emerald-400' : 'bg-amber-300'}`} />
                {name}
                {has && <ChevronRight className={`size-3 text-subtle transition ${open === name ? 'rotate-90' : ''}`} />}
              </button>
            );
          }}
        />
      </p>
      {shown && <InlineCapture capture={shown} live={working.some((a) => a.name === shown.agent) && !finished.includes(shown.agent)} ready={!working.some((a) => a.name === shown.agent) || finished.includes(shown.agent)} onWatch={() => onWatch(shown.agent)} onGallery={() => onGallery(shown.agent, shown.id)} onClose={() => setOpen(null)} />}
      <div className="mt-1.5 text-[11px] text-faint">{when(message.minutesAgo)}</div>
    </div>
  );
}

function InlineCapture({ capture, live: isLive, ready, onWatch, onGallery, onClose }: { capture: Capture; live: boolean; ready: boolean; onWatch: () => void; onGallery: () => void; onClose: () => void }) {
  const openReview = useOpenReview();
  const review = useProjectReview(PROJECT);
  const item = review.data?.items?.find((i) => i.agent === capture.agent);
  return (
    <div className="mt-3 max-w-[480px] animate-pop-in overflow-hidden rounded-xl border border-line-strong bg-surface shadow-[0_20px_50px_-30px_var(--ab-shadow-deep)]" data-pip-inline={capture.agent}>
      <div className="flex items-center gap-2 px-3 py-2 text-[12px]">
        {capture.kind === 'recording' ? <Film className="size-3.5 text-subtle" /> : <ImageIcon className="size-3.5 text-subtle" />}
        <span className="min-w-0 flex-1 truncate text-muted">
          <span className="font-medium text-secondary">{capture.agent}</span> · {capture.name}.{capture.kind === 'recording' ? 'webm' : 'png'} · {when(capture.minutesAgo)}
        </span>
        <button type="button" onClick={onClose} className="rounded p-0.5 text-subtle hover:bg-surface-raised hover:text-primary" aria-label="Close">
          <X className="size-3.5" />
        </button>
      </div>
      <div className="bg-black">{capture.kind === 'recording' ? <Player scene={capture.scene} duration={capture.duration ?? 10} /> : <Desktop scene={capture.scene} t={capture.at} />}</div>
      <div className="flex items-center gap-1.5 px-2 py-1.5">
        {isLive && (
          <Button size="sm" variant="ghost" className="h-7 px-2 text-[12px]" onClick={onWatch}>
            <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
            Watch live
          </Button>
        )}
        <Button size="sm" variant="ghost" className="h-7 px-2 text-[12px]" onClick={onGallery}>
          <Images />
          All of today’s
        </Button>
        {ready && item && openReview && (
          <Button size="sm" variant="primary" className="ml-auto h-7 px-2.5 text-[12px]" onClick={() => openReview({ project: PROJECT, item })} data-pip-review={capture.agent}>
            Review
          </Button>
        )}
      </div>
    </div>
  );
}

// ——— The dock ———

// useDrag moves an element with the pointer, from its spot in the area, and
// tells a click from a drag.
function useDrag({ area, onClick, onDrop }: { area: React.RefObject<HTMLDivElement | null>; onClick?: () => void; onDrop: (at: Spot, moved: boolean) => void }) {
  const [drag, setDrag] = useState<Spot | null>(null);
  const start = useRef<{ px: number; py: number; ox: number; oy: number; moved: boolean } | null>(null);
  const down = (e: ReactPointerEvent<HTMLElement>, origin: Spot) => {
    if ((e.target as HTMLElement).closest('button')) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    start.current = { px: e.clientX, py: e.clientY, ox: origin.x, oy: origin.y, moved: false };
  };
  const move = (e: ReactPointerEvent<HTMLElement>) => {
    const s = start.current;
    if (!s) return;
    const dx = e.clientX - s.px;
    const dy = e.clientY - s.py;
    if (!s.moved && Math.hypot(dx, dy) < 5) return;
    s.moved = true;
    const box = area.current!.getBoundingClientRect();
    setDrag({ x: Math.max(8, Math.min(box.width - TILE - 8, s.ox + dx)), y: Math.max(8, Math.min(box.height - 150, s.oy + dy)) });
  };
  const up = () => {
    const s = start.current;
    start.current = null;
    if (!s) return;
    if (!s.moved) onClick?.();
    else if (drag) onDrop(drag, true);
    setDrag(null);
  };
  return { drag, handlers: (origin: Spot) => ({ onPointerDown: (e: ReactPointerEvent<HTMLElement>) => down(e, origin), onPointerMove: move, onPointerUp: up }) };
}

function Dock({ agents: list, finished, stacked, hidden, onStack, onHide, onGrow, onPin, onFloat, area }: { agents: Live[]; finished: string[]; stacked: boolean; hidden: boolean; onStack: () => void; onHide: (h: boolean) => void; onGrow: (n: string) => void; onPin: (n: string) => void; onFloat: (n: string, at: Spot) => void; area: React.RefObject<HTMLDivElement | null> }) {
  const [fan, setFan] = useState(false);
  if (list.length === 0) return null;
  const at = list.filter((a) => !finished.includes(a.name)).length;
  if (hidden) {
    return (
      <button type="button" onClick={() => onHide(false)} className="absolute right-0 top-4 z-20 flex items-center gap-1.5 rounded-l-xl border border-r-0 border-line-strong bg-overlay py-2 pl-2.5 pr-2 text-[12px] text-secondary shadow-lg backdrop-blur-xl hover:text-title" data-pip-dock="hidden">
        <ChevronLeft className="size-3.5" />
        <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
        {at} live
      </button>
    );
  }
  const deck = stacked && !fan;
  return (
    <div className="absolute right-4 top-3 z-20 md:right-8" style={{ width: TILE }} data-pip-dock={stacked ? 'stacked' : 'column'} onMouseLeave={() => setFan(false)}>
      <div className="mb-1.5 flex items-center gap-1 px-0.5 text-[11px] text-subtle">
        <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
        <span className="flex-1">{at} at work</span>
        <Tip label={stacked ? 'Spread out' : 'Stack'}>
          <button type="button" onClick={onStack} className="rounded p-1 hover:bg-surface-raised hover:text-primary" data-pip-stack>
            {stacked ? <Rows3 className="size-3.5" /> : <Layers className="size-3.5" />}
          </button>
        </Tip>
        <Tip label="Hide the dock">
          <button type="button" onClick={() => onHide(true)} className="rounded p-1 hover:bg-surface-raised hover:text-primary">
            <ChevronRight className="size-3.5" />
          </button>
        </Tip>
      </div>
      <div className="relative" style={{ height: deck ? 140 + (list.length - 1) * 9 : undefined }} onMouseEnter={() => stacked && setFan(true)}>
        {list.map((a, i) => (
          <DockTile key={a.name} agent={a} index={i} deck={deck} count={list.length} finished={finished.includes(a.name)} area={area} onGrow={() => onGrow(a.name)} onPin={() => onPin(a.name)} onFloat={(p) => onFloat(a.name, p)} />
        ))}
      </div>
    </div>
  );
}

function DockTile({ agent, index, deck, count, finished, area, onGrow, onPin, onFloat }: { agent: Live; index: number; deck: boolean; count: number; finished: boolean; area: React.RefObject<HTMLDivElement | null>; onGrow: () => void; onPin: () => void; onFloat: (at: Spot) => void }) {
  const self = useRef<HTMLDivElement>(null);
  // Let go near the dock, it stays docked.
  const { drag, handlers } = useDrag({ area, onClick: onGrow, onDrop: (p) => p.x < area.current!.clientWidth - TILE * 1.6 && onFloat(p) });
  const origin = () => {
    const box = area.current!.getBoundingClientRect();
    const r = self.current!.getBoundingClientRect();
    return { x: r.left - box.left, y: r.top - box.top };
  };
  const style: React.CSSProperties = deck
    ? { position: 'absolute', top: index * 9, left: 0, right: 0, zIndex: count - index, transform: `scale(${1 - index * 0.05})`, transformOrigin: 'top center', opacity: index > 2 ? 0 : 1 }
    : { position: 'relative', marginBottom: 10 };
  return (
    <>
      <div
        ref={self}
        style={{ ...style, visibility: drag ? 'hidden' : undefined, transition: 'transform .2s, top .2s' }}
        onPointerDown={(e) => handlers(origin()).onPointerDown(e)}
        onPointerMove={(e) => handlers(origin()).onPointerMove(e)}
        onPointerUp={() => handlers(origin()).onPointerUp()}
        data-pip-tile={agent.name}
      >
        <Tile agent={agent} finished={finished} onPin={onPin} onGrow={onGrow} />
      </div>
      {drag && (
        <div className="pointer-events-none fixed z-50 rotate-1 opacity-95" style={{ left: area.current!.getBoundingClientRect().left + drag.x, top: area.current!.getBoundingClientRect().top + drag.y, width: TILE }}>
          <Tile agent={agent} finished={finished} />
        </div>
      )}
    </>
  );
}

// Tile is one small live desktop.
function Tile({ agent, finished, onPin, onGrow, onDock }: { agent: Live; finished: boolean; onPin?: () => void; onGrow?: () => void; onDock?: () => void }) {
  const openReview = useOpenReview();
  return (
    <div className="group relative cursor-grab overflow-hidden rounded-xl border border-line-strong bg-black shadow-[0_18px_40px_-18px_rgb(0_0_0/0.8)] active:cursor-grabbing">
      <Desktop scene={finished ? 'testdone' : agent.scene} t={finished ? 0 : undefined} />
      <div className="absolute inset-x-0 bottom-0 flex items-center gap-1.5 bg-gradient-to-t from-black/85 via-black/50 to-transparent px-2 pb-1.5 pt-5 text-[11px] text-white">
        <span className={`size-1.5 shrink-0 rounded-full ${finished ? 'bg-amber-300' : 'animate-pulse bg-emerald-400'}`} />
        <span className="shrink-0 font-medium">{agent.name}</span>
        <span className="min-w-0 truncate text-white/70">{agent.title}</span>
      </div>
      <div className="absolute right-1 top-1 flex gap-0.5 opacity-0 transition group-hover:opacity-100">
        {onDock && <TileButton label="Back to the dock" onClick={onDock}><Minimize2 /></TileButton>}
        {onGrow && <TileButton label="Grow" onClick={onGrow}><Maximize2 /></TileButton>}
        {onPin && <TileButton label="Pin beside the chat" onClick={onPin}><PanelRightOpen /></TileButton>}
      </div>
      {finished && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-1.5 bg-black/55 text-[12px] text-white backdrop-blur-[1px]" data-pip-finished={agent.name}>
          <span className="font-medium">Finished · ready to review</span>
          <button type="button" onClick={() => openReview?.({ project: PROJECT })} className="rounded-md bg-brand-500 px-2.5 py-1 font-medium hover:bg-brand-400">
            Review
          </button>
        </div>
      )}
    </div>
  );
}

function TileButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <Tip label={label}>
      <button type="button" onClick={onClick} className="flex size-6 items-center justify-center rounded-md bg-black/60 text-white/90 backdrop-blur hover:bg-black/80 [&_svg]:size-3.5">
        {children}
      </button>
    </Tip>
  );
}

function Floating({ agent, at, finished, area, onMove, onDock, onGrow }: { agent: Live; at: Spot; finished: boolean; area: React.RefObject<HTMLDivElement | null>; onMove: (at: Spot) => void; onDock: () => void; onGrow: () => void }) {
  const { drag, handlers } = useDrag({ area, onClick: onGrow, onDrop: onMove });
  const p = drag ?? at;
  return (
    <div className="absolute z-30 animate-pop-in" style={{ left: p.x, top: p.y, width: TILE }} {...handlers(at)} data-pip-float={agent.name}>
      <Tile agent={agent} finished={finished} onDock={onDock} onGrow={onGrow} />
    </div>
  );
}

// Big is a desktop grown over the chat, still floating: it can be moved,
// pinned beside the chat, or shrunk back into the dock.
function Big({ agent, finished, area, onPin, onShrink }: { agent: Live; finished: boolean; area: React.RefObject<HTMLDivElement | null>; onPin: () => void; onShrink: () => void }) {
  const [at, setAt] = useState<Spot | null>(null);
  const width = Math.min(680, (area.current?.clientWidth ?? 1200) * 0.55);
  const start = useRef<{ px: number; py: number; ox: number; oy: number } | null>(null);
  const self = useRef<HTMLDivElement>(null);
  return (
    <div
      ref={self}
      className="absolute z-30 animate-pop-in overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)]"
      style={at ? { left: at.x, top: at.y, width } : { right: 32 + TILE + 16, top: 12, width }}
      data-pip-big={agent.name}
    >
      <div
        className="flex cursor-grab items-center gap-2 border-b border-line px-3 py-2 active:cursor-grabbing"
        onPointerDown={(e) => {
          if ((e.target as HTMLElement).closest('button')) return;
          e.currentTarget.setPointerCapture(e.pointerId);
          const box = area.current!.getBoundingClientRect();
          const r = self.current!.getBoundingClientRect();
          start.current = { px: e.clientX, py: e.clientY, ox: r.left - box.left, oy: r.top - box.top };
        }}
        onPointerMove={(e) => {
          const s = start.current;
          if (s) setAt({ x: s.ox + e.clientX - s.px, y: Math.max(0, s.oy + e.clientY - s.py) });
        }}
        onPointerUp={() => (start.current = null)}
      >
        <span className={`size-2 rounded-full ${finished ? 'bg-amber-300' : 'animate-pulse bg-emerald-400'}`} />
        <span className="text-[13px] font-semibold text-title">{agent.name}</span>
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted">{agent.title}</span>
        {!finished && <span className="rounded bg-rose-500/15 px-1.5 py-px text-[10px] font-semibold tracking-wide text-rose-300">LIVE</span>}
        <Tip label="Take over its mouse and keyboard">
          <Button size="icon-sm" variant="ghost" onClick={() => toast('Concept: this would hand you the agent’s mouse and keyboard, as the Desktop tab does.')}>
            <MousePointer2 />
          </Button>
        </Tip>
        <Tip label="Open the agent">
          <Button size="icon-sm" variant="ghost" onClick={() => toast('Concept: this would open the agent’s page.')}>
            <ArrowUpRight />
          </Button>
        </Tip>
        <Tip label="Pin beside the chat">
          <Button size="icon-sm" variant="ghost" onClick={onPin} data-pip-pin>
            <PanelRightOpen />
          </Button>
        </Tip>
        <Tip label="Back to the dock">
          <Button size="icon-sm" variant="ghost" onClick={onShrink} data-pip-shrink>
            <Minimize2 />
          </Button>
        </Tip>
      </div>
      <div className="bg-black">
        <Desktop scene={finished ? 'testdone' : agent.scene} t={finished ? 0 : undefined} />
      </div>
      <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-muted">
        <span className="text-subtle">Now</span>
        <span className="min-w-0 flex-1 truncate text-secondary">{finished ? 'Finished: ready for your review' : agent.doing}</span>
        <span className="text-faint">{agent.since} min at work</span>
      </div>
    </div>
  );
}

// Pinned is a desktop in a column beside the chat, in the place of v2's
// right-hand panel, with what it captured today under it.
function Pinned({ agent, working, finished, captures, onSwitch, onUnpin, onGallery }: { agent: Live; working: Live[]; finished: string[]; captures: Capture[]; onSwitch: (name: string) => void; onUnpin: () => void; onGallery: (open: string) => void }) {
  const mine = captures.filter((c) => c.agent === agent.name).sort((a, b) => a.minutesAgo - b.minutesAgo);
  const done = finished.includes(agent.name);
  return (
    <aside className="flex w-[44%] min-w-[420px] max-w-[820px] shrink-0 animate-fade-in flex-col border-l border-line bg-ink" data-pip-pinned={agent.name}>
      <div className="flex items-center gap-1 border-b border-line px-2 py-1.5">
        <div className="flex min-w-0 flex-1 gap-1 overflow-x-auto [scrollbar-width:none]">
          {working.map((a) => (
            <button key={a.name} type="button" onClick={() => onSwitch(a.name)} className={`flex shrink-0 items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] ${a.name === agent.name ? 'bg-surface-raised text-title' : 'text-muted hover:bg-surface hover:text-primary'}`}>
              <span className={`size-1.5 rounded-full ${finished.includes(a.name) ? 'bg-amber-300' : 'bg-emerald-400'}`} />
              {a.name}
            </button>
          ))}
        </div>
        <Tip label="Unpin">
          <Button size="icon-sm" variant="ghost" onClick={onUnpin} data-pip-unpin>
            <PanelRightClose />
          </Button>
        </Tip>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        <div className="mb-2 flex items-center gap-2">
          <span className={`size-2 rounded-full ${done ? 'bg-amber-300' : 'animate-pulse bg-emerald-400'}`} />
          <span className="text-[13px] font-semibold text-title">{agent.title}</span>
          {!done && <span className="ml-auto rounded bg-rose-500/15 px-1.5 py-px text-[10px] font-semibold tracking-wide text-rose-300">LIVE</span>}
        </div>
        <div className="overflow-hidden rounded-xl border border-line-strong bg-black">
          <Desktop scene={done ? 'testdone' : agent.scene} t={done ? 0 : undefined} />
        </div>
        <div className="mt-2 text-[12px] text-muted">
          <span className="text-subtle">Now</span> <span className="text-secondary">{done ? 'Finished: ready for your review' : agent.doing}</span>
        </div>
        {mine.length > 0 && (
          <>
            <div className="mb-2 mt-5 text-[11px] font-medium uppercase tracking-wide text-faint">Captured today</div>
            <div className="grid grid-cols-3 gap-2">
              {mine.map((c) => (
                <Thumb key={c.id} capture={c} onClick={() => onGallery(c.id)} />
              ))}
            </div>
          </>
        )}
      </div>
    </aside>
  );
}

// ——— Today's gallery ———

function Thumb({ capture, onClick, wide }: { capture: Capture; onClick: () => void; wide?: boolean }) {
  return (
    <button type="button" onClick={onClick} className="group text-left" data-pip-thumb={capture.id}>
      <div className="relative overflow-hidden rounded-lg border border-line bg-black transition group-hover:border-line-heavy">
        <Desktop scene={capture.scene} t={capture.at} />
        {capture.kind === 'recording' && (
          <span className="absolute bottom-1 right-1 flex items-center gap-0.5 rounded bg-black/70 px-1 text-[10px] text-white">
            <Play className="size-2.5 fill-white" />
            0:{String(capture.duration).padStart(2, '0')}
          </span>
        )}
      </div>
      {wide && (
        <div className="mt-1.5 flex items-center gap-1.5 text-[12px]">
          <span className="font-medium text-secondary">{capture.agent}</span>
          <span className="min-w-0 flex-1 truncate text-subtle">{capture.name}</span>
          <span className="shrink-0 text-faint">{capture.minutesAgo < 1 ? 'now' : `${capture.minutesAgo < 60 ? `${capture.minutesAgo}m` : `${Math.floor(capture.minutesAgo / 60)}h`}`}</span>
        </div>
      )}
    </button>
  );
}

function Gallery({ state, captures, finished, working, onState, onWatch }: { state: { agent?: string; open?: string } | null; captures: Capture[]; finished: string[]; working: Live[]; onState: (s: { agent?: string; open?: string } | null) => void; onWatch: (name: string) => void }) {
  const [kind, setKind] = useState<'all' | 'recording' | 'screenshot'>('all');
  const now = useNow();
  const openReview = useOpenReview();
  const review = useProjectReview(PROJECT);
  const list = captures.filter((c) => (!state?.agent || c.agent === state.agent) && (kind === 'all' || c.kind === kind)).sort((a, b) => a.minutesAgo - b.minutesAgo);
  const who = [...new Set(captures.map((c) => c.agent))];
  const open = state?.open ? captures.find((c) => c.id === state.open) : undefined;
  const at = open ? list.findIndex((c) => c.id === open.id) : -1;
  const groups: [string, Capture[]][] = [
    ['In the last hour', list.filter((c) => c.minutesAgo < 60)],
    ['Earlier today', list.filter((c) => c.minutesAgo >= 60)],
  ];
  const isLive = (name: string) => working.some((a) => a.name === name) && !finished.includes(name);
  const item = open && review.data?.items?.find((i) => i.agent === open.agent);
  return (
    <Dialog open={state !== null} onOpenChange={(o) => !o && onState(null)}>
      <DialogContent className="flex h-[86vh] max-w-6xl flex-col gap-0 p-0" data-pip-gallery>
        <div className="flex items-center gap-3 border-b border-line px-6 py-4 pr-14">
          <div>
            <DialogTitle className="text-[15px] font-semibold text-title">Today</DialogTitle>
            <p className="text-[12px] text-subtle" data-now={now}>
              {captures.length} captures from {who.length} agents in agentbox
            </p>
          </div>
          <div className="ml-auto flex rounded-lg border border-line p-0.5 text-[12px]">
            {(['all', 'recording', 'screenshot'] as const).map((k) => (
              <button key={k} type="button" onClick={() => setKind(k)} className={`rounded-md px-2.5 py-1 ${kind === k ? 'bg-surface-raised text-title' : 'text-muted hover:text-primary'}`}>
                {k === 'all' ? 'All' : k === 'recording' ? 'Recordings' : 'Screenshots'}
              </button>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap gap-1.5 border-b border-line px-6 py-2.5 text-[12px]">
          <Chip on={!state?.agent} onClick={() => onState({})}>
            All agents
          </Chip>
          {who.map((name) => (
            <Chip key={name} on={state?.agent === name} onClick={() => onState({ agent: name })}>
              <span className={`size-1.5 rounded-full ${isLive(name) ? 'animate-pulse bg-emerald-400' : 'bg-amber-300'}`} />
              {name}
              <span className="text-faint">{captures.filter((c) => c.agent === name).length}</span>
            </Chip>
          ))}
        </div>
        {open ? (
          <div className="flex min-h-0 flex-1 flex-col gap-3 p-6" data-pip-viewer={open.id}>
            <div className="flex items-center gap-2 text-[13px]">
              <Button size="sm" variant="ghost" onClick={() => onState({ agent: state?.agent })}>
                <ChevronLeft />
                All
              </Button>
              <span className="font-medium text-title">{open.agent}</span>
              <span className="text-muted">
                {open.name}.{open.kind === 'recording' ? 'webm' : 'png'} · {when(open.minutesAgo)}
              </span>
              <div className="ml-auto flex items-center gap-1.5">
                {isLive(open.agent) && (
                  <Button size="sm" variant="ghost" onClick={() => onWatch(open.agent)}>
                    <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
                    Watch live
                  </Button>
                )}
                {item && openReview && (
                  <Button size="sm" variant="primary" onClick={() => { onState(null); openReview({ project: PROJECT, item }); }}>
                    Review {open.agent}
                  </Button>
                )}
              </div>
            </div>
            <div className="relative flex min-h-0 flex-1 items-center justify-center">
              <div className="aspect-[16/10] max-h-full w-full max-w-[1000px] overflow-hidden rounded-xl border border-line-strong bg-black" style={{ maxWidth: 'min(1000px, calc((86vh - 220px) * 1.6))' }}>
                {open.kind === 'recording' ? <Player key={open.id} scene={open.scene} duration={open.duration ?? 10} /> : <Desktop scene={open.scene} t={open.at} />}
              </div>
              {at > 0 && <NavButton side="left" onClick={() => onState({ ...state, open: list[at - 1].id })} />}
              {at >= 0 && at < list.length - 1 && <NavButton side="right" onClick={() => onState({ ...state, open: list[at + 1].id })} />}
            </div>
          </div>
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
            {groups.map(
              ([title, items]) =>
                items.length > 0 && (
                  <section key={title} className="mb-6">
                    <h3 className="mb-2.5 text-[11px] font-medium uppercase tracking-wide text-faint">{title}</h3>
                    <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-4">
                      {items.map((c) => (
                        <Thumb key={c.id} capture={c} wide onClick={() => onState({ ...state, open: c.id })} />
                      ))}
                    </div>
                  </section>
                ),
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" onClick={onClick} className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 ${on ? 'border-brand-400/60 bg-brand-500/15 text-title' : 'border-line text-muted hover:text-primary'}`}>
      {children}
    </button>
  );
}

function NavButton({ side, onClick }: { side: 'left' | 'right'; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={`absolute top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full border border-line-strong bg-overlay text-secondary shadow-lg hover:text-title ${side === 'left' ? 'left-0' : 'right-0'}`}>
      {side === 'left' ? <ChevronLeft className="size-4" /> : <ChevronRight className="size-4" />}
    </button>
  );
}

