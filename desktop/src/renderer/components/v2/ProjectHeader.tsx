// v2's project header: a slim breadcrumb in the top bar (the project's icon
// and name, then Lead or the agent), and the project's ⋯ menu beside the
// chat's controls. The view puts those in the top bar through InTopBar.
import { useQuery } from '@tanstack/react-query';
import { ArrowLeftRight, Check, ChevronRight, Copy, Ellipsis, FolderOpen, Pin, PinOff, Plus, Settings, Smile, SquarePen, type LucideIcon } from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { requestComposerFocus } from '../../lib/newTask';
import { projectLabel } from '../../lib/projectName';
import { togglePin } from '../../lib/projectOrder';
import { openPanelOn, setPanel } from '../../lib/rightPanel';
import { setPins, useV2Projects } from '../../lib/v2Projects';
import { ProjectIcon } from '../ProjectIcon';
import { Button } from '../ui/button';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuMore, MenuSeparator, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { TalkToProject } from './TalkToProject';

const topSlotId = 'top-bar-slot';

// TopSlot is where a view puts its own controls in the top bar.
export function TopSlot() {
  return <div id={topSlotId} className="flex min-w-0 items-center gap-1" />;
}

export function InTopBar({ children }: { children: ReactNode }) {
  const [el, setEl] = useState<HTMLElement | null>(null);
  useEffect(() => setEl(document.getElementById(topSlotId)), []);
  return el ? createPortal(children, el) : null;
}

// ProjectCrumb is "Project › Lead" (or the agent): the project's root is in
// a tooltip and copied on click.
export function ProjectCrumb({ project, then }: { project: T.Project; then: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(project.root);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <span className="group flex min-w-0 items-center gap-1.5">
      <Tip label={project.root}>
        <button type="button" onClick={copy} className="-mx-1 flex min-w-0 items-center gap-1.5 rounded-md px-1 py-0.5 text-primary hover:bg-surface-raised" data-project-crumb>
          <ProjectIcon project={project} className="size-5 shrink-0 rounded-md text-[11px]" />
          <span className="max-w-[16rem] truncate font-medium">{projectLabel(project)}</span>
        </button>
      </Tip>
      <Tip label={t('v2.header.copyPath')}>
        <button type="button" aria-label={t('v2.header.copyPath')} onClick={copy} className="shrink-0 rounded p-0.5 text-faint opacity-0 transition hover:text-primary focus-visible:opacity-100 group-hover:opacity-100">
          {copied ? <Check className="size-3 text-emerald-400" /> : <Copy className="size-3" />}
        </button>
      </Tip>
      <ChevronRight className="size-3.5 shrink-0 text-faint" />
      <span className={`truncate ${then === T.LeadName ? 'text-muted' : 'font-medium text-primary'}`}>{then === T.LeadName ? t('v2.header.lead') : then}</span>
    </span>
  );
}

// ProjectAction is one item of a project's menu, the same in its ⋯ and in its
// sidebar row's right-click menu: projectActions lists them once for both.
export type ProjectAction = { key: string; icon: LucideIcon; label: string; onSelect: () => void; separatorBefore?: boolean };

// projectActions is a project's menu: New task (its lead's chat, to write the
// task in), Talk to another project, pinning and its settings in view, and
// under More what's rarely needed: its icon, its folder, and starting an
// agent yourself rather than through the lead. "New agent" went: v2 says New
// task for giving a project work, wherever it's offered.
export function projectActions(
  t: ReturnType<typeof useT>,
  { project, others, pinned, onNewTask, onTalk, onPin, onSettings, onChangeIcon, onNewAgent }: { project: T.Project; others: boolean; pinned: boolean; onNewTask: () => void; onTalk: () => void; onPin: () => void; onSettings: () => void; onChangeIcon: () => void; onNewAgent: () => void },
): { main: ProjectAction[]; more: ProjectAction[] } {
  return {
    main: [
      { key: 'new-task', icon: SquarePen, label: t('v2.sidebar.newTask'), onSelect: onNewTask },
      ...(others ? [{ key: 'talk', icon: ArrowLeftRight, label: t('v2.talk.button'), onSelect: onTalk }] : []),
      { key: 'pin', icon: pinned ? PinOff : Pin, label: pinned ? t('v2.sidebar.unpin') : t('v2.sidebar.pin'), onSelect: onPin, separatorBefore: true },
      { key: 'settings', icon: Settings, label: t('v2.sidebar.projectSettings'), onSelect: onSettings },
    ],
    more: [
      { key: 'icon', icon: Smile, label: t('projectIcon.change'), onSelect: onChangeIcon },
      { key: 'folder', icon: FolderOpen, label: t('project.view.openFolder'), onSelect: () => void window.agentbox.openPath(project.root) },
      { key: 'new-agent', icon: Plus, label: t('v2.menu.agentYourself'), onSelect: onNewAgent },
    ],
  };
}

// ProjectMenu is the project's ⋯ in the top bar: projectActions, and under
// More the lead chat's own rarely-used item (Reload tools).
export function ProjectMenu({ project, leadRef, onNewAgent, onChangeIcon, more: chatMore }: { project: T.Project; leadRef: string; onNewAgent: () => void; onChangeIcon: () => void; more?: ReactNode }) {
  const t = useT();
  const [talk, setTalk] = useState(false);
  const { pins } = useV2Projects();
  const others = (useQuery({ queryKey: ['projects'], queryFn: api.projects }).data?.length ?? 0) > 1;
  const pinned = pins.includes(project.name);
  const { main, more } = projectActions(t, {
    project,
    others,
    pinned,
    onNewTask: () => requestComposerFocus(leadRef),
    onTalk: () => setTimeout(() => setTalk(true), 0),
    onPin: () => setPins(togglePin(pins, project.name)),
    onSettings: () => {
      setPanel({ section: 'general' });
      openPanelOn(leadRef, 'settings');
    },
    onChangeIcon,
    onNewAgent,
  });
  const item = (a: ProjectAction) => [
    a.separatorBefore && <MenuSeparator key={`${a.key}-sep`} />,
    <MenuItem key={a.key} icon={a.icon} onSelect={a.onSelect} data-project-action={a.key}>
      {a.label}
    </MenuItem>,
  ];
  return (
    <TalkToProject
      project={project.name}
      leadRef={leadRef}
      open={talk}
      onOpenChange={setTalk}
      anchor={
        <span className="inline-flex">
          <Menu>
            <Tip label={t('project.view.actions')}>
              <MenuTrigger asChild>
                <Button size="icon" variant="ghost" aria-label={t('project.view.actions')} data-project-menu>
                  <Ellipsis />
                </Button>
              </MenuTrigger>
            </Tip>
            <MenuContent>
              <MenuLabel>{projectLabel(project)}</MenuLabel>
              {main.flatMap(item)}
              <MenuMore label={t('common.more')}>
                {more.flatMap(item)}
                {chatMore}
              </MenuMore>
            </MenuContent>
          </Menu>
        </span>
      }
    />
  );
}
