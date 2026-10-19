// v2's project header: a slim breadcrumb in the top bar (the project's icon
// and name, then Lead or the agent), and the project's ⋯ menu beside the
// chat's controls. The view puts those in the top bar through InTopBar.
import { useQuery } from '@tanstack/react-query';
import { ArrowLeftRight, Check, CheckCheck, ChevronRight, Clapperboard, Copy, Ellipsis, FolderOpen, GitPullRequest, Pin, PinOff, Plus, Settings, Smile, Split, SquarePen, type LucideIcon } from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { togglePin } from '../../lib/projectOrder';
import { fitsBeside, openPanelOn, panelBack, setPanel, useChatPanel } from '../../lib/rightPanel';
import { setPins, useV2Projects } from '../../lib/v2Projects';
import { ProjectIcon } from '../ProjectIcon';
import { Button } from '../ui/button';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuMore, MenuSeparator, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { useOpenReview, useProjectReview } from './ReviewV2';
import { cn } from '../../lib/utils';
import { TalkToProject } from './TalkToProject';
import { openFanOut } from '../../lib/groups';

const topSlotId = 'top-bar-slot';
const topEndSlotId = 'top-bar-end-slot';

// TopSlot is where a view puts its own controls in the top bar, and
// TopEndSlot, at its far right, where its ⋯ and Panel go.
export function TopSlot() {
  return <div id={topSlotId} className="flex min-w-0 items-center gap-1" />;
}
export function TopEndSlot() {
  return <div id={topEndSlotId} className="flex shrink-0 items-center gap-1 empty:hidden" />;
}

export function InTopBar({ end, children }: { end?: boolean; children: ReactNode }) {
  const [el, setEl] = useState<HTMLElement | null>(null);
  useEffect(() => setEl(document.getElementById(end ? topEndSlotId : topSlotId)), [end]);
  return el ? createPortal(children, el) : null;
}

// ProjectCrumb is "Project › Lead" (or the agent): the project's root is in
// a tooltip and copied on click. With onProject (an agent's chat), a click on
// the project opens its lead's chat instead, and the copy icon copies.
export function ProjectCrumb({ project, then, onProject }: { project: T.Project; then: string; onProject?: () => void }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(project.root);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <span className="group flex min-w-0 items-center gap-1.5">
      <Tip label={onProject ? t('v2.header.openLead') : project.root}>
        <button type="button" onClick={onProject ?? copy} className="-mx-1 flex min-w-0 items-center gap-1.5 rounded-md px-1 py-0.5 text-primary hover:bg-surface-raised" data-project-crumb={onProject ? 'lead' : 'copy'}>
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
// task in), Talk to another project, pinning and, on a right-click of its
// sidebar row, its settings in view (the top bar has them as a button), and
// under More what's rarely needed: its icon, its folder, and starting an
// agent yourself rather than through the lead. "New agent" went: v2 says New
// task for giving a project work, wherever it's offered.
export function projectActions(
  t: ReturnType<typeof useT>,
  { project, others, pinned, onNewTask, onTalk, onToday, onPin, onSettings, onChangeIcon, onNewAgent }: { project: T.Project; others: boolean; pinned: boolean; onNewTask: () => void; onTalk: () => void; onToday?: () => void; onPin: () => void; onSettings?: () => void; onChangeIcon: () => void; onNewAgent: () => void },
): { main: ProjectAction[]; more: ProjectAction[] } {
  return {
    main: [
      { key: 'new-task', icon: SquarePen, label: t('v2.sidebar.newTask'), onSelect: onNewTask },
      { key: 'fan-out', icon: Split, label: t('group.menu.fanOut'), onSelect: () => openFanOut({ project: project.name }) },
      ...(others ? [{ key: 'talk', icon: ArrowLeftRight, label: t('v2.talk.button'), onSelect: onTalk }] : []),
      ...(onToday ? [{ key: 'today', icon: Clapperboard, label: t('today.title'), onSelect: onToday }] : []),
      { key: 'pin', icon: pinned ? PinOff : Pin, label: pinned ? t('v2.sidebar.unpin') : t('v2.sidebar.pin'), onSelect: onPin, separatorBefore: true },
      ...(onSettings ? [{ key: 'settings', icon: Settings, label: t('v2.sidebar.projectSettings'), onSelect: onSettings }] : []),
    ],
    more: [
      { key: 'icon', icon: Smile, label: t('projectIcon.change'), onSelect: onChangeIcon },
      { key: 'folder', icon: FolderOpen, label: t('project.view.openFolder'), onSelect: () => void window.agentbox.openPath(project.root) },
      { key: 'new-agent', icon: Plus, label: t('v2.menu.agentYourself'), onSelect: onNewAgent },
    ],
  };
}

// ProjectPlaces is the project's Pull requests, with how many are open and
// ready to review, and its Settings: one click from the lead's chat, in the
// right-hand panel, over whatever it shows. Clicked again, the panel goes
// back to what it showed.
export function ProjectPlaces({ project, leadRef }: { project: string; leadRef: string }) {
  const t = useT();
  const panel = useChatPanel(leadRef);
  const pulls = useQuery({ queryKey: ['pulls', project], queryFn: () => api.projectPullRequests(project), refetchInterval: 60_000 });
  const open = (pulls.data?.pullRequests ?? []).filter((pr) => pr.state === 'open').length;
  const ready = useProjectReview(project, !!useOpenReview()).data?.items?.length ?? 0;
  const on = (view: 'pulls' | 'settings') => (fitsBeside() || panel.open) && panel.wide === view;
  const toggle = (view: 'pulls' | 'settings') => {
    if (on(view)) return panelBack(leadRef);
    if (view === 'settings') setPanel({ section: 'general' });
    openPanelOn(leadRef, view);
  };
  const pullsTip = [t('panel.view.pulls'), open > 0 && t('v2.header.openPulls', { count: open }), ready > 0 && t('v2.header.readyPulls', { count: ready })].filter(Boolean).join(' · ');
  return (
    <>
      <Tip label={pullsTip}>
        <Button size="sm" variant="ghost" aria-label={pullsTip} aria-pressed={on('pulls')} className={cn('h-8 gap-1.5 px-2', on('pulls') && 'bg-surface-raised text-primary')} onClick={() => toggle('pulls')} data-project-pulls={open}>
          <GitPullRequest />
          <span className="hidden text-[12px] lg:inline">{t('panel.view.pulls')}</span>
          {open > 0 && <span className="rounded-full bg-surface-strong px-1.5 text-[10.5px] tabular-nums text-secondary">{open}</span>}
          {ready > 0 && (
            <span className="rounded-full bg-brand-500/15 px-1.5 text-[10.5px] font-medium tabular-nums text-brand-300 [:root[data-appearance=light]_&]:text-brand-600" data-project-pulls-ready={ready}>
              {t('v2.header.ready', { count: ready })}
            </span>
          )}
        </Button>
      </Tip>
      <Tip label={t('v2.sidebar.projectSettings')}>
        <Button size="icon-sm" variant="ghost" aria-label={t('v2.sidebar.projectSettings')} aria-pressed={on('settings')} className={cn(on('settings') && 'bg-surface-raised text-primary')} onClick={() => toggle('settings')} data-project-settings>
          <Settings />
        </Button>
      </Tip>
    </>
  );
}

// ProjectMenu is the project's ⋯ in the top bar: the lead chat's New chat,
// then projectActions but New task (you're in the chat it would open, its
// composer right there), and under More the project's reviewed agents (the
// review modal's Reviewed tab, there even when nothing waits for review) and
// the chat's rarely-used item (Reload tools).
export function ProjectMenu({ project, leadRef, onNewAgent, onChangeIcon, newChat, more: chatMore }: { project: T.Project; leadRef: string; onNewAgent: () => void; onChangeIcon: () => void; newChat?: ReactNode; more?: ReactNode }) {
  const t = useT();
  const [talk, setTalk] = useState(false);
  // Talk to another project, picked: the menu, closing, mustn't take the
  // focus back to the ⋯, which would be outside the picker opening under it
  // and so close it again (from the keyboard, it did every time).
  const talking = useRef(false);
  const openReview = useOpenReview();
  const { pins } = useV2Projects();
  const others = (useQuery({ queryKey: ['projects'], queryFn: api.projects }).data?.length ?? 0) > 1;
  const pinned = pins.includes(project.name);
  const { main, more } = projectActions(t, {
    project,
    others,
    pinned,
    onNewTask: () => {},
    onTalk: () => {
      talking.current = true;
      setTimeout(() => setTalk(true), 0);
    },
    onPin: () => setPins(togglePin(pins, project.name)),
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
            <Tip label={t('v2.header.chatOptions')}>
              <MenuTrigger asChild>
                <Button size="icon-sm" variant="ghost" aria-label={t('v2.header.chatOptions')} data-project-menu>
                  <Ellipsis />
                </Button>
              </MenuTrigger>
            </Tip>
            <MenuContent
              onCloseAutoFocus={(e) => {
                if (!talking.current) return;
                talking.current = false;
                e.preventDefault();
              }}
            >
              <MenuLabel>{projectLabel(project)}</MenuLabel>
              {newChat}
              {main.filter((a) => a.key !== 'new-task').flatMap(item)}
              <MenuMore label={t('common.more')}>
                {more.flatMap(item)}
                {openReview && (
                  <MenuItem icon={CheckCheck} onSelect={() => openReview({ project: project.name, reviewed: true })} data-project-action="reviewed">
                    {t('v2.menu.reviewed')}
                  </MenuItem>
                )}
                {chatMore}
              </MenuMore>
            </MenuContent>
          </Menu>
        </span>
      }
    />
  );
}
