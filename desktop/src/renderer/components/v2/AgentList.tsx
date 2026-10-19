// UI v2's right-hand panel opens on its project's agents, as v1's rail had
// them beside the chat: the rail's own rows (AgentRail's AgentRow, with its
// status, last report and pull request), with what v2 knows besides, an
// agent using its desktop now and one ready to review. The repository, the
// lead's checkout, heads it; the finished agents fold under them as in the
// rail, and Past agents is at its foot.
//
// A row opens the agent in the panel, on its Desktop, as a tile of the lane
// of desktops does; the panel's back arrow comes back here.
import { useQuery } from '@tanstack/react-query';
import { Archive, ChevronRight } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { livePages, countByAgent } from '../../lib/pages';
import { pastAgentsKey } from '../../lib/pastAgents';
import { projectLabel } from '../../lib/projectName';
import { leadOf, openPanelOn, pickTarget } from '../../lib/rightPanel';
import { cn } from '../../lib/utils';
import { AgentRow, FinishedSection, RailEmpty, RailSkeleton, useRailAgents } from '../AgentRail';
import { useProjectPages } from '../pages/usePages';
import { useInUse } from '../pip/PipLane';
import { ProjectIcon } from '../ProjectIcon';
import { DeskGlyph } from './DesktopPeek';
import { useOpenReview, useProjectReview } from './ReviewV2';

export function AgentList({ project, chat, onSelect, onNewAgent }: { project: string; chat: string; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const t = useT();
  const rail = useRailAgents(project);
  const live = new Set(useInUse(project).map((a) => a.ref));
  const ready = new Set((useProjectReview(project, !!useOpenReview()).data?.items ?? []).map((i) => i.ref));
  const pageCounts = countByAgent(livePages(useProjectPages(project).data));
  const past = useQuery({ queryKey: [...pastAgentsKey, 'panel', project], queryFn: () => api.archivedAgents({ project, limit: 50 }), staleTime: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const p = projects.data?.find((x) => x.name === project);
  const name = projectLabel(project, projects.data);

  const row = (agent: T.Agent) => (
    <AgentRow
      key={agent.ref}
      agent={agent}
      active={chat === agent.ref}
      pr={rail.prs.get(agent.ref)}
      pages={pageCounts.get(agent.ref) ?? 0}
      onPages={() => openPanelOn(chat, 'evidence', agent.ref)}
      at={rail.lastReport.get(agent.ref)}
      asking={rail.asking(agent)}
      mood={rail.mood(agent)}
      onSelect={() => openPanelOn(chat, 'desktop', agent.ref)}
      onOpen={onSelect}
      marks={
        <>
          {live.has(agent.ref) && <DeskGlyph desk="live" />}
          {ready.has(agent.ref) && (
            <span className="shrink-0 rounded-full px-1.5 text-[10px] font-medium text-secondary ring-1 ring-inset ring-line-strong" data-list-ready>
              {t('v2.st.review')}
            </span>
          )}
        </>
      }
    />
  );

  return (
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 pb-3 pt-1.5" data-panel-list={project}>
      <button type="button" onClick={() => pickTarget(chat, leadOf(project))} className="group relative flex w-full min-w-0 items-center gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors hover:bg-surface" data-list-repository>
        {p ? <ProjectIcon project={p} className="size-10 rounded-xl text-[15px]" /> : <span className="size-10 shrink-0 rounded-xl bg-surface-strong" />}
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-medium text-secondary">{t('panel.repository')}</span>
          <span className="block truncate text-[11px] text-subtle">{t('panel.repositorySub', { project: name })}</span>
        </span>
        <ChevronRight className="size-3.5 shrink-0 text-faint opacity-0 transition group-hover:opacity-100" />
      </button>

      {(rail.moving.length > 0 || !rail.loaded) && <div className="my-1.5 border-t border-line-faint" />}
      {!rail.loaded && <RailSkeleton />}
      {rail.moving.map(row)}
      {rail.finished.length > 0 && <FinishedSection count={rail.finished.length} open={rail.showFinished || rail.finished.some((a) => a.ref === chat)} onToggle={rail.setShowFinished}>{rail.finished.map(row)}</FinishedSection>}
      {rail.loaded && rail.mine.length === 0 && <RailEmpty project={name} onNewAgent={() => onNewAgent(project)} />}

      {(past.data?.length ?? 0) > 0 && (
        <button
          type="button"
          onClick={() => onSelect({ kind: 'pastAgents', project })}
          className={cn('mt-1.5 flex w-full items-center gap-1.5 rounded-lg border-t border-line-faint px-2.5 pb-1.5 pt-2.5 text-left text-[11.5px] text-subtle transition hover:bg-surface hover:text-primary')}
          data-list-past
        >
          <Archive className="size-3.5 shrink-0" />
          {t('pastAgents.title')}
          <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums">{past.data!.length >= 50 ? '50+' : past.data!.length}</span>
        </button>
      )}
    </div>
  );
}
