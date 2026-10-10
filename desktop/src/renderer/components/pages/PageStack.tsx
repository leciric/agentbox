import { X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { isHomeChat } from '../../lib/api';
import { isLeadRef, markSeen, matchesPage, openPage, stackPages, thumbKey, watchChat } from '../../lib/pages';
import { useT } from '../../lib/i18n';
import { useNow } from '../../lib/useNow';
import { cn } from '../../lib/utils';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';
import { PageRow, PageSearch } from './PageList';
import { AgentName, PageThumb, useAgentLabel } from './PageThumb';
import { useProjectPages } from './usePages';

// How many pages the stack holds face up; the rest are "+N".
const shownMax = 3;
// A card's size fanned out; piled up it is smaller, and sits lower, as its
// caption is hidden.
const cardW = 156;
const gap = 10;
const piled = 0.62;
const captionH = 38;
// Where each card lies in the pile, front first: a little askew, so it reads
// as a stack of sheets.
const pile = [
  { x: 0, y: 0, r: 0 },
  { x: -7, y: -4, r: -6 },
  { x: 6, y: -7, r: 5 },
];
const fan = [-1.5, 1.2, -0.8];

// PageStack is the pages a chat's agents published or updated that you
// haven't opened, piled at the composer's corner: in the lead's chat every
// agent's, in an agent's its own. Nothing shows until there's one. Pointing
// at it fans them out with their titles and agents; clicking one opens it,
// and a page opened leaves the stack. A page arriving drops onto the pile.
export function PageStack({ agent }: { agent: T.Agent }) {
  const t = useT();
  const project = agent.project;
  const query = useProjectPages(isHomeChat(agent.ref) ? null : project);
  const now = useNow(60_000);
  const list = stackPages(query.data, agent.ref, now);
  const [open, setOpen] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const leave = useRef<number>(undefined);
  // The cards that were on the pile at the last render, so only one that
  // wasn't drops in.
  const known = useRef<Set<string> | null>(null);
  const keys = list.map(thumbKey);
  const arrived = known.current ? new Set(keys.filter((k) => !known.current!.has(k))) : new Set(keys);
  useEffect(() => {
    known.current = new Set(keys);
  });
  useEffect(() => () => window.clearTimeout(leave.current), []);
  // While this chat is on screen, a page arriving on its stack needs no toast.
  useEffect(() => watchChat(agent.ref), [agent.ref]);

  if (list.length === 0) return null;
  const shown = list.slice(0, shownMax);
  const more = list.length - shown.length;
  const fanned = open || listOpen;
  const lead = isLeadRef(agent.ref);

  return (
    <div
      className="pointer-events-none absolute bottom-full right-2 z-10 mb-3 md:right-3"
      style={{ width: cardW, height: cardW * 0.625 + 40 }}
      data-page-stack={fanned ? 'open' : 'closed'}
      aria-label={t('pages.stack.label', { count: list.length })}
      role="group"
      onMouseEnter={() => {
        window.clearTimeout(leave.current);
        setOpen(true);
      }}
      onMouseLeave={() => {
        leave.current = window.setTimeout(() => setOpen(false), 220);
      }}
      onFocus={() => setOpen(true)}
      onBlur={(e) => !e.currentTarget.contains(e.relatedTarget) && setOpen(false)}
    >
      {shown
        .map((page, i) => {
          const p = pile[i];
          const x = fanned ? -i * (cardW + gap) : p.x;
          const transform = fanned ? `translate(${x}px, ${-Math.min(i, 1) * 4}px) rotate(${fan[i]}deg)` : `translate(${x}px, ${p.y + captionH * piled}px) rotate(${p.r}deg) scale(${piled})`;
          return (
            <div
              key={page.id}
              className="pointer-events-auto absolute bottom-0 right-0 origin-bottom-right transition-transform duration-300 ease-[cubic-bezier(.2,.9,.25,1)] motion-reduce:transition-none"
              style={{ width: cardW, transform, zIndex: shownMax - i, transitionDelay: fanned ? `${i * 25}ms` : '0ms' }}
            >
              <button
                onClick={() => openPage(project, page)}
                data-stack-page={page.id}
                className={cn('group block w-full rounded-xl text-left focus-visible:outline-none', arrived.has(thumbKey(page)) && 'animate-page-drop')}
                aria-label={page.title}
              >
                <PageThumb
                  project={project}
                  page={page}
                  eager
                  className={cn(
                    'rounded-xl ring-1 ring-black/10 shadow-[0_14px_32px_-14px_var(--ab-shadow-deep),0_2px_6px_-2px_var(--ab-shadow-soft)] transition group-hover:ring-2 group-hover:ring-brand-400/70 group-focus-visible:ring-2 group-focus-visible:ring-brand-400',
                    i === 0 && arrived.has(thumbKey(page)) && 'animate-page-glow',
                  )}
                />
                <span
                  className={cn(
                    'mt-1.5 grid gap-px rounded-lg border border-line bg-composer/90 px-2 py-1 shadow-[0_8px_20px_-14px_var(--ab-shadow-deep)] backdrop-blur-xl transition-opacity duration-200',
                    fanned ? 'opacity-100' : 'pointer-events-none opacity-0',
                  )}
                >
                  <span className="truncate text-[12px] font-medium leading-tight text-title">{page.title}</span>
                  <span className="truncate text-[10.5px] leading-tight text-subtle">
                    <AgentName agentRef={page.agent} />
                  </span>
                </span>
              </button>
            </div>
          );
        })
        .reverse()}

      {/* The count, at the pile's corner, piled up; the rest in a list, fanned out. */}
      {more > 0 && (
        <MoreList
          project={project}
          pages={list}
          lead={lead}
          now={now}
          more={more}
          fanned={fanned}
          open={listOpen}
          onOpenChange={setListOpen}
          style={fanned ? { right: shown.length * (cardW + gap), bottom: captionH + cardW * 0.3125 - 16 } : { right: cardW * piled - 18, bottom: cardW * 0.625 * piled - 10 }}
        />
      )}
      {!more && list.length > 1 && !fanned && (
        <span
          className="pointer-events-none absolute z-10 flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-brand-500 px-1 text-[10.5px] font-semibold tabular-nums text-white shadow"
          style={{ right: -7, bottom: cardW * 0.625 * piled - 11 }}
        >
          {list.length}
        </span>
      )}

      {fanned && (
        <Tip label={t('pages.stack.dismiss')}>
          <button
            aria-label={t('pages.stack.dismiss')}
            data-stack-dismiss
            onClick={() => markSeen(list)}
            className="pointer-events-auto absolute -right-2 -top-2 z-20 flex size-6 animate-fade-in items-center justify-center rounded-full border border-line-strong bg-overlay text-subtle shadow-md transition hover:text-primary"
            style={{ top: -8 }}
          >
            <X className="size-3.5" />
          </button>
        </Tip>
      )}
    </div>
  );
}

// MoreList is "+N": piled up, a count; fanned out, a button that opens every
// new page in a list with a search.
function MoreList({
  project,
  pages,
  lead,
  now,
  more,
  fanned,
  open,
  onOpenChange,
  style,
}: {
  project: string;
  pages: T.Artifact[];
  lead: boolean;
  now: number;
  more: number;
  fanned: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  style: React.CSSProperties;
}) {
  const t = useT();
  const [query, setQuery] = useState('');
  const label = useAgentLabel();
  const found = pages.filter((a) => matchesPage(a, query, label(a.agent)));
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <button
          data-stack-more={more}
          aria-label={t('pages.stack.more', { count: more })}
          className={cn(
            'pointer-events-auto absolute z-10 flex items-center justify-center rounded-full font-semibold tabular-nums transition-all duration-300',
            fanned
              ? 'h-8 border border-line-strong bg-composer/90 px-3 text-[12px] text-secondary shadow-md backdrop-blur-xl hover:border-line-vivid hover:text-title'
              : 'h-[22px] min-w-[26px] bg-brand-500 px-1.5 text-[11px] text-white shadow',
          )}
          style={style}
        >
          +{more}
        </button>
      </PopoverTrigger>
      <PopoverContent side="top" align="end" className="w-80 p-2" data-stack-list>
        <PageSearch value={query} onChange={setQuery} autoFocus className="mb-1.5" />
        <div className="max-h-80 overflow-y-auto">
          {found.map((page) => (
            <PageRow
              key={page.id}
              project={project}
              page={page}
              showAgent={lead}
              now={now}
              onOpen={() => {
                onOpenChange(false);
                openPage(project, page);
              }}
            />
          ))}
          {found.length === 0 && <p className="px-2 py-4 text-center text-[12.5px] text-subtle">{t('pages.noMatch')}</p>}
        </div>
      </PopoverContent>
    </Popover>
  );
}
