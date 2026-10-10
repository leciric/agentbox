import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { arrivals, onScreenChats, openPage, showsArrival } from '../../lib/pages';
import { useT } from '../../lib/i18n';
import { t as tNow } from '../../../shared/i18n/index.ts';
import { projectLabel } from '../../lib/projectName';
import { AgentName, PageThumb } from './PageThumb';

// PageArrivals tells you about a page an agent just published or updated
// when no chat whose stack would show it is on screen: a toast with its
// thumbnail, which opens it, and an OS notification too while the window
// isn't in front (the bridge decides). Pages read for the first time, when
// the app starts or a project is first opened, are not news.
export function PageArrivals({ onNotified }: { onNotified: (id: string, open: () => void) => void }) {
  const queryClient = useQueryClient();
  const before = useRef(new Map<string, T.Artifacts>());
  const notified = useRef(onNotified);
  notified.current = onNotified;

  useEffect(() => {
    // What's read already is what later reads are compared with.
    for (const query of queryClient.getQueryCache().findAll({ queryKey: ['pages'] })) {
      const data = query.state.data as T.Artifacts | undefined;
      if (data) before.current.set(String(query.queryKey[1]), data);
    }
    return queryClient.getQueryCache().subscribe((event) => {
      if (event.type !== 'updated' || event.action.type !== 'success' || event.query.queryKey[0] !== 'pages') return;
      const project = String(event.query.queryKey[1]);
      const next = event.query.state.data as T.Artifacts | undefined;
      const prev = before.current.get(project);
      if (next) before.current.set(project, next);
      const seenOnScreen = onScreenChats();
      for (const page of arrivals(prev, next)) {
        if (document.hasFocus() && !showsArrival(page, seenOnScreen)) continue;
        announce(queryClient, project, page, notified.current);
      }
    });
  }, [queryClient]);
  return null;
}

function announce(queryClient: ReturnType<typeof useQueryClient>, project: string, page: T.Artifact, onNotified: (id: string, open: () => void) => void) {
  const id = `page:${page.id}@${page.version}`;
  const projects = queryClient.getQueryData<T.Project[]>(['projects']);
  const agents = queryClient.getQueryData<T.Agent[]>(['agents']);
  const agent = agents?.find((a) => a.ref === page.agent);
  const who = page.agent.endsWith('/lead') ? tNow('chat.project.agentTitle') : agent?.title || page.agent.split('/')[1];
  const open = () => {
    toast.dismiss(id);
    openPage(project, page);
  };
  toast.custom(() => <PageToast project={project} projectName={projectLabel(project, projects)} page={page} onOpen={open} />, { id, duration: 10_000 });
  onNotified(id, open);
  void window.agentbox.notify({
    id,
    title: tNow(page.version > 1 ? 'pages.toast.updated' : 'pages.toast.published', { agent: who }),
    body: [page.title, projectLabel(project, projects)].join('\n'),
  });
}

// PageToast is a page's arrival, the whole toast the way to it.
function PageToast({ project, projectName, page, onOpen }: { project: string; projectName: string; page: T.Artifact; onOpen: () => void }) {
  const t = useT();
  return (
    <button
      onClick={onOpen}
      data-toast-page={page.id}
      className="flex w-[356px] max-w-[calc(100vw-2rem)] items-center gap-3 rounded-xl border border-line-strong bg-overlay p-2.5 text-left shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur transition hover:border-line-vivid"
    >
      <PageThumb project={project} page={page} eager className="w-[88px] shrink-0 animate-page-drop rounded-lg ring-1 ring-black/10" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[11.5px] text-muted">
          {t(page.version > 1 ? 'pages.toast.updatedShort' : 'pages.toast.publishedShort')} · <AgentName agentRef={page.agent} />
        </span>
        <span className="mt-0.5 block truncate text-[13px] font-medium text-title">{page.title}</span>
        <span className="mt-0.5 block truncate text-[11px] text-subtle">{projectName}</span>
      </span>
      <span className="shrink-0 self-center pr-1 text-[11.5px] font-medium text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">{t('pages.toast.open')}</span>
    </button>
  );
}
