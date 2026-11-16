// Concept: grouping the lead's session tabs (components/chat/SessionTabs.tsx).
// Three static models to choose from, one page each, every state laid out as
// a labelled frame so a screenshot shows all of it at once:
//
//   ?concept=tabgroups&option=a   Named, coloured groups that fold into a chip
//   ?concept=tabgroups&option=b   One button per group, its tabs in a menu
//   ?concept=tabgroups&option=c   Two rows: the groups, then the chosen one's tabs
//   &theme=light                  the light appearance
//
// Each page shows: expanded, collapsed (with what a folded group says about
// what waits inside it), creating a group (the tab's menu, and dragging a tab
// onto another), renaming and recolouring, and a narrow window. Menus are
// drawn open and static, not Radix menus, so they stay put for a screenshot.
// Mock only: nothing here ships or is translated.
import {
  AlarmClock,
  ChevronDown,
  ChevronRight,
  ChevronsLeftRight,
  FolderPlus,
  History,
  Layers,
  LoaderCircle,
  Pencil,
  Pin,
  Plus,
  TriangleAlert,
  Ungroup,
  X,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../../lib/utils';

// ——— The fixture: 11 sessions, two pinned, three groups, one loose ———

type Color = 'rose' | 'sky' | 'mint' | 'lilac' | 'peach';
type Status = 'needs' | 'failed' | 'working' | 'unread' | 'idle';
const COLORS: Color[] = ['rose', 'sky', 'mint', 'lilac', 'peach'];

interface Session {
  id: string;
  topic: string;
  color: Color;
  status: Status;
  needs?: number;
  pinned?: boolean;
  agents: number;
  group?: string;
}
interface Group {
  id: string;
  name: string;
  color: Color;
}

const groups: Group[] = [
  { id: 'ui', name: 'Desktop UI', color: 'sky' },
  { id: 'win', name: 'Windows', color: 'peach' },
  { id: 'docs', name: 'Docs', color: 'mint' },
];

const sessions: Session[] = [
  { id: 'rel', topic: 'Release 0.17', color: 'rose', status: 'needs', needs: 2, pinned: true, agents: 1 },
  { id: 'inbox', topic: 'Inbox triage', color: 'lilac', status: 'idle', pinned: true, agents: 0 },
  { id: 'tabs', topic: 'Session tab groups', color: 'sky', status: 'working', agents: 2, group: 'ui' },
  { id: 'side', topic: 'Sidebar drag order', color: 'sky', status: 'unread', agents: 1, group: 'ui' },
  { id: 'light', topic: 'Light theme contrast pass', color: 'sky', status: 'idle', agents: 0, group: 'ui' },
  { id: 'wsl', topic: 'WSL install flow', color: 'peach', status: 'failed', agents: 1, group: 'win' },
  { id: 'nsis', topic: 'Native installer (NSIS)', color: 'peach', status: 'needs', needs: 1, agents: 1, group: 'win' },
  { id: 'pipe', topic: 'Named pipe relay', color: 'peach', status: 'working', agents: 3, group: 'win' },
  { id: 'agents', topic: 'AGENTS.md rewrite', color: 'mint', status: 'idle', agents: 0, group: 'docs' },
  { id: 'log', topic: 'Changelog cleanup', color: 'mint', status: 'unread', agents: 0, group: 'docs' },
  { id: 'oauth', topic: 'OAuth expiry asks to log in', color: 'lilac', status: 'idle', agents: 1 },
];

const CURRENT = 'tabs';
const by = (id: string) => sessions.find((s) => s.id === id)!;
const inGroup = (g: string) => sessions.filter((s) => s.group === g && !s.pinned);
const pinned = sessions.filter((s) => s.pinned);
const loose = sessions.filter((s) => !s.group && !s.pinned);
const gc = (c: Color) => `var(--ab-session-${c})`;
const tint = (c: Color, pct: number) => `color-mix(in oklab, ${gc(c)} ${pct}%, transparent)`;

// ——— Marks: one session's, and what a folded group adds up to ———

function Mark({ s, on }: { s: Session; on?: boolean }) {
  if (s.status === 'needs') return <Needs n={s.needs ?? 1} />;
  if (s.status === 'failed') return <TriangleAlert className="size-3 shrink-0 text-rose-300" />;
  if (s.status === 'working') return <LoaderCircle className="size-3 shrink-0 animate-spin text-brand-300" />;
  if (s.status === 'unread' && !on) return <span className="size-1.5 shrink-0 rounded-full bg-brand-400" />;
  return null;
}

function Needs({ n }: { n: number }) {
  return <span className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-amber-400 px-1 text-micro font-semibold tabular-nums leading-none text-black">{n}</span>;
}

// GroupMark is a folded group's sum: the amber count of everything waiting on
// you, a red warning if any stopped, then a spinner if any works, else a dot
// if any said something unseen. Waiting and stopped both show, since both are
// yours to act on; working and unread only when nothing more urgent does.
function GroupMark({ list }: { list: Session[] }) {
  const needs = list.reduce((n, s) => n + (s.status === 'needs' ? (s.needs ?? 1) : 0), 0);
  const failed = list.some((s) => s.status === 'failed');
  const working = list.some((s) => s.status === 'working' && s.id !== CURRENT);
  const unread = list.some((s) => s.status === 'unread');
  return (
    <>
      {needs > 0 && <Needs n={needs} />}
      {failed && <TriangleAlert className="size-3 shrink-0 text-rose-300" />}
      {!needs && !failed && working && <LoaderCircle className="size-3 shrink-0 animate-spin text-brand-300" />}
      {!needs && !failed && !working && unread && <span className="size-1.5 shrink-0 rounded-full bg-brand-400" />}
    </>
  );
}

// ——— A tab, as SessionTabs draws one today ———

function Tab({ s, compact, iconOnly, ring, ghost, color, pop, popEnd }: { s: Session; compact?: boolean; iconOnly?: boolean; ring?: boolean; ghost?: boolean; color?: Color; pop?: ReactNode; popEnd?: boolean }) {
  const on = s.id === CURRENT;
  return (
    <div className="group relative flex shrink-0" data-session-color={color ?? s.color}>
      <div
        className={cn(
          'mb-1.5 flex h-7 shrink-0 select-none items-center gap-1.5 rounded-md text-ui',
          iconOnly ? 'w-8 justify-center px-0' : cn('pl-2.5', compact ? 'max-w-32 pr-2.5' : 'max-w-56 pr-7'),
          on ? 'session-line bg-surface-strong text-primary' : 'session-line-quiet text-muted',
          ring && 'outline outline-2 outline-brand-400',
          ghost && 'opacity-50',
        )}
        title={s.topic}
      >
        <Mark s={s} on={on} />
        {s.pinned && (s.status === 'idle' || iconOnly) && !(iconOnly && s.status !== 'idle') && <Pin className="size-3 shrink-0 -rotate-45 text-subtle" />}
        {!iconOnly && <span className="min-w-0 truncate">{s.topic}</span>}
        {!iconOnly && !compact && s.agents > 0 && <span className="shrink-0 text-caption tabular-nums text-faint">{s.agents}</span>}
      </div>
      {on && !iconOnly && !compact && (
        <span className="absolute right-1.5 top-1 flex size-5 items-center justify-center rounded-md text-faint">
          <X className="size-3" />
        </span>
      )}
      {pop && <Pop className={popEnd ? 'left-auto right-0' : undefined}>{pop}</Pop>}
    </div>
  );
}

function RowEnd({ narrow }: { narrow?: boolean }) {
  return (
    <>
      <span className="min-w-2 flex-1" />
      {!narrow && (
        <span className="mb-0.5 flex h-7 shrink-0 items-center gap-1 px-2 text-ui text-muted">
          <AlarmClock className="size-3.5" />
          <span className="text-caption tabular-nums text-faint">1</span>
        </span>
      )}
      <span className="mb-0.5 flex h-7 shrink-0 items-center gap-1 px-2 text-ui text-muted">
        <History className="size-3.5" />
        {!narrow && <span className="text-caption tabular-nums text-faint">14</span>}
      </span>
    </>
  );
}

function NewTab() {
  return (
    <span className="mb-0.5 flex h-7 shrink-0 items-center px-2 text-muted">
      <Plus className="size-3.5" />
    </span>
  );
}

// ——— A static menu, styled as components/ui/context-menu ———

function Menu({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('relative z-20 min-w-52 rounded-xl border border-line-strong bg-overlay p-1 shadow-pop', className)}>{children}</div>;
}

// Pop hangs what it holds (a menu, a dragged tab) off the element it's in.
function Pop({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('absolute left-0 top-full z-20 mt-0.5', className)}>{children}</div>;
}

// Dragged is a tab in flight over a drop target, with what dropping does.
function Dragged({ topic, color, says }: { topic: string; color: Color; says: string }) {
  return (
    <>
      <div className="absolute left-5 top-full z-30 -mt-2 flex h-7 items-center whitespace-nowrap rounded-md bg-surface-strong px-2.5 text-ui text-primary shadow-pop" data-session-color={color}>
        {topic}
      </div>
      <Pop className="left-2 mt-8">
        <Callout>{says}</Callout>
      </Pop>
    </>
  );
}

function Item({ icon: Icon, children, hint, active, sub, dot }: { icon?: typeof Pin; children: ReactNode; hint?: ReactNode; active?: boolean; sub?: boolean; dot?: Color }) {
  return (
    <div className={cn('flex select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-ui text-secondary', active && 'bg-surface-raised')}>
      {Icon && <Icon className="size-4 text-subtle" />}
      {dot && <span className="size-2.5 shrink-0 rounded-full" style={{ background: gc(dot) }} />}
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {hint && <span className="text-caption text-subtle">{hint}</span>}
      {sub && <ChevronRight className="size-3.5 text-subtle" />}
    </div>
  );
}

// Sub is a menu item with its submenu open beside it.
function Sub({ item, children, left }: { item: ReactNode; children: ReactNode; left?: boolean }) {
  return (
    <div className="relative">
      {item}
      <div className={cn('absolute top-0', left ? 'right-full mr-2' : 'left-full ml-2')}>{children}</div>
    </div>
  );
}

function Sep() {
  return <div className="mx-1 my-1 h-px bg-line-faint" />;
}

function Swatches({ on }: { on: Color }) {
  return (
    <div className="flex items-center gap-2 px-2.5 py-2">
      {COLORS.map((c) => (
        <span key={c} className={cn('size-5 rounded-full', c === on && 'outline outline-2 outline-offset-2 outline-brand-400')} style={{ background: gc(c) }} />
      ))}
    </div>
  );
}

function NameField({ value }: { value: string }) {
  return (
    <div className="px-1.5 pt-1.5">
      <div className="flex h-8 items-center rounded-lg border border-brand-400 bg-sunken px-2.5 text-ui text-primary">
        {value}
        <span className="ml-px h-4 w-px animate-pulse bg-primary" />
      </div>
    </div>
  );
}

// Callout is the mock's own annotation, pointing at a part of the frame.
function Callout({ children }: { children: ReactNode }) {
  return (
    <div className="whitespace-nowrap rounded-md border border-dashed border-brand-400 bg-ink px-2 py-1 text-caption text-brand-200">
      {children}
    </div>
  );
}

// ——— A frame: one state of the row over a stub of the chat below ———

function Frame({ title, note, width = 1040, height = 200, children, chat = true }: { title: string; note?: ReactNode; width?: number; height?: number; children: ReactNode; chat?: boolean }) {
  return (
    <section className="flex flex-col gap-2">
      <div>
        <h3 className="text-ui font-semibold text-title">{title}</h3>
        {note && <p className="max-w-[1040px] text-caption text-subtle">{note}</p>}
      </div>
      <div className="relative overflow-hidden rounded-lg border border-line bg-ink" style={{ width, height }}>
        {children}
        {chat && (
          <div className="mx-5 flex flex-col gap-2 rounded-lg border border-line-faint bg-chat p-4" data-session-color="sky">
            <div className="h-2.5 w-2/3 rounded bg-surface" />
            <div className="h-2.5 w-1/2 rounded bg-surface" />
            <div className="h-2.5 w-3/5 rounded bg-surface-faint" />
          </div>
        )}
      </div>
    </section>
  );
}

function Row({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('relative z-10 flex min-w-0 shrink-0 items-end gap-1 px-5 pt-1', className)}>{children}</div>;
}

// ——— Option A: Chrome-style groups, a coloured chip that folds ———

function Chip({ g, folded, narrow, drop, pop }: { g: Group; folded?: boolean; narrow?: boolean; drop?: boolean; pop?: ReactNode }) {
  const list = inGroup(g.id);
  return (
    <span className="relative flex shrink-0">
    <span
      className={cn('mb-1.5 flex h-7 shrink-0 select-none items-center gap-1.5 rounded-md px-2 text-ui font-medium text-primary', drop && 'outline outline-2 outline-brand-400')}
      style={{ background: tint(g.color, folded ? 30 : 20), boxShadow: `inset 0 0 0 1px ${tint(g.color, 55)}` }}
    >
      {narrow ? <span className="size-2 rounded-full" style={{ background: gc(g.color) }} /> : g.name}
      {folded && (
        <>
          <span className="text-caption tabular-nums text-subtle">{list.length}</span>
          <GroupMark list={list} />
        </>
      )}
    </span>
    {pop}
    </span>
  );
}

// AGroup is a chip and its tabs, underlined in the group's colour as Chrome
// does; folded, only the chip and the shown session (if it's in there) stay.
function AGroup({ g, folded, narrow, drop, compact, pop }: { g: Group; folded?: boolean; narrow?: boolean; drop?: boolean; compact?: boolean; pop?: ReactNode }) {
  const list = inGroup(g.id);
  const peek = folded ? list.filter((s) => s.id === CURRENT) : list;
  return (
    <div className="relative flex shrink-0 items-end gap-1">
      <Chip g={g} folded={folded} narrow={narrow} drop={drop} pop={pop} />
      {peek.map((s) => (
        <Tab key={s.id} s={s} color={g.color} compact={compact} />
      ))}
      <span className="absolute inset-x-0 bottom-0 h-0.5 rounded-full" style={{ background: tint(g.color, 70) }} />
    </div>
  );
}

function OptionA() {
  const [ui, win, docs] = groups;
  return (
    <>
      <Intro
        title="A · Named, coloured groups that fold into a chip"
        body="Chrome's model. A group is a chip with its name, in one of the five session colours, and an underline running under its tabs. Click the chip to fold the group into it; a folded chip adds up what is inside (waiting count, a stop, else a spinner, else an unread dot). Joining a group gives a session the group's colour, so its agents in the right-hand panel match too. Pinned sessions stay outside groups, first in the row; the shown session peeks out of a folded group so you never lose your place."
      />
      <Frame title="Expanded" note="Two pinned sessions, three open groups, one loose session. At 1040px the row already overflows and scrolls: unfolded groups only add width.">
        <Row>
          {pinned.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <AGroup g={ui} />
          <AGroup g={win} />
          <AGroup g={docs} />
          {loose.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <NewTab />
        </Row>
      </Frame>
      <Frame title="Collapsed" note="Windows and Docs folded: Windows' chip shows 1 waiting and a stopped session; Docs shows an unread dot. Desktop UI is folded too, but the session on screen peeks out after its chip.">
        <Row>
          {pinned.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <AGroup g={ui} folded />
          <AGroup g={win} folded />
          <AGroup g={docs} folded />
          {loose.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <NewTab />
          <RowEnd />
        </Row>
      </Frame>
      <Frame title="Creating a group: the tab's menu" height={330} note="Right-click a tab: Add to new group makes a group of it alone, named after it and in its colour, with the name field open. Add to group lists the others. Multi-select (Ctrl+click) adds several at once.">
        <Row>
          {pinned.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <AGroup g={ui} folded />
          <AGroup g={win} folded />
          <AGroup g={docs} folded />
          <Tab
            s={by('oauth')}
            ring
            pop={
              <Menu>
                <Item icon={Pin}>Pin</Item>
                <Item icon={Pencil} hint="F2">
                  Rename
                </Item>
                <Sep />
                <Item icon={FolderPlus}>Add to new group</Item>
                <Sub
                  left
                  item={
                    <Item icon={Layers} sub active>
                      Add to group
                    </Item>
                  }
                >
                  <Menu className="min-w-44">
                    <Item dot="sky">Desktop UI</Item>
                    <Item dot="peach">Windows</Item>
                    <Item dot="mint">Docs</Item>
                  </Menu>
                </Sub>
                <Sep />
                <Item icon={X}>Close</Item>
              </Menu>
            }
          />
          <NewTab />
          <RowEnd />
        </Row>
      </Frame>
      <Frame title="Creating a group: drag a tab onto another" height={200} note="Dragging OAuth expiry over the Windows chip: dropped there, it joins Windows. Held over the middle of another loose tab for half a second, the two become a new group; over a gap between tabs it only moves, as today. Pinned tabs take no group.">
        <Row>
          {pinned.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <AGroup g={ui} folded />
          <AGroup g={win} folded drop pop={<Dragged topic="OAuth expiry asks to log in" color="lilac" says="Drop into Windows" />} />
          <AGroup g={docs} folded />
          <Tab s={by('oauth')} ghost />
          <NewTab />
          <RowEnd />
        </Row>
      </Frame>
      <Frame title="Renaming and recolouring" height={330} note="Clicking an open chip folds it; right-clicking it (or double-clicking) opens this: the name, the five colours, and what applies to the whole group. Close group sends all its sessions to History in one go, and History can reopen the group whole.">
        <Row>
          {pinned.map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <AGroup
            g={ui}
            pop={
              <Pop>
                <Menu className="w-64">
                  <NameField value="Desktop UI" />
                  <Swatches on="sky" />
                  <Sep />
                  <Item icon={Plus}>New session in group</Item>
                  <Item icon={ChevronsLeftRight} hint="click">
                    Fold
                  </Item>
                  <Item icon={Ungroup}>Ungroup</Item>
                  <Item icon={X}>Close group (3 sessions)</Item>
                </Menu>
              </Pop>
            }
          />
          <AGroup g={win} folded />
          <AGroup g={docs} folded />
          <NewTab />
          <RowEnd />
        </Row>
      </Frame>
      <Frame title="Narrow window (440px)" width={440} height={200} note="Groups other than the shown session's fold by themselves, and a folded chip is its colour dot, count and marks; pinned sessions are icons. What's left scrolls, as today.">
        <Row className="px-3">
          {pinned.map((s) => (
            <Tab key={s.id} s={s} iconOnly />
          ))}
          <AGroup g={ui} folded narrow compact />
          <AGroup g={win} folded narrow />
          <AGroup g={docs} folded narrow />
          <RowEnd narrow />
        </Row>
      </Frame>
    </>
  );
}

// ——— Option B: a button per group, its sessions in a menu ———

// GroupButton is a group in B's row: its colour, name, count and sum, and a
// chevron; its sessions live in its menu. The shown session sits next to its
// button, joined to it, so the row says where you are.
function GroupButton({ g, open, narrow, drop, pop }: { g: Group; open?: boolean; narrow?: boolean; drop?: boolean; pop?: ReactNode }) {
  const list = inGroup(g.id);
  const here = list.find((s) => s.id === CURRENT);
  return (
    <div className="relative flex shrink-0 items-end">
      {pop}
      <span
        className={cn(
          'mb-1.5 flex h-7 shrink-0 select-none items-center gap-1.5 px-2 text-ui text-secondary',
          here ? 'rounded-l-md' : 'rounded-md',
          open ? 'bg-surface-strong text-primary' : 'bg-surface',
          drop && 'outline outline-2 outline-brand-400',
        )}
      >
        <span className="size-2 shrink-0 rounded-full" style={{ background: gc(g.color) }} />
        {!narrow && <span>{g.name}</span>}
        <span className="text-caption tabular-nums text-faint">{list.length}</span>
        <GroupMark list={list.filter((s) => s.id !== CURRENT)} />
        <ChevronDown className="size-3 text-subtle" />
      </span>
      {here && (
        <div className="mb-1.5 flex" data-session-color={g.color}>
          <span className="session-line flex h-7 max-w-56 items-center gap-1.5 rounded-r-md border-l border-line-faint bg-surface-strong pl-2.5 pr-2.5 text-ui text-primary">
            <Mark s={here} on />
            <span className={cn('truncate', narrow && 'max-w-20')}>{here.topic}</span>
            {!narrow && <span className="text-caption tabular-nums text-faint">{here.agents}</span>}
          </span>
        </div>
      )}
    </div>
  );
}

function BRow({ open, drop, ring, pop }: { open?: string; drop?: string; ring?: string; pop?: ReactNode }) {
  return (
    <Row>
      {pinned.map((s) => (
        <Tab key={s.id} s={s} />
      ))}
      {groups.map((g) => (
        <GroupButton key={g.id} g={g} open={open === g.id} drop={drop === g.id} pop={open === g.id || drop === g.id ? pop : undefined} />
      ))}
      <span className="mx-1 mb-2.5 h-4 w-px shrink-0 bg-line" />
      {loose.map((s) => (
        <Tab key={s.id} s={s} compact ring={ring === s.id} ghost={drop !== undefined && s.id === 'oauth'} pop={ring === s.id ? pop : undefined} popEnd />
      ))}
      <NewTab />
      <RowEnd />
    </Row>
  );
}

function SessionLine({ s }: { s: Session }) {
  const on = s.id === CURRENT;
  return (
    <div className={cn('flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-ui', on ? 'bg-surface-raised text-primary' : 'text-secondary')}>
      <span className="flex w-4 justify-center">
        <Mark s={s} on={on} />
      </span>
      <span className="min-w-0 flex-1 truncate">{s.topic}</span>
      {s.agents > 0 && <span className="text-caption tabular-nums text-faint">{s.agents}</span>}
      <X className="size-3 text-faint" />
    </div>
  );
}

function OptionB() {
  return (
    <>
      <Intro
        title="B · A button per group, its sessions in a menu"
        body="Groups never spread across the row: each is one button (colour dot, name, count, and the sum of what waits inside) whose menu lists its sessions. The session on screen is drawn joined to its group's button. Pinned sessions are ordinary tabs before the groups, and loose ones ordinary tabs after them. The row's width is fixed by how many groups there are, not how many sessions, at the price of a click to switch inside a group."
      />
      <Frame title="Collapsed (the resting state)" note="Two pinned, the three groups, then one loose session. Windows' button says 1 waiting and a stop; Docs an unread dot. You are in Desktop UI › Session tab groups.">
        <BRow />
      </Frame>
      <Frame title="Expanded: a group's menu open" height={330} note="Its sessions with their marks, agent counts and a close button; Mod+1…9 still count tabs in the row, and Mod+Alt+↓ opens the current group's menu. The footer adds a session to this group.">
        <BRow open="win" pop={<Pop><Menu className="w-72">
          <div className="flex items-center gap-2 px-2.5 pb-1 pt-1.5 text-caption font-medium text-subtle">
            <span className="size-2 rounded-full" />
            Windows
            <span className="flex-1" />
            <Pencil className="size-3" />
          </div>
          {inGroup('win').map((s) => (
            <SessionLine key={s.id} s={s} />
          ))}
          <Sep />
          <Item icon={Plus}>New session in Windows</Item>
        </Menu></Pop>} />
      </Frame>
      <Frame title="Creating a group: the tab's menu" height={330} note="Right-click a tab: Move to group lists the groups and New group…, which opens the name and colour editor below with the tab already in it.">
        <BRow
          ring="oauth"
          pop={
            <Menu>
              <Item icon={Pin}>Pin</Item>
              <Item icon={Pencil} hint="F2">
                Rename
              </Item>
              <Sep />
              <Sub
                left
                item={
                  <Item icon={Layers} sub active>
                    Move to group
                  </Item>
                }
              >
                <Menu className="min-w-44">
                  <Item dot="sky">Desktop UI</Item>
                  <Item dot="peach">Windows</Item>
                  <Item dot="mint">Docs</Item>
                  <Sep />
                  <Item icon={FolderPlus}>New group…</Item>
                </Menu>
              </Sub>
              <Sep />
              <Item icon={X}>Close</Item>
            </Menu>
          }
        />
      </Frame>
      <Frame title="Creating a group: drag a tab onto a group" note="Drop a tab on a group's button to move it in; drop a tab on another loose tab to make a new group of the two. Dragging out of a group is from its menu onto the row.">
        <BRow drop="win" pop={<Dragged topic="OAuth expiry asks to log in" color="lilac" says="Drop into Windows" />} />
      </Frame>
      <Frame title="Renaming and recolouring" height={330} note="The pencil in a group's menu, or right-clicking its button, turns the menu's head into the editor.">
        <BRow open="docs" pop={<Pop className="left-auto right-0"><Menu className="w-72">
          <NameField value="Docs and release notes" />
          <Swatches on="mint" />
          <Sep />
          {inGroup('docs').map((s) => (
            <SessionLine key={s.id} s={s} />
          ))}
          <Sep />
          <Item icon={Ungroup}>Ungroup</Item>
          <Item icon={X}>Close group (2 sessions)</Item>
        </Menu></Pop>} />
      </Frame>
      <Frame title="Narrow window (440px)" width={440} note="Group buttons drop their names (dot, count, sum); pinned sessions become icons; the joined current tab truncates; loose tabs scroll after the groups. The groups themselves always fit.">
        <Row className="px-3">
          {pinned.map((s) => (
            <Tab key={s.id} s={s} iconOnly />
          ))}
          {groups.map((g) => (
            <GroupButton key={g.id} g={g} narrow />
          ))}
          <Tab s={by('oauth')} compact />
          <RowEnd narrow />
        </Row>
      </Frame>
    </>
  );
}

// ——— Option C: two rows, the groups and then the chosen group's tabs ———

function GroupTab({ g, on, drop, narrow }: { g: Group | 'all'; on?: boolean; drop?: boolean; narrow?: boolean }) {
  const list = g === 'all' ? sessions.filter((s) => !s.pinned) : inGroup(g.id);
  return (
    <span
      className={cn(
        'relative flex h-7 shrink-0 select-none items-center gap-1.5 rounded-md px-2 text-ui',
        on ? 'text-primary' : 'text-muted',
        drop && 'outline outline-2 outline-brand-400',
      )}
    >
      {g !== 'all' && <span className="size-2 shrink-0 rounded-full" style={{ background: gc(g.color) }} />}
      {!(narrow && g !== 'all') && <span className={on ? 'font-medium' : undefined}>{g === 'all' ? 'All' : g.name}</span>}
      <span className="text-caption tabular-nums text-faint">{list.length}</span>
      {!on && <GroupMark list={list} />}
      {on && <span className="absolute inset-x-2 -bottom-1 h-0.5 rounded-full" style={{ background: g === 'all' ? 'var(--color-brand-400)' : gc(g.color) }} />}
    </span>
  );
}

function GroupRow({ on, drop, edit, pop }: { on: string; drop?: string; edit?: boolean; pop?: ReactNode }) {
  return (
    <div className="relative z-20 flex items-center gap-1 border-b border-line-faint px-5 pb-1 pt-1.5">
      <GroupTab g="all" on={on === 'all'} />
      <span className="mx-1 h-4 w-px bg-line" />
      {groups.map((g) =>
        edit && g.id === 'ui' ? (
          <span key={g.id} className="relative flex h-7 items-center gap-1.5 rounded-md border border-brand-400 bg-sunken px-2 text-ui text-primary">
            <span className="size-2 rounded-full" style={{ background: gc(g.color) }} />
            Desktop app UI
            <span className="h-4 w-px animate-pulse bg-primary" />
            {pop}
          </span>
        ) : (
          <GroupTab key={g.id} g={g} on={on === g.id} drop={drop === g.id} />
        ),
      )}
      <span className={cn('relative flex h-7 items-center gap-1 rounded-md px-2 text-caption text-subtle', drop === 'new' && 'outline outline-2 outline-dashed outline-brand-400 text-brand-200')}>
        <Plus className="size-3" />
        {drop === 'new' ? 'Drop for a new group' : 'Group'}
        {drop === 'new' && pop}
      </span>
      <span className="flex-1" />
      <span className="flex h-7 items-center gap-1 px-2 text-muted">
        <AlarmClock className="size-3.5" />
        <span className="text-caption text-faint">1</span>
      </span>
      <span className="flex h-7 items-center gap-1 px-2 text-muted">
        <History className="size-3.5" />
        <span className="text-caption text-faint">14</span>
      </span>
    </div>
  );
}

// GroupLabel marks where a group starts in All's row; scrolled, the row is
// scrolled to its end, past the groups before.
function GroupLabel({ g, scrolled }: { g: Group; scrolled?: boolean }) {
  return (
    <span className="mb-2.5 ml-1 shrink-0 text-micro font-medium uppercase tracking-wide" style={{ color: gc(g.color) }}>
      {scrolled && '‹ '}
      {g.name}
    </span>
  );
}

function PinnedStrip({ iconOnly }: { iconOnly?: boolean }) {
  return (
    <>
      {pinned.map((s) => (
        <Tab key={s.id} s={s} iconOnly={iconOnly} />
      ))}
      <span className="mx-1 mb-2.5 h-4 w-px shrink-0 bg-line" />
    </>
  );
}

function OptionC() {
  return (
    <>
      <Intro
        title="C · Two rows: groups, then the chosen group's tabs"
        body="Like a workspace switcher. The upper row is the groups (All first) with a count and the sum of what waits in each; the lower row is the chosen group's tabs, as today. Pinned sessions are a strip at the start of the lower row in every group, so they are always one click away. A collapsed group is simply one not chosen; All is the expanded view. It costs one more row of height (about 36px) whenever there is a group."
      />
      <Frame title="Expanded: All" height={230} note="Every session, in group order, with a small label where each group starts. Still overflows at 1040px; All is for looking around, a group for working.">
        <GroupRow on="all" />
        <Row className="pt-2">
          <PinnedStrip />
          {groups.map((g) => (
            <div key={g.id} className="flex shrink-0 items-end gap-1">
              <GroupLabel g={g} />
              {inGroup(g.id).map((s) => (
                <Tab key={s.id} s={s} />
              ))}
            </div>
          ))}
        </Row>
      </Frame>
      <Frame title="Collapsed: one group chosen" height={230} note="Desktop UI chosen: its three tabs, with the pinned strip before them. Windows says 1 waiting and a stop, Docs an unread dot, from the upper row. Ungrouped sessions show under All only (or in an Ungrouped group, if we want one).">
        <GroupRow on="ui" />
        <Row className="pt-2">
          <PinnedStrip />
          {inGroup('ui').map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <NewTab />
        </Row>
      </Frame>
      <Frame title="Creating a group: the tab's menu" height={330} note="Right-click a tab: Move to group, or New group from this tab, which adds a group after the others with its name field open.">
        <GroupRow on="all" />
        <Row className="pt-2">
          <PinnedStrip />
          <GroupLabel g={groups[2]} scrolled />
          {inGroup('docs').map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <span className="mb-2.5 ml-1 shrink-0 text-micro font-medium uppercase tracking-wide text-subtle">No group</span>
          <Tab
            s={by('oauth')}
            ring
            pop={
              <Menu>
                <Item icon={Pin}>Pin</Item>
                <Item icon={Pencil} hint="F2">
                  Rename
                </Item>
                <Sep />
                <Item icon={Layers} sub>
                  Move to group
                </Item>
                <Item icon={FolderPlus} active>
                  New group from this tab
                </Item>
                <Sep />
                <Item icon={X}>Close</Item>
              </Menu>
            }
          />
        </Row>
      </Frame>
      <Frame title="Creating a group: drag a tab up" height={230} note="Drag a tab up onto a group to move it there, or onto + Group to start one. Upper and lower rows are far apart, so a drop is never ambiguous with reordering.">
        <GroupRow on="all" drop="new" pop={<Dragged topic="OAuth expiry asks to log in" color="lilac" says="Makes a new group of it" />} />
        <Row className="pt-2">
          <PinnedStrip />
          <GroupLabel g={groups[2]} scrolled />
          {inGroup('docs').map((s) => (
            <Tab key={s.id} s={s} />
          ))}
          <span className="mb-2.5 ml-1 shrink-0 text-micro font-medium uppercase tracking-wide text-subtle">No group</span>
          <Tab s={by('oauth')} ghost />
        </Row>
      </Frame>
      <Frame title="Renaming and recolouring" height={330} note="Double-click a group to rename it in place; right-click for its colour and the rest.">
        <GroupRow
          on="ui"
          edit
          pop={
            <Pop>
              <Menu className="w-60">
                <Swatches on="sky" />
                <Sep />
                <Item icon={Plus}>New session in group</Item>
                <Item icon={Ungroup}>Ungroup</Item>
                <Item icon={X}>Close group (3 sessions)</Item>
              </Menu>
            </Pop>
          }
        />
        <Row className="pt-2">
          <PinnedStrip />
          {inGroup('ui').map((s) => (
            <Tab key={s.id} s={s} />
          ))}
        </Row>
      </Frame>
      <Frame title="Narrow window (440px)" width={440} height={230} note="The upper row keeps All and the dots with their counts and sums, named only when chosen; the lower row scrolls as today.">
        <div className="flex items-center gap-0.5 border-b border-line-faint px-3 pb-1 pt-1.5">
          <GroupTab g="all" narrow />
          {groups.map((g) => (g.id === 'ui' ? <GroupTab key={g.id} g={g} on /> : <GroupTab key={g.id} g={g} narrow />))}
          <span className="flex-1" />
          <History className="size-3.5 text-muted" />
        </div>
        <Row className="px-3 pt-2">
          <PinnedStrip iconOnly />
          {inGroup('ui').map((s) => (
            <Tab key={s.id} s={s} compact />
          ))}
        </Row>
      </Frame>
    </>
  );
}

// ——— The page ———

function Intro({ title, body }: { title: string; body: string }) {
  return (
    <header className="flex max-w-[1040px] flex-col gap-1.5">
      <h2 className="text-heading font-semibold text-title">{title}</h2>
      <p className="text-ui text-secondary">{body}</p>
    </header>
  );
}

export function TabGroupsConcept() {
  const option = new URLSearchParams(location.search).get('option') ?? 'a';
  return (
    <div className="flex min-h-screen flex-col gap-7 bg-ink p-8" data-tabgroups-option={option}>
      {option === 'a' && <OptionA />}
      {option === 'b' && <OptionB />}
      {option === 'c' && <OptionC />}
    </div>
  );
}
