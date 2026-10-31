// The command palette's commands (Ctrl+K, components/SearchModal.tsx): the
// actions, each a verb with a handler, and every setting, which opens
// Settings at it. Matching and ranking are lib/palette.ts's.
import { useQuery, type QueryClient } from '@tanstack/react-query';
import {
  BellOff,
  BellRing,
  Bot,
  Brain,
  Briefcase,
  Bug,
  Camera,
  CalendarClock,
  Circle,
  CircleStop,
  Coins,
  FolderGit2,
  FolderPlus,
  GitBranch,
  GitPullRequest,
  History,
  House,
  Images,
  Keyboard,
  KeyRound,
  Layers,
  ListTodo,
  MessageSquare,
  Monitor,
  NotebookPen,
  Pause,
  Play,
  Plug,
  Plus,
  Settings,
  SlidersHorizontal,
  Smartphone,
  Sparkles,
  Square,
  SquareTerminal,
  Stethoscope,
  Users,
  Wand2,
  type LucideIcon,
} from 'lucide-react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import type { View } from '../App';
import { lifecycleActions } from '../lib/agentActions';
import { api, isProjectChat } from '../lib/api';
import { openFanOut } from '../lib/groups';
import { t as tNow, useT, type MessageKey } from '../lib/i18n';
import { openNewTask } from '../lib/newTask';
import { setDnd, useDnd } from '../lib/notifyPrefs';
import type { Command } from '../lib/palette';
import { projectLabel } from '../lib/projectName';
import { requestReveal, type Reveal } from '../lib/reveal';
import { leadOf, openPanelOn } from '../lib/rightPanel';
import { catalog, sectionTitles } from '../lib/settingsCatalog';
import { setSheetOpen } from '../lib/shortcuts';
import type { AgentPlaceName } from '../lib/tabs';
import { startTour, welcomeTour } from '../lib/tours';
import { useUiV2 } from '../lib/uiV2';
import { errorMessage } from '../lib/utils';
import { phoneGroups } from './PhoneSettings';
import { projectSettingGroups } from './ProjectSettings';
import { openSettingsSection } from './SettingsPage';

// What an action is for: nothing, the agent or the project you're on, or one
// asked for in the palette when you're on none.
export type Needs = 'agent' | 'project';

export type PaletteAction = Command & {
  group: 'actions';
  icon: LucideIcon;
  needs?: Needs;
  // keys is its own shortcut, shown beside it ("Mod", ",").
  keys?: string[];
  // run does it, given the agent's ref or the project's name it needs.
  run: (target: string) => void | Promise<unknown>;
};

export type PaletteSetting = Command & {
  group: 'settings';
  icon: LucideIcon;
  section: string;
  setting: string;
};

export type PaletteCommand = PaletteAction | PaletteSetting;

// The app's own hands the palette works with: where you are, going
// somewhere, and the dialogs App keeps.
export type PaletteContext = {
  view: View;
  select: (view: View) => void;
  addProject: () => void;
  newAgent: (project: string) => void;
  whatsNew: () => void;
};

// currentAgent and currentProject are what an action needing one uses
// without asking: the agent whose page is open (not a lead, whose page is the
// project's chat), and the project it or the page is in.
export function currentAgent(view: View): string | undefined {
  return view.kind === 'agent' && !isProjectChat(view.ref) ? view.ref : undefined;
}
export function currentProject(view: View): string | undefined {
  if (view.kind === 'project' || view.kind === 'today') return view.project;
  if (view.kind === 'agent') return view.ref.split('/')[0];
  if (view.kind === 'pastAgents') return view.project ?? view.ref?.split('/')[0];
  return undefined;
}

// openSetting opens Settings at one setting, scrolled to it and flashed, its
// Advanced fold opened if it's in one; click presses the control in it.
export function openSetting(section: string, setting: string, extra: Omit<Reveal, 'setting'> = {}) {
  const css = (v: string) => v.replace(/["\\]/g, '\\$&');
  requestReveal({ selector: `[data-settings-section="${css(section)}"] [data-setting-id="${css(setting)}"]`, setting, ...extra });
  openSettingsSection(section);
}

// Something an action started in the background: said when done, or why not.
function report(work: Promise<unknown>, done: string) {
  void work.then(
    () => toast(done),
    (err) => toast.error(errorMessage(err)),
  );
}

const savedToast = (item: T.MediaItem) => toast(tNow('agent.media.saved', { name: item.name }));

// usePaletteCommands is every action that can run here, and every setting.
export function usePaletteCommands(ctx: PaletteContext, queryClient: QueryClient): PaletteCommand[] {
  const t = useT();
  const v2 = useUiV2();
  const dnd = useDnd();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  // Phones pair with this machine's own daemon, as Settings has it.
  const target = useQuery({ queryKey: ['target'], queryFn: () => window.agentbox.target.get(), staleTime: Infinity });
  const local = !('web' in window.agentbox) && target.data?.kind === 'local';
  const { select } = ctx;
  const here = currentAgent(ctx.view);
  const agentOf = (ref: string) => agents.data?.find((a) => a.ref === ref);
  const nameOf = (ref: string) => agentOf(ref)?.title || ref.split('/')[1] || ref;

  // An action's words are palette.<id>.title, .keywords and .about.
  const action = (id: string, icon: LucideIcon, run: PaletteAction['run'], more: Partial<PaletteAction> = {}): PaletteAction => ({
    id,
    group: 'actions',
    title: t(`palette.${id}.title` as MessageKey),
    keywords: t(`palette.${id}.keywords` as MessageKey),
    icon,
    run,
    ...more,
  });
  const project = (id: string, icon: LucideIcon, place: View | ((name: string) => void)) =>
    action(id, icon, (name) => (typeof place === 'function' ? place(name) : select({ ...place, project: name } as View)), { needs: 'project' });
  const agent = (id: string, icon: LucideIcon, run: (ref: string) => void) => action(id, icon, run, { needs: 'agent' });
  const agentTab = (tab: AgentPlaceName) => (ref: string) => select({ kind: 'agent', ref, tab });

  // An agent's lifecycle, offered as its state allows when it's the one
  // you're on; asked for, any agent.
  const states = here ? lifecycleActions(agentOf(here)?.state ?? '') : (['start', 'stop', 'pause', 'resume'] as const);
  const lifecycle = (
    [
      ['start', Play, 'palette.done.start'],
      ['stop', Square, 'palette.done.stop'],
      ['pause', Pause, 'palette.done.pause'],
      ['resume', Play, 'palette.done.resume'],
    ] as const
  )
    .filter(([name]) => states.includes(name))
    .map(([name, icon, done]) => agent(`agent-${name}`, icon, (ref) => report(api.agentAction(ref, name), t(done, { agent: nameOf(ref) }))));

  const actions: PaletteAction[] = [
    // Making things.
    action('new-agent', Plus, () => ctx.newAgent(currentProject(ctx.view) ?? '')),
    ...(v2 ? [action('new-task', ListTodo, () => openNewTask(), { keys: ['Mod', 'N'] })] : []),
    action('add-project', FolderPlus, () => ctx.addProject()),
    project('fan-out', Users, (name) => openFanOut({ project: name })),

    // An agent's machine.
    agent('record', Circle, (ref) => {
      select({ kind: 'agent', ref, tab: 'browser' });
      report(api.startRecording(ref, { name: 'screen recording' }), t('palette.done.record', { agent: nameOf(ref) }));
    }),
    agent('record-stop', CircleStop, (ref) => {
      void api.stopRecording(ref).then(
        (item) => {
          void queryClient.invalidateQueries({ queryKey: ['recording', ref] });
          savedToast(item);
        },
        (err) => toast.error(errorMessage(err)),
      );
    }),
    agent('screenshot', Camera, (ref) => {
      void api.screenshot(ref, { target: 'display', name: 'screen' }).then(savedToast, (err) => toast.error(errorMessage(err)));
    }),
    agent('android-start', Smartphone, (ref) => {
      select({ kind: 'agent', ref, tab: 'android' });
      // The request lasts until Android has booted; the tab shows it starting.
      setTimeout(() => void queryClient.invalidateQueries({ queryKey: ['android', ref] }), 1_500);
      void api.startAndroid(ref).then(
        (next) => queryClient.setQueryData(['android', ref], next),
        (err) => toast.error(errorMessage(err)),
      );
    }),
    agent('android-stop', Smartphone, (ref) => {
      void api.stopAndroid(ref).then(
        (next) => queryClient.setQueryData(['android', ref], next),
        (err) => toast.error(errorMessage(err)),
      );
    }),
    ...lifecycle,

    // An agent's places.
    agent('agent-chat', MessageSquare, agentTab('chat')),
    agent('agent-terminal', SquareTerminal, agentTab('terminal')),
    agent('agent-desktop', Monitor, (ref) => (v2 ? (select({ kind: 'agent', ref }), openPanelOn(ref, 'desktop', ref)) : select({ kind: 'agent', ref, tab: 'browser' }))),
    agent('agent-android', Smartphone, agentTab('android')),
    agent('agent-media', Images, agentTab('media')),
    agent('agent-snapshots', History, agentTab('snapshots')),
    agent('agent-settings', SlidersHorizontal, agentTab('settings')),
    agent('agent-secrets', KeyRound, agentTab('secrets')),

    // A project's places.
    project('project-chat', MessageSquare, { kind: 'project', project: '', tab: 'chat' }),
    project('project-pulls', GitPullRequest, { kind: 'project', project: '', tab: 'pulls' }),
    project('project-media', Images, { kind: 'project', project: '', tab: 'media' }),
    project('project-past', History, { kind: 'pastAgents', project: '' } as View),
    project('project-today', CalendarClock, { kind: 'today', project: '' }),
    project('project-settings', Settings, { kind: 'project', project: '', tab: 'settings' }),
    project('project-repository', GitBranch, { kind: 'project', project: '', tab: 'repository' }),
    project('project-notes', NotebookPen, { kind: 'project', project: '', tab: 'brief' }),
    project('project-memory', Brain, { kind: 'project', project: '', tab: 'memory' }),
    project('project-tokens', Coins, { kind: 'project', project: '', tab: 'tokens' }),
    project('project-secrets', KeyRound, { kind: 'project', project: '', tab: 'secrets' }),
    project('project-connectors', Plug, { kind: 'project', project: '', tab: 'connectors' }),
    project('project-skills', Wand2, { kind: 'project', project: '', tab: 'skills' }),
    ...(v2
      ? [
          project('schedule', CalendarClock, (name) => {
            select({ kind: 'project', project: name, tab: 'chat' });
            requestReveal({ selector: '[data-schedule-new]', click: true });
            openPanelOn(leadOf(name), 'schedules');
          }),
        ]
      : []),
    // Nesting is switched on where it lives, in the project's settings; one
    // already on is only shown, never switched off from here.
    project('nesting', Layers, (name) => {
      const on = projects.data?.find((p) => p.name === name)?.nesting;
      openSetting(`project:${name}`, 'nesting', { click: !on });
    }),

    // AgentBox's places.
    action('home', House, () => select({ kind: 'home' })),
    ...(v2 ? [] : [action('home-chat', MessageSquare, () => select({ kind: 'homeChat' }))]),
    action('jobs', Briefcase, () => select({ kind: 'jobs' })),
    action('all-media', Images, () => select({ kind: 'media' })),
    action('past-agents', Bot, () => select({ kind: 'pastAgents' })),
    action('settings', Settings, () => select({ kind: 'settings' }), { keys: ['Mod', ','] }),
    ...(v2 ? [action('shortcuts', Keyboard, () => setSheetOpen(true), { keys: ['Mod', '/'] })] : []),

    // AgentBox-wide things to do.
    action('add-connector', Plug, () => openSetting('connectors', 'connectors', { selector: '[data-settings-section="connectors"] [data-connectors-import]', click: true })),
    action('add-skill', Wand2, () => openSetting('skills', 'skills', { selector: '[data-settings-section="skills"] [data-skills-import]', click: true })),
    ...(local ? [action('connect-phone', Smartphone, () => openSetting('phone', 'phone-chat', { selector: '[data-phone-start]', click: true }))] : []),
    dnd.on
      ? action('dnd-off', BellRing, () => setDnd(null))
      : action('dnd-on', BellOff, () => {
          setDnd('hour');
          toast(t('palette.done.dnd'));
        }),
    action('report-problem', Bug, () => openSetting('general', 'report-problem', { selector: '[data-report-problem]', click: true })),
    action('diagnostics', Stethoscope, () => openSetting('diagnostics', 'diagnostics')),
    action('whats-new', Sparkles, () => ctx.whatsNew()),
    ...(v2 ? [action('tour', Sparkles, () => startTour(welcomeTour.id))] : []),
  ];

  // Every setting: the installation's, the phone's, and each project's.
  const sectionName = (id: string) => (sectionTitles[id] ? t(sectionTitles[id]) : id);
  const settings: PaletteSetting[] = [
    ...catalog
      .filter((s) => (!s.v2 || v2) && (s.section !== 'phone' || local))
      .map(
        (s): PaletteSetting => ({
          id: `setting:${s.section}:${s.id}`,
          group: 'settings',
          title: t(s.label),
          keywords: s.keywords ? t(s.keywords) : undefined,
          where: `${sectionName(s.section)} · ${t(s.group)}`,
          icon: SlidersHorizontal,
          section: s.section,
          setting: s.id,
        }),
      ),
    ...[
      ...(local ? phoneGroups().map((g) => ({ g, section: 'phone', where: `${t('settings.section.phone.title')} · ${g.title}`, icon: Smartphone })) : []),
      ...(projects.data ?? []).flatMap((p) =>
        projectSettingGroups(p).map((g) => ({ g, section: `project:${p.name}`, where: `${projectLabel(p.name, projects.data)} · ${g.title}`, icon: FolderGit2 })),
      ),
    ].flatMap(({ g, section, where, icon }) =>
      g.entries.map(
        (e): PaletteSetting => ({
          id: `setting:${section}:${e.id}`,
          group: 'settings',
          title: e.label,
          keywords: e.keywords,
          where,
          icon,
          section,
          setting: e.id,
        }),
      ),
    ),
  ];
  return [...actions, ...settings];
}

// runSetting opens Settings at a setting the palette found.
export function runSetting(s: PaletteSetting) {
  openSetting(s.section, s.setting);
}
