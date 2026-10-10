import { X } from 'lucide-react';
import { useDeferredValue, useState } from 'react';
import type * as T from '../../../shared/api';
import { byAgent, matchesPage, openPage } from '../../lib/pages';
import { useT } from '../../lib/i18n';
import { useNow } from '../../lib/useNow';
import { AIIcon } from '../state';
import { PageRow, PageSearch } from './PageList';
import { useAgentLabel } from './PageThumb';

// PagesPanel is the right column's Pages tab: every page the project's chats
// published that Hatch still has, under the agent that published it, the
// agent with the newest page first, with a search. Clicking an agent's page
// icon in the Agents list opens it on that agent alone.
export function PagesPanel({ project, pages, agents, agent, onAgent }: { project: string; pages: T.Artifact[]; agents: T.Agent[]; agent: string; onAgent: (ref: string) => void }) {
  const t = useT();
  const now = useNow(60_000);
  const label = useAgentLabel();
  const [query, setQuery] = useState('');
  const search = useDeferredValue(query);
  const found = pages.filter((a) => (!agent || a.agent === agent) && matchesPage(a, search, label(a.agent)));
  const groups = byAgent(found);

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-rail-pages>
      <div className="grid gap-1.5 px-3 pb-2">
        <PageSearch value={query} onChange={setQuery} />
        {agent && (
          <span
            className="flex min-w-0 items-center gap-1 self-start rounded-full border border-line-strong bg-surface-raised py-0.5 pl-2 pr-1 text-[11.5px] text-secondary"
            data-rail-pages-agent={agent}
          >
            <span className="min-w-0 truncate">{label(agent)}</span>
            <button
              aria-label={t('pages.rail.allAgents')}
              onClick={() => onAgent('')}
              className="flex size-4 items-center justify-center rounded-full text-subtle hover:bg-surface-strong hover:text-primary"
            >
              <X className="size-3" />
            </button>
          </span>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 pb-3">
        {groups.map(([ref, list]) => {
          const a = agents.find((x) => x.ref === ref);
          return (
            <section key={ref} className="mb-2" data-rail-pages-group={ref}>
              <h3 className="sticky top-0 z-[1] flex items-center gap-2 bg-rail px-1.5 py-1.5 backdrop-blur-xl">
                <span className="flex size-5 shrink-0 items-center justify-center rounded-md border border-line-strong bg-surface-raised text-brand-200">
                  <AIIcon ai={a?.ai ?? 'claude'} className="size-3" />
                </span>
                <span className="min-w-0 truncate text-[12px] font-medium text-secondary">{label(ref)}</span>
                <span className="rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-subtle">{list.length}</span>
              </h3>
              {list.map((page) => (
                <PageRow key={page.id} project={project} page={page} showAgent={false} now={now} onOpen={() => openPage(project, page)} />
              ))}
            </section>
          );
        })}
        {groups.length === 0 && <p className="px-2 py-8 text-center text-[12.5px] text-subtle">{t('pages.noMatch')}</p>}
      </div>
    </div>
  );
}
