import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { thumbKey, tileHue } from '../../lib/pages';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';

// HatchMark is Hatch's egg with a crack through it, as Settings → Connectors
// draws it.
export function HatchMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn('size-4', className)} fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M8 1.8c2.6 0 4.6 3.4 4.6 6.4a4.6 4.6 0 0 1-9.2 0C3.4 5.2 5.4 1.8 8 1.8Z" />
      <path d="m4 8.4 1.8 1.4L8 7.8l2.2 2L12 8.4" />
    </svg>
  );
}

// PageIcon is a page with its corner folded, the mark of pages on an agent's
// row and in the tabs.
export function PageIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={cn('size-4', className)} fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M4 1.75h5.25L12.5 5v8.25a1 1 0 0 1-1 1h-7.5a1 1 0 0 1-1-1V2.75a1 1 0 0 1 1-1Z" />
      <path d="M9 1.75V5.25h3.5M5.5 8.5h5M5.5 11h3.25" />
    </svg>
  );
}

// AgentName is who made a page: an agent by its title, the project's chat
// as such.
export function AgentName({ agentRef }: { agentRef: string }) {
  return <>{useAgentLabel()(agentRef)}</>;
}

// useAgentLabel names agents the way AgentName does, for a search.
export function useAgentLabel(): (ref: string) => string {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, staleTime: 30_000 });
  return (ref) => {
    const name = ref.split('/')[1] ?? ref;
    if (name === T.LeadName) return t('chat.project.agentTitle');
    return agents.data?.find((a) => a.ref === ref)?.title || name;
  };
}

// TitleTile is a page drawn from its title, for while its snapshot is on its
// way and for when its HTML can't be had: a sheet in the page's own colour,
// with a header bar, its title and lines of text.
export function TitleTile({ page, className }: { page: Pick<T.Artifact, 'id' | 'title'>; className?: string }) {
  const hue = tileHue(page.id);
  return (
    <span
      aria-hidden
      className={cn('absolute inset-0 flex flex-col overflow-hidden bg-white text-left', className)}
      style={{ background: `linear-gradient(160deg, hsl(${hue} 70% 97%), hsl(${(hue + 40) % 360} 60% 93%))` }}
      data-page-tile
    >
      <span className="h-[14%] shrink-0" style={{ background: `linear-gradient(90deg, hsl(${hue} 65% 55%), hsl(${(hue + 30) % 360} 70% 62%))` }} />
      <span className="flex min-h-0 flex-1 flex-col gap-[6%] px-[8%] pt-[7%]">
        <span className="line-clamp-2 font-semibold leading-[1.15] tracking-tight" style={{ color: `hsl(${hue} 45% 22%)`, fontSize: 'clamp(7px, 11cqw, 22px)' }}>
          {page.title}
        </span>
        <span className="flex flex-col gap-[5px]">
          <span className="h-[3px] w-[92%] rounded-full" style={{ background: `hsl(${hue} 30% 70% / 0.55)` }} />
          <span className="h-[3px] w-[78%] rounded-full" style={{ background: `hsl(${hue} 30% 70% / 0.55)` }} />
          <span className="h-[3px] w-[85%] rounded-full" style={{ background: `hsl(${hue} 30% 70% / 0.4)` }} />
        </span>
      </span>
    </span>
  );
}

// PageThumb is a page's thumbnail: its first screen, drawn offscreen by the
// main process once per version and kept, asked for only once it scrolls
// near the screen, so a list of hundreds draws what you look at. Until then,
// and when the HTML can't be had, its TitleTile.
export function PageThumb({ project, page, className, eager }: { project: string; page: T.Artifact; className?: string; eager?: boolean }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [near, setNear] = useState(!!eager);
  useEffect(() => {
    if (near || !ref.current) return;
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && setNear(true), { rootMargin: '200px' });
    io.observe(ref.current);
    return () => io.disconnect();
  }, [near]);
  const thumb = useQuery({
    queryKey: ['pageThumb', thumbKey(page)],
    queryFn: () => api.pageThumb(project, page.id, thumbKey(page)),
    enabled: near,
    staleTime: Infinity,
    gcTime: 30 * 60_000,
    retry: false,
  });
  const [shown, setShown] = useState(false);
  return (
    <span ref={ref} className={cn('relative block aspect-[16/10] overflow-hidden bg-white [container-type:inline-size]', className)} data-page-thumb={page.id}>
      <TitleTile page={page} />
      {thumb.data && (
        <img
          src={thumb.data}
          alt=""
          draggable={false}
          onLoad={() => setShown(true)}
          className={cn('absolute inset-0 size-full object-cover object-top transition-opacity duration-300', shown ? 'opacity-100' : 'opacity-0')}
        />
      )}
    </span>
  );
}
