import * as MenuPrimitive from '@radix-ui/react-dropdown-menu';
import { useQuery } from '@tanstack/react-query';
import { ChevronsUpDown, MessagesSquare, Plus, Users } from 'lucide-react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { cn, humanBytes } from '../lib/utils';
import { AIIcon, aiLabel, StatusDot } from './state';
import { Menu, MenuContent, MenuLabel, MenuSeparator, MenuTrigger } from './ui/menu';

const itemClass = (active: boolean) =>
  cn(
    'flex w-full cursor-default select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] outline-none data-[highlighted]:bg-surface-raised',
    active ? 'text-title' : 'text-secondary',
  );

// AgentSwitcher is how you move between a project's lead chat and its agents:
// a compact control in the TopBar, so it's there no matter which tab of an
// agent you're looking at (unlike something docked inside AgentView). Its
// first entry is always the project's chat, which doubles as the one-click
// way back to it from anywhere.
export function AgentSwitcher({ view, onSelect, onNewAgent }: { view: View; onSelect: (view: View) => void; onNewAgent: (project: string) => void }) {
  const project = view.kind === 'agent' ? view.ref.split('/')[0] : view.kind === 'project' ? view.project : null;
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled: project !== null });
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage, enabled: project !== null });

  if (!project) return null;
  const mine = agents.data?.filter((agent) => agent.project === project) ?? [];
  const onLead = view.kind === 'project';

  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          className="flex shrink-0 items-center gap-1.5 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs text-muted transition hover:bg-surface-raised hover:text-primary"
          aria-label={`Switch chat or agent in ${project}`}
        >
          <Users className="size-3.5" />
          <span className="hidden tabular-nums sm:inline">{mine.length}</span>
          <ChevronsUpDown className="size-3" />
        </button>
      </MenuTrigger>
      <MenuContent className="w-72 p-1.5" align="start">
        <MenuLabel>{project}</MenuLabel>
        <MenuPrimitive.Item className={itemClass(onLead)} onSelect={() => onSelect({ kind: 'project', project })}>
          <MessagesSquare className="size-4 shrink-0 text-brand-300" />
          <span className="min-w-0 flex-1 truncate">Project chat</span>
          {onLead && <span className="size-1.5 shrink-0 rounded-full bg-brand-400" />}
        </MenuPrimitive.Item>
        {mine.length > 0 && <MenuSeparator />}
        {mine.map((agent) => (
          <AgentRow
            key={agent.ref}
            agent={agent}
            active={view.kind === 'agent' && view.ref === agent.ref}
            sample={usage.data?.agents.find((u) => u.ref === agent.ref)}
            onSelect={() => onSelect({ kind: 'agent', ref: agent.ref })}
          />
        ))}
        <MenuSeparator />
        <MenuPrimitive.Item className={cn(itemClass(false), 'text-muted')} onSelect={() => onNewAgent(project)}>
          <Plus className="size-4 shrink-0" />
          <span className="flex-1">New agent</span>
        </MenuPrimitive.Item>
      </MenuContent>
    </Menu>
  );
}

function AgentRow({ agent, active, sample, onSelect }: { agent: T.Agent; active: boolean; sample?: T.AgentUsage; onSelect: () => void }) {
  return (
    <MenuPrimitive.Item data-agent={agent.ref} className={itemClass(active)} onSelect={onSelect}>
      <StatusDot state={agent.state} />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate font-medium">{agent.title || agent.name}</span>
          {agent.chat === 'running' && <span className="shrink-0 text-[10.5px] font-medium text-sky-300/90">Working</span>}
          {agent.chat === 'waiting' && <span className="shrink-0 text-[10.5px] font-medium text-amber-300">Needs you</span>}
        </span>
        <span className="mt-0.5 flex items-center gap-1.5 text-[11px] text-subtle">
          <AIIcon ai={agent.ai} className="size-3 shrink-0" />
          <span className={cn('truncate', agent.title && 'font-mono')}>{agent.title ? agent.name : aiLabel(agent.ai)}</span>
          {sample && agent.state === 'running' && (
            <span className="ml-auto shrink-0 font-mono text-[10.5px] tabular-nums text-subtle">
              {sample.cpu.toFixed(0)}% · {humanBytes(sample.memory)}
            </span>
          )}
        </span>
      </span>
    </MenuPrimitive.Item>
  );
}
