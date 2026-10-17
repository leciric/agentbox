import { useQueries, useQuery } from '@tanstack/react-query';
import { Film, Image as ImageIcon, Images, X } from 'lucide-react';
import { use, useMemo, useState, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { mediaUrl } from '../../lib/media';
import { mediaQuery } from '../../lib/mediaPages';
import { pastAgentsKey } from '../../lib/pastAgents';
import { captureKinds, latestBy, mentionsIn, reported } from '../../lib/pip';
import { setLane } from '../../lib/pipStore';
import { useInUse } from './PipLane';
import { timeAgo } from '../../lib/utils';
import { Button } from '../ui/button';
import { useOpenReview, useProjectReview } from '../v2/ReviewV2';
import { AgentMentions, MessageMentions, type Mentions } from './mentionChip';

export function AgentMentionsProvider({ project, onSelect, children }: { project: string; onSelect: (view: View) => void; children: ReactNode }) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const past = useQuery({ queryKey: [...pastAgentsKey, 'mentions', project], queryFn: () => api.archivedAgents({ project, limit: 200 }), staleTime: 60_000 });
  const names = useMemo(
    () => new Set([...(agents.data ?? []).filter((a) => a.project === project).map((a) => a.name), ...(past.data ?? []).map((a) => a.name)]),
    [agents.data, past.data, project],
  );
  const inUse = useInUse(project);
  const liveKey = inUse.map((a) => a.name).join(',');
  const live = useMemo(() => new Set(liveKey ? liveKey.split(',') : []), [liveKey]);
  const value = useMemo(
    () => ({
      project,
      names,
      live,
      onWatch: (ref: string) => setLane(project, () => ({ big: ref, folded: false })),
      onToday: () => onSelect({ kind: 'today', project }),
    }),
    [project, names, live, onSelect],
  );
  return <AgentMentions value={value}>{children}</AgentMentions>;
}

// captures is an agent's latest screenshots and recordings, newest first.
const capturesQuery = (project: string, name: string) => ({
  queryKey: ['pipCaptures', project, name],
  queryFn: async () => {
    const q = mediaQuery({ project, agent: name, kinds: captureKinds });
    q.set('limit', '20');
    return (await api.mediaPage(q)).items ?? [];
  },
  staleTime: 30_000,
});

// MentionScope is one of the lead's messages: what it says, and under it
// the capture of the agent open in it.
export function MentionScope({ item, children }: { item: T.ChatItem; children: ReactNode }) {
  const mentions = use(AgentMentions);
  const names = useMemo(() => (mentions ? mentionsIn(item.text ?? '', mentions.names) : []), [mentions, item.text]);
  if (!mentions || names.length === 0) return children;
  return (
    <Scope mentions={mentions} item={item} names={names} reports={!item.streaming}>
      {children}
    </Scope>
  );
}

function Scope({ mentions, item, names, reports, children }: { mentions: Mentions; item: T.ChatItem; names: string[]; reports: boolean; children: ReactNode }) {
  const captures = useQueries({ queries: names.map((n) => capturesQuery(mentions.project, n)) });
  // Until a chip is clicked, a message opens on the agent it names first, its
  // subject ("agent-14 has the export working…"), when that agent captured
  // something for it. Not on one that finished: its evidence card, where it
  // finished, shows its proof already (chat/EvidenceCard.tsx).
  const review = useProjectReview(mentions.project);
  const [chosen, setChosen] = useState<string | null | undefined>(undefined);
  const done = review.data?.items?.some((i) => i.agent === names[0]);
  const auto = reports && !done && reported(captures[0].data ?? [], item.createdAt) ? names[0] : undefined;
  const open = chosen === undefined ? (auto ?? null) : chosen;
  const at = names.indexOf(open ?? '');
  const items = at >= 0 ? (captures[at].data ?? []) : [];
  const shown = open ? (open === auto && chosen === undefined ? reported(items, item.createdAt) : latestBy(items, item.createdAt)) : undefined;
  const scope = useMemo(() => ({ open, toggle: (name: string) => setChosen(open === name ? null : name) }), [open]);
  return (
    <MessageMentions value={scope}>
      {children}
      {open && <CaptureCard mentions={mentions} name={open} item={shown} loading={captures[at]?.isPending} onClose={() => setChosen(null)} />}
    </MessageMentions>
  );
}

function CaptureCard({ mentions, name, item, loading, onClose }: { mentions: Mentions; name: string; item: T.MediaItem | undefined; loading?: boolean; onClose: () => void }) {
  const t = useT();
  const openReview = useOpenReview();
  const review = useProjectReview(mentions.project);
  const ready = review.data?.items?.find((i) => i.agent === name);
  const ref = `${mentions.project}/${name}`;
  return (
    <div className="mb-1 mt-3 max-w-[400px] animate-pop-in overflow-hidden rounded-xl border border-line-strong bg-surface shadow-[0_20px_50px_-30px_var(--ab-shadow-deep)]" data-pip-capture={name}>
      <div className="flex items-center gap-2 py-1.5 pl-3 pr-1.5 text-[12px]">
        {item?.kind === 'recording' ? <Film className="size-3.5 shrink-0 text-subtle" /> : <ImageIcon className="size-3.5 shrink-0 text-subtle" />}
        <span className="min-w-0 flex-1 truncate text-muted">
          <span className="font-medium text-secondary">{name}</span>
          {item && ` · ${item.name} · ${timeAgo(item.createdAt)}`}
        </span>
        <button type="button" onClick={onClose} className="rounded p-1 text-subtle hover:bg-surface-raised hover:text-primary" aria-label={t('common.close')}>
          <X className="size-3.5" />
        </button>
      </div>
      {item ? (
        <div className="bg-stage">
          {item.kind === 'recording' ? (
            <video key={item.id} src={mediaUrl(item)} controls autoPlay muted playsInline className="aspect-[16/10] w-full bg-stage" />
          ) : (
            <img src={mediaUrl(item)} alt={item.name} className="aspect-[16/10] w-full object-contain object-top" />
          )}
        </div>
      ) : (
        <div className="border-t border-line px-3 py-6 text-center text-[12px] text-subtle">{loading ? t('common.loading') : t('pip.mention.nothing')}</div>
      )}
      <div className="flex items-center gap-1 px-1.5 py-1">
        {mentions.live.has(name) && (
          <Button size="sm" variant="ghost" className="h-7 px-2 text-[12px]" onClick={() => mentions.onWatch(ref)} data-pip-watch={name}>
            <span className="size-1.5 animate-pulse rounded-full bg-rose-400" />
            {t('pip.mention.watch')}
          </Button>
        )}
        <Button size="sm" variant="ghost" className="h-7 px-2 text-[12px]" onClick={() => mentions.onToday()}>
          <Images />
          {t('pip.mention.today')}
        </Button>
        {ready && openReview && (
          <Button size="sm" variant="primary" className="ml-auto h-7 px-2.5 text-[12px]" onClick={() => openReview({ project: mentions.project, item: ready })} data-pip-review={name}>
            {t('pip.review')}
          </Button>
        )}
      </div>
    </div>
  );
}
