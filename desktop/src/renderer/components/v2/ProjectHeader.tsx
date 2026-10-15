// v2's project header: a slim breadcrumb in the top bar (the project's icon
// and name, then Lead or the agent), and the project's ⋯ menu beside the
// chat's controls. The view puts those in the top bar through InTopBar.
import { ArrowLeftRight, Check, ChevronRight, Copy, Ellipsis, FolderOpen, Pin, PinOff, Plus, Settings, Smile } from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { togglePin } from '../../lib/projectOrder';
import { openPanelOn, setPanel } from '../../lib/rightPanel';
import { setPins, useV2Projects } from '../../lib/v2Projects';
import { ProjectIcon } from '../ProjectIcon';
import { Button } from '../ui/button';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '../ui/menu';
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
      <button type="button" aria-label={t('v2.header.copyPath')} onClick={copy} className="shrink-0 rounded p-0.5 text-faint opacity-0 transition hover:text-primary focus-visible:opacity-100 group-hover:opacity-100">
        {copied ? <Check className="size-3 text-emerald-400" /> : <Copy className="size-3" />}
      </button>
      <ChevronRight className="size-3.5 shrink-0 text-faint" />
      <span className={`truncate ${then === T.LeadName ? 'text-muted' : 'font-medium text-primary'}`}>{then === T.LeadName ? t('v2.header.lead') : then}</span>
    </span>
  );
}

// ProjectMenu is the project's ⋯: what the header block's buttons did, and
// pinning.
export function ProjectMenu({ project, leadRef, onNewAgent, onChangeIcon }: { project: T.Project; leadRef: string; onNewAgent: () => void; onChangeIcon: () => void }) {
  const t = useT();
  const [talk, setTalk] = useState(false);
  const { pins } = useV2Projects();
  const pinned = pins.includes(project.name);
  return (
    <TalkToProject
      project={project.name}
      leadRef={leadRef}
      open={talk}
      onOpenChange={setTalk}
      anchor={
        <span className="inline-flex">
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon" variant="ghost" aria-label={t('project.view.actions')} data-project-menu>
                <Ellipsis />
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuLabel>{projectLabel(project)}</MenuLabel>
              <MenuItem icon={Plus} onSelect={onNewAgent}>
                {t('project.view.newAgent')}
              </MenuItem>
              <MenuItem icon={ArrowLeftRight} onSelect={() => setTimeout(() => setTalk(true), 0)}>
                {t('v2.talk.button')}
              </MenuItem>
              <MenuSeparator />
              <MenuItem icon={Smile} onSelect={onChangeIcon}>
                {t('projectIcon.change')}
              </MenuItem>
              <MenuItem icon={FolderOpen} onSelect={() => void window.agentbox.openPath(project.root)}>
                {t('project.view.openFolder')}
              </MenuItem>
              <MenuItem
                icon={Settings}
                onSelect={() => {
                  setPanel({ section: 'general' });
                  openPanelOn(leadRef, 'settings');
                }}
              >
                {t('v2.header.settings')}
              </MenuItem>
              <MenuItem icon={pinned ? PinOff : Pin} onSelect={() => setPins(togglePin(pins, project.name))}>
                {pinned ? t('v2.sidebar.unpin') : t('v2.sidebar.pin')}
              </MenuItem>
            </MenuContent>
          </Menu>
        </span>
      }
    />
  );
}
