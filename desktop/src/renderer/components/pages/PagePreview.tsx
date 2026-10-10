import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ExternalLink, Globe, Lock, MessageSquareText, RefreshCw, TriangleAlert } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { closePage, isExpired, isLeadRef, pagesKey, useOpenedPage } from '../../lib/pages';
import { requestReveal } from '../../lib/reveal';
import { useT } from '../../lib/i18n';
import { useNow } from '../../lib/useNow';
import { cn, errorMessage, timeAgo } from '../../lib/utils';
import { AIIcon } from '../state';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';
import { Tip } from '../ui/tooltip';
import { AgentName, HatchMark, PageThumb } from './PageThumb';

// PagePreviewHost shows the page something asked to open (lib/pages.ts
// openPage): the stack, a toast, the Pages tab, a Media view, a publish_page
// line. One for the whole window, in App. "Show where it was published"
// goes to the chat that published it, at the call.
export function PagePreviewHost({ onSelect }: { onSelect: (view: View) => void }) {
  const opened = useOpenedPage();
  return (
    <Dialog open={!!opened} onOpenChange={(next) => !next && closePage()}>
      {opened && (
        <PagePreview
          key={`${opened.project}/${opened.page.id}`}
          project={opened.project}
          page={opened.page}
          onReveal={() => {
            const { agent, item } = opened.page;
            closePage();
            const name = agent.split('/')[1];
            requestReveal({ chat: { project: opened.project, agent: isLeadRef(agent) ? undefined : name, item } });
            onSelect(isLeadRef(agent) ? { kind: 'project', project: opened.project, tab: 'chat' } : { kind: 'agent', ref: agent, tab: 'chat' });
          }}
        />
      )}
    </Dialog>
  );
}

function agentAI(agents: T.Agent[] | undefined, ref: string): string {
  return agents?.find((a) => a.ref === ref)?.ai ?? 'claude';
}

// PagePreview is one page, large: what it is, who made it and when, and the
// page itself in a sandboxed frame. The page's HTML comes through the daemon,
// with the Hatch connector's sign-in, since a private page's link only opens
// for the user signed in to Hatch in a browser.
function PagePreview({ project, page, onReveal }: { project: string; page: T.Artifact; onReveal?: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const now = useNow(30_000);
  const [reloads, setReloads] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const preview = useQuery({
    queryKey: ['pagePreview', project, page.id, reloads],
    queryFn: () => api.pagePreview(project, page.id),
    retry: false,
    staleTime: 0,
    gcTime: 0,
  });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, staleTime: 30_000 });
  // What Hatch says, once it has: the title and the version.
  const a = preview.data?.artifact ?? page;
  const status = preview.data?.status;
  const gone = status === T.ArtifactExpired || isExpired(a, now);
  const reload = () => {
    setLoaded(false);
    setReloads((n) => n + 1);
    void queryClient.invalidateQueries({ queryKey: pagesKey(project) });
  };
  const browser = () => void window.agentbox.openExternal(a.url);

  let body: ReactNode;
  if (preview.isPending) {
    body = <div className="skeleton absolute inset-0 rounded-none" />;
  } else if (preview.error) {
    body = <Empty icon={<TriangleAlert className="size-5" />} title={t('pages.unavailable')} text={errorMessage(preview.error)} onBrowser={a.url ? browser : undefined} />;
  } else if (gone) {
    body = <Empty icon={<HatchMark className="size-5" />} title={t('pages.goneTitle')} text={t('pages.goneText')} />;
  } else if (status === T.ArtifactUnavailable) {
    body = <Empty icon={<TriangleAlert className="size-5" />} title={t('pages.unavailable')} text={preview.data.error ?? ''} onBrowser={browser} />;
  } else if (!preview.data.page) {
    body = <Empty icon={<Globe className="size-5" />} title={t('pages.noPreview')} text={t('pages.noPreviewText')} onBrowser={browser} />;
  } else {
    body = (
      <>
        {!loaded && <div className="skeleton absolute inset-0 rounded-none" />}
        <iframe
          key={`${a.version}#${reloads}`}
          title={a.title}
          src={api.pageUrl(project, a.id)}
          // No allow-same-origin: the page gets an opaque origin, and can't
          // reach the app, its storage or the daemon. The daemon's answer
          // carries Hatch's own sandboxing policy too.
          sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals"
          referrerPolicy="no-referrer"
          onLoad={() => setLoaded(true)}
          className={cn('absolute inset-0 size-full border-0 bg-white transition-opacity duration-300', loaded ? 'opacity-100' : 'opacity-0')}
        />
      </>
    );
  }

  return (
    <DialogContent
      className="flex h-[86vh] w-[calc(100vw-3rem)] max-w-[1180px] flex-col gap-0 overflow-hidden p-0"
      data-page-preview={a.id}
      // Focusing the first button would open its tooltip over the page: the
      // dialog itself takes focus, so Escape and Tab still work from the start.
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        (event.currentTarget as HTMLElement | null)?.focus();
      }}
    >
      <div className="flex items-start gap-3 border-b border-line px-5 py-4 pr-14">
        <PageThumb project={project} page={a} eager className={cn('w-16 shrink-0 rounded-md ring-1 ring-line-strong', gone && 'opacity-50 grayscale')} />
        <div className="grid min-w-0 flex-1 gap-1">
          <DialogTitle className="truncate text-[15px] leading-snug">{a.title}</DialogTitle>
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted">
            <span className="flex min-w-0 items-center gap-1.5">
              <AIIcon ai={agentAI(agents.data, a.agent)} className="size-3.5 shrink-0" />
              <span className="truncate text-secondary">
                <AgentName agentRef={a.agent} />
              </span>
            </span>
            <span aria-hidden className="text-faint">
              ·
            </span>
            <Tip label={new Date(a.createdAt).toLocaleString()}>
              <span>{t('pages.published', { when: timeAgo(a.createdAt, now) })}</span>
            </Tip>
            {a.version > 1 && (
              <>
                <span aria-hidden className="text-faint">
                  ·
                </span>
                <Tip label={new Date(a.updatedAt).toLocaleString()}>
                  <span>{t('pages.updated', { n: a.version, when: timeAgo(a.updatedAt, now) })}</span>
                </Tip>
              </>
            )}
            {a.agents.length > 1 && (
              <>
                <span aria-hidden className="text-faint">
                  ·
                </span>
                <span>{t('pages.alsoBy', { count: a.agents.length - 1 })}</span>
              </>
            )}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            {a.public ? (
              <Badge>
                <Globe />
                {t('pages.public')}
              </Badge>
            ) : (
              <Badge>
                <Lock />
                {t('pages.private')}
              </Badge>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {onReveal && a.item && (
            <Tip label={t('pages.revealTip')}>
              <Button size="icon-sm" variant="ghost" onClick={onReveal} aria-label={t('pages.revealTip')}>
                <MessageSquareText />
              </Button>
            </Tip>
          )}
          <Tip label={t('pages.reload')}>
            <Button size="icon-sm" variant="ghost" onClick={reload} disabled={preview.isFetching} aria-label={t('pages.reload')}>
              <RefreshCw className={cn(preview.isFetching && 'animate-spin')} />
            </Button>
          </Tip>
          {a.url && (
            <Button size="sm" variant="secondary" onClick={browser}>
              <ExternalLink />
              {t('pages.openInBrowser')}
            </Button>
          )}
        </div>
      </div>
      <div className="relative min-h-0 flex-1 bg-sunken">{body}</div>
    </DialogContent>
  );
}

function Empty({ icon, title, text, onBrowser }: { icon: ReactNode; title: string; text: string; onBrowser?: () => void }) {
  const t = useT();
  return (
    <div className="absolute inset-0 flex items-center justify-center p-8">
      <div className="grid max-w-sm justify-items-center gap-3 text-center">
        <span className="flex size-11 items-center justify-center rounded-2xl border border-line-strong bg-surface-raised text-muted">{icon}</span>
        <p className="text-[15px] font-medium text-title">{title}</p>
        {text && <p className="text-[13px] leading-relaxed text-muted">{text}</p>}
        {onBrowser && (
          <Button size="sm" variant="secondary" onClick={onBrowser} className="mt-1">
            <ExternalLink />
            {t('pages.openInBrowser')}
          </Button>
        )}
      </div>
    </div>
  );
}
