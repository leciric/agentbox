// Watch, the prototype navigation's (lib/uiVerbs.ts) place for the agents
// and their machines. Its left column is a project's agents, v2's panel list
// (AgentList) with its monitors, under a project picker; the middle is the
// agent picked, on its machine: Desktop, Terminal, and Android for an Android
// project, the tabs Classic's agent page had (AgentView), with the machine's
// bar above them (AgentMachineBar). With no agent picked, the middle is every
// project's agents and the machine's load, v2 Home's Status tab
// (HomeStatusV2). Its chat is Ask's: "Open chat" goes there. Below lg an
// agent's machine takes the whole width, with a back arrow to the list.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, MessagesSquare, Monitor, Smartphone, SquareTerminal } from 'lucide-react';
import type * as T from '../../../shared/api';
import type { View, WatchPlace } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { cn } from '../../lib/utils';
import { AndroidTab } from '../AndroidTab';
import { BrowserTab } from '../BrowserTab';
import { ProjectIcon } from '../ProjectIcon';
import { TerminalTab } from '../TerminalTab';
import { Button } from '../ui/button';
import { Select, SelectOption } from '../ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../ui/tabs';
import { AgentList } from '../v2/AgentList';
import { AgentMachineBar } from '../v2/AgentMachineBar';
import { HomeStatusV2 } from '../v2/PagesV2';

export function WatchView({
  agentRef,
  place = 'browser',
  project: picked,
  onSelect,
  onAddProject,
  onNewAgent,
}: {
  agentRef?: string;
  place?: WatchPlace;
  project?: string;
  onSelect: (view: View) => void;
  onAddProject: () => void;
  onNewAgent: (project: string) => void;
}) {
  const t = useT();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agent = agents.data?.find((a) => a.ref === agentRef);
  // The list's project: the agent's, the one picked, else the first with
  // agents, else the first.
  const withAgents = (projects.data ?? []).filter((p) => agents.data?.some((a) => a.project === p.name));
  const project = agent?.project ?? picked ?? withAgents[0]?.name ?? projects.data?.[0]?.name ?? '';
  // What a page here opens: an agent stays in Watch, on its machine; the rest
  // goes where it always went.
  const watching = (next: View) => onSelect(next.kind === 'agent' ? { kind: 'watch', ref: next.ref } : next);

  return (
    <div className="flex h-full min-h-0">
      <aside className={cn('w-full shrink-0 flex-col border-r border-line bg-rail md:w-[300px]', agent ? 'hidden lg:flex' : 'flex')} data-watch-list>
        <div className="flex shrink-0 flex-col gap-2 px-3 pb-2 pt-4">
          <div>
            <h1 className="text-base font-semibold text-title">{t('verbs.watch')}</h1>
            <p className="text-caption text-muted">{t('verbs.watch.subtitle')}</p>
          </div>
          {(projects.data?.length ?? 0) > 0 && (
            <Select value={project} onChange={(p) => onSelect({ kind: 'watch', project: p })} aria-label={t('v2.filters.project')} className="w-full" data-watch-project>
              {(projects.data ?? []).map((p) => (
                <SelectOption key={p.name} value={p.name}>
                  <span className="flex min-w-0 items-center gap-2">
                    <ProjectIcon project={p} className="size-4 rounded text-caption" />
                    <span className="truncate">{projectLabel(p)}</span>
                    <span className="ml-auto shrink-0 tabular-nums text-faint">{agents.data?.filter((a) => a.project === p.name).length ?? 0}</span>
                  </span>
                </SelectOption>
              ))}
            </Select>
          )}
          <button
            type="button"
            onClick={() => onSelect({ kind: 'watch', project })}
            aria-current={!agent ? 'page' : undefined}
            className={cn('rounded-lg px-2 py-1.5 text-left text-ui transition-colors', !agent ? 'bg-surface-raised text-title' : 'text-muted hover:bg-surface hover:text-primary')}
            data-watch-all
          >
            {t('verbs.watch.all')}
          </button>
        </div>
        {project && <AgentList key={project} project={project} chat={agentRef ?? `${project}/lead`} onSelect={watching} onNewAgent={onNewAgent} />}
      </aside>
      <section className={cn('min-w-0 flex-1 flex-col', agent ? 'flex' : 'hidden md:flex')}>
        {agent ? (
          <Machine agent={agent} place={place} onSelect={onSelect} />
        ) : (
          <HomeStatusV2 onSelect={watching} onAddProject={onAddProject} onNewAgent={() => onNewAgent('')} />
        )}
      </section>
    </div>
  );
}

function Machine({ agent, place, onSelect }: { agent: T.Agent; place: WatchPlace; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const android = useQuery({ queryKey: ['android', agent.ref], queryFn: () => api.android(agent.ref), enabled: agent.state === 'running' });
  // As the agent's page decides it: Android projects, or an emulator started anyway.
  const hasAndroid = projects.data?.some((p) => p.name === agent.project && p.android) || android.data?.running === true;
  const start = useMutation({
    mutationFn: () => api.agentAction(agent.ref, agent.state === 'paused' ? 'resume' : 'start'),
    onSuccess: (updated) => queryClient.setQueryData<T.Agent[]>(['agents'], (list) => list?.map((a) => (a.ref === updated.ref ? updated : a))),
  });
  const shown: WatchPlace = place === 'android' && !hasAndroid ? 'browser' : place;
  const go = (p: string) => onSelect({ kind: 'watch', ref: agent.ref, place: p as WatchPlace });
  return (
    <Tabs value={shown} onValueChange={go} className="flex min-h-0 flex-1 flex-col" data-watch-machine={agent.ref}>
      <div className="flex shrink-0 flex-wrap items-center gap-2 px-4 pb-2 pt-3 md:px-6">
        <Button size="icon-sm" variant="ghost" className="lg:hidden" aria-label={t('verbs.watch.all')} onClick={() => onSelect({ kind: 'watch', project: agent.project })}>
          <ArrowLeft />
        </Button>
        <div className="min-w-0 flex-1">
          <h1 className={cn('truncate text-base font-semibold text-title', !agent.title && 'font-mono')}>{agent.title || agent.name}</h1>
          <p className="truncate text-caption text-muted">
            {projectLabel(agent.project, projects.data)} · {agent.name}
          </p>
        </div>
        <TabsList>
          <TabsTrigger value="browser" data-watch-place="browser">
            <Monitor />
            {t('agent.tab.desktop')}
          </TabsTrigger>
          <TabsTrigger value="terminal" data-watch-place="terminal">
            <SquareTerminal />
            {t('agent.tab.terminal')}
          </TabsTrigger>
          {hasAndroid && (
            <TabsTrigger value="android" data-watch-place="android">
              <Smartphone />
              {t('agent.tab.android')}
            </TabsTrigger>
          )}
        </TabsList>
        <Button size="sm" variant="secondary" onClick={() => onSelect({ kind: 'agent', ref: agent.ref, tab: 'chat' })} data-watch-chat>
          <MessagesSquare />
          {t('verbs.watch.openChat')}
        </Button>
      </div>
      <AgentMachineBar agent={agent} />
      <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line bg-sunken md:mx-6 md:mb-6">
        <TabsContent value="browser" className="flex flex-col">
          <BrowserTab agent={agent} onOpenMedia={() => onSelect({ kind: 'review', tab: 'proof' })} />
        </TabsContent>
        <TabsContent value="terminal" className="flex flex-col">
          <TerminalTab agent={agent} starting={start.isPending} onStart={() => start.mutate()} />
        </TabsContent>
        {hasAndroid && (
          <TabsContent value="android" className="flex flex-col">
            <AndroidTab agent={agent} onOpenMedia={() => onSelect({ kind: 'review', tab: 'proof' })} onSetup={() => onSelect({ kind: 'settings' })} />
          </TabsContent>
        )}
      </div>
    </Tabs>
  );
}
