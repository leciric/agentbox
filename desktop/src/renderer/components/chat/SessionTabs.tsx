import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { isUnread, SESSION_COLORS, sessionColor, sessionStatus, useSeenAt } from '../../lib/sessions';
import { dropAt, groupSum, tabRow, type DropBox, type Drop, type Dragged } from '../../lib/tabGroups';
import { SessionStatusMark } from './SessionStatusMark';
import { keysHint, sessionKeys } from '../../lib/shortcuts';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuMore, ContextMenuSeparator, ContextMenuTrigger } from '../ui/context-menu';
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from '../ui/popover';
import { wakeTime } from './SessionHeader';
import { AlarmClock, ChevronDown, ChevronRight, FolderPlus, History, Layers, LoaderCircle, LogOut, Pencil, Pin, PinOff, Plus, TriangleAlert, Ungroup, X } from 'lucide-react';
import { Tip } from '../ui/tooltip';

// SessionTabs is the row above a project's chat: one tab per session, with
// where it stands before its name (SessionStatusMark: what waits on you, a
// stop, a spinner while its lead works, or a dot when it has said something
// since you last looked, here or on the phone); each is a folder tab on the box of the chat below it, in its
// colour (sessionColor) once there are several, the box and the bracket of the
// agents it made having the same; + opens a new one at once, untitled until its first turn
// names it (ProjectChatPanel). A tab's ×, and its shortcut
// (lib/shortcuts.ts), close the session into the chat's history, which the
// History button at the end of the row opens (SessionHistory): nothing is
// lost, so nothing asks first. Double-clicking a tab renames it; its
// right-click menu pins it before the others or moves it into a tab group,
// and dragging it moves it. All of it is kept by the daemon
// (ProjectChatPanel), and Mod+1…9 goes to the Nth thing in the row.
//
// Tab groups (lib/tabGroups.ts) sit between the pinned tabs and the loose
// ones, each one button: its colour, name, count and the sum of what waits
// in it (groupSum), its sessions in a menu (GroupMenu), and the session on
// screen, when it is one of them, joined to its right. A group not collapsed
// spreads its tabs after the button instead. Dropping a tab on a group's
// button moves it in, and on the middle of a loose tab makes a group of the
// two; a group's button drags among the groups. A row too narrow for it all
// tightens in steps (useTight): first the pinned tabs lose their names and
// the others shorten, then the group buttons lose theirs.
export function SessionTabs({
  sessions,
  groups = [],
  current,
  adding,
  closedCount,
  snoozed = [],
  onWake,
  onChoose,
  onAdd,
  onClose,
  onHistory,
  onRename,
  onPin,
  onMove,
  onGroup,
  onNewGroup,
  onUpdateGroup,
  onUngroup,
  onOrderGroups,
  onAddToGroup,
}: {
  sessions: T.LeadSession[];
  groups?: T.LeadSessionGroup[];
  current?: string;
  adding: boolean;
  closedCount: number;
  // snoozed are the sessions hidden until a time (lib/sessions.ts isSnoozed),
  // listed apart at the end of the row; onWake brings one back now.
  snoozed?: T.LeadSession[];
  onWake?: (id: string) => void;
  onChoose: (id: string) => void;
  onAdd: () => void;
  onClose: (id: string) => void;
  onHistory: () => void;
  onRename: (id: string, topic: string) => void;
  onPin: (id: string, pinned: boolean) => void;
  // onMove drops id before another session, or after the last with none,
  // in its own lot (lib/tabGroups moveBefore).
  onMove: (id: string, before: string | undefined) => void;
  // onGroup moves a session into a group, or with '' out of its own.
  onGroup?: (id: string, group: string) => void;
  // onNewGroup makes a group of sessions, answering it once made, when the
  // row opens its editor.
  onNewGroup?: (ids: string[]) => Promise<T.LeadSessionGroup | undefined>;
  onUpdateGroup?: (id: string, patch: T.LeadSessionGroupPatch) => void;
  onUngroup?: (id: string) => void;
  onOrderGroups?: (ids: string[]) => void;
  onAddToGroup?: (group: string) => void;
}) {
  const t = useT();
  const seen = useSeenAt();
  const [renaming, setRenaming] = useState<string>();
  // The group whose menu is open, and whether on its editor.
  const [menu, setMenu] = useState<{ group: string; editing: boolean }>();
  // What is being dragged, and what letting go would do (lib/tabGroups
  // dropAt). Pointer events rather than HTML5 drag and drop: they move the
  // same under a mouse, a pen or a touchpad, and a drag only starts past a
  // few pixels, so a click stays a click.
  const [dragging, setDragging] = useState<Dragged>();
  const [drop, setDrop] = useState<Drop>();
  const row = useRef<HTMLDivElement>(null);
  const press = useRef<{ drag: Dragged; x: number; moved: boolean } | null>(null);
  const justDragged = useRef(false);
  const items = tabRow(sessions, groups);
  const tight = useTight(row, items.map((it) => (it.kind === 'tab' ? it.session.id + it.session.topic : `${it.group.id}${it.group.name}${it.group.collapsed}`)).join('|') + (current ?? ''));
  const boxes = (): DropBox[] =>
    [...(row.current?.querySelectorAll<HTMLElement>('[data-drop-id]') ?? [])].map((el) => {
      const box = el.getBoundingClientRect();
      return {
        kind: el.dataset.dropKind === 'group' ? 'group' : 'tab',
        id: el.dataset.dropId!,
        left: box.left,
        width: box.width,
        pinned: el.dataset.dropPinned === 'true',
        group: el.dataset.dropGroup || undefined,
      };
    });
  const endDrag = () => {
    press.current = null;
    setDragging(undefined);
    setDrop(undefined);
  };
  const letGo = (drag: Dragged, d: Drop) => {
    justDragged.current = true;
    if (drag.kind === 'group') {
      if (d.kind !== 'gap' || d.before === drag.id) return;
      const ids = groups.map((g) => g.id).filter((id) => id !== drag.id);
      const at = d.before === undefined ? ids.length : ids.indexOf(d.before);
      ids.splice(at < 0 ? ids.length : at, 0, drag.id);
      onOrderGroups?.(ids);
      return;
    }
    if (d.kind === 'gap') onMove(drag.id, d.before);
    else if (d.kind === 'into') onGroup?.(drag.id, d.group);
    else void onNewGroup?.([d.with, drag.id]).then((g) => g && setMenu({ group: g.id, editing: true }));
  };
  // drag is what a pointer pressed on a tab or a group's button does.
  const drag = (what: Dragged) => ({
    onPointerDown: (e: React.PointerEvent<HTMLElement>) => {
      if (e.button !== 0 || (e.target as Element).closest('[data-session-close]')) return;
      press.current = { drag: what, x: e.clientX, moved: false };
      e.currentTarget.setPointerCapture(e.pointerId);
    },
    onPointerMove: (e: React.PointerEvent<HTMLElement>) => {
      const p = press.current;
      if (!p || p.drag.id !== what.id || p.drag.kind !== what.kind) return;
      if (!p.moved && Math.abs(e.clientX - p.x) < 6) return;
      p.moved = true;
      setDragging(what);
      setDrop(dropAt(boxes(), e.clientX, what));
    },
    onPointerUp: (e: React.PointerEvent<HTMLElement>) => {
      const p = press.current;
      if (p?.moved) letGo(p.drag, dropAt(boxes(), e.clientX, p.drag));
      endDrag();
    },
    onPointerCancel: endDrag,
  });
  // A click that ends a drag doesn't choose what it was on.
  const clicked = (fn: () => void) => () => {
    if (justDragged.current) {
      justDragged.current = false;
      return;
    }
    fn();
  };
  // gapMark is the brand rule a drop would land at, before an element or
  // after the last of its kind.
  const gapMark = (kind: 'tab' | 'group', id: string, last: boolean) => {
    if (!dragging || drop?.kind !== 'gap' || dragging.id === id || (dragging.kind === 'group') !== (kind === 'group')) return undefined;
    if (drop.before === id) return 'shadow-[inset_2px_0_0_var(--color-brand-400)]';
    if (drop.before === undefined && last) return 'shadow-[inset_-2px_0_0_var(--color-brand-400)]';
    return undefined;
  };
  const tabIDs = items.flatMap((it) => (it.kind === 'tab' ? [it.session.id] : it.group.collapsed ? [] : it.sessions.map((s) => s.id)));
  const groupIDs = items.flatMap((it) => (it.kind === 'group' ? [it.group.id] : []));
  const hasLoose = items.some((it) => it.kind === 'tab' && !it.session.pinned);

  const tab = (s: T.LeadSession, opts: { group?: T.LeadSessionGroup; joined?: boolean } = {}) => {
    const on = s.id === current;
    const unread = !on && isUnread(s, seen[s.ref]);
    const color = sessionColor(sessions, s);
    const iconOnly = tight >= 1 && !!s.pinned;
    if (renaming === s.id) {
      return (
        <TopicInput
          key={s.id}
          initial={s.topic}
          label={t('sessions.rename')}
          onDone={(topic) => {
            setRenaming(undefined);
            if (topic && topic !== s.topic) onRename(s.id, topic);
          }}
        />
      );
    }
    const what: Dragged = { kind: 'tab', id: s.id, pinned: !!s.pinned, group: s.group };
    const target = drop?.kind === 'merge' && drop.with === s.id;
    return (
      <ContextMenu key={s.id}>
        {/* The close button is the tab's sibling, not inside it: a
            control inside a tab is a second, nested stop for a screen
            reader. From the keyboard, Delete on the tab (or its menu)
            closes it; the button is for the pointer. */}
        <div role="none" className={cn('group relative flex shrink-0', opts.joined && 'mb-1.5')}>
          <ContextMenuTrigger asChild>
            <div
              role="tab"
              aria-selected={on}
              aria-keyshortcuts="Delete F2"
              aria-label={iconOnly ? s.topic : undefined}
              title={iconOnly ? s.topic : undefined}
              tabIndex={on ? 0 : -1}
              data-session={s.id}
              data-session-pinned={s.pinned || undefined}
              data-session-state={sessionStatus(s)}
              data-session-color={color}
              data-session-in-group={opts.group?.id}
              // A tab joined to its group's button is a drop into the group.
              data-drop-kind={opts.joined ? 'group' : 'tab'}
              data-drop-id={opts.joined ? opts.group!.id : s.id}
              data-drop-pinned={!!s.pinned}
              data-drop-group={opts.joined ? undefined : opts.group?.id}
              {...drag(what)}
              onClick={clicked(() => onChoose(s.id))}
              onDoubleClick={() => setRenaming(s.id)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') onChoose(s.id);
                if (e.key === 'F2') setRenaming(s.id);
                if (e.key === 'Delete') onClose(s.id);
              }}
              className={cn(
                'flex h-7 max-w-56 shrink-0 cursor-pointer select-none items-center gap-1.5 text-ui transition-colors',
                !opts.joined && 'mb-1.5',
                opts.joined ? 'rounded-r-md border-l border-line-faint' : 'rounded-md',
                iconOnly ? 'w-8 justify-center px-0' : 'pl-2.5 pr-7',
                color && (on ? 'session-line' : 'session-line-quiet'),
                on ? 'bg-surface-strong text-primary' : 'text-muted hover:bg-surface hover:text-secondary',
                dragging?.kind === 'tab' && dragging.id === s.id && 'cursor-grabbing opacity-50',
                target && 'outline outline-2 outline-brand-400',
                !opts.joined && gapMark('tab', s.id, tabIDs.at(-1) === s.id),
              )}
            >
              <SessionStatusMark session={s} unread={unread} />
              {(iconOnly ? sessionStatus(s) === 'idle' && !unread : !unread && sessionStatus(s) === 'idle' && s.pinned) && (
                <Pin aria-label={t('sessions.pinned')} className="size-3 shrink-0 -rotate-45 text-subtle" />
              )}
              {!iconOnly && <span className={cn('min-w-0 truncate', tight >= 1 && 'max-w-24')}>{s.topic}</span>}
              {!iconOnly && s.agents > 0 && <span className="shrink-0 text-caption tabular-nums text-faint">{s.agents}</span>}
            </div>
          </ContextMenuTrigger>
          {!iconOnly && (
            <Tip label={on ? `${t('sessions.close')} · ${keysHint(sessionKeys.close)}` : t('sessions.close')}>
              <button
                type="button"
                aria-label={t('sessions.closeNamed', { topic: s.topic })}
                data-session-close={s.id}
                tabIndex={-1}
                aria-hidden
                onClick={(e) => {
                  e.stopPropagation();
                  onClose(s.id);
                }}
                className={cn('absolute right-1.5 top-1 flex size-5 items-center justify-center rounded-md text-faint hover:bg-surface-strong hover:text-primary', !on && 'opacity-0 group-hover:opacity-100 group-has-[:focus-visible]:opacity-100')}
              >
                <X className="size-3" />
              </button>
            </Tip>
          )}
        </div>
        <ContextMenuContent>
          <ContextMenuItem icon={s.pinned ? PinOff : Pin} onSelect={() => onPin(s.id, !s.pinned)} data-session-pin={s.id}>
            {s.pinned ? t('sessions.unpin') : t('sessions.pin')}
          </ContextMenuItem>
          <ContextMenuItem icon={Pencil} onSelect={() => setRenaming(s.id)} hint="F2">
            {t('sessions.rename')}
          </ContextMenuItem>
          {!s.pinned && onGroup && (
            <>
              <ContextMenuSeparator />
              <ContextMenuMore label={t('sessions.group.moveTo')} icon={Layers}>
                {groups
                  .filter((g) => g.id !== s.group)
                  .map((g) => (
                    <ContextMenuItem key={g.id} onSelect={() => onGroup(s.id, g.id)} data-session-move-to={g.id}>
                      <span className="flex items-center gap-2.5">
                        <GroupDot color={g.color} />
                        <span className="truncate">{g.name || t('sessions.group.unnamed')}</span>
                      </span>
                    </ContextMenuItem>
                  ))}
                {groups.some((g) => g.id !== s.group) && <ContextMenuSeparator />}
                <ContextMenuItem icon={FolderPlus} onSelect={() => void onNewGroup?.([s.id]).then((g) => g && setMenu({ group: g.id, editing: true }))} data-session-new-group={s.id}>
                  {t('sessions.group.new')}
                </ContextMenuItem>
              </ContextMenuMore>
              {s.group && (
                <ContextMenuItem icon={LogOut} onSelect={() => onGroup(s.id, '')} data-session-ungroup={s.id}>
                  {t('sessions.group.remove')}
                </ContextMenuItem>
              )}
            </>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem icon={X} onSelect={() => onClose(s.id)}>
            {t('sessions.close')}
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    );
  };

  const groupItem = (g: T.LeadSessionGroup, members: T.LeadSession[]) => {
    const here = members.find((s) => s.id === current);
    const joined = g.collapsed && here;
    const sum = groupSum(members, g.collapsed ? current : undefined, seen);
    const open = menu?.group === g.id;
    const into = drop?.kind === 'into' && drop.group === g.id;
    const name = g.name || t('sessions.group.unnamed');
    return (
      <div key={g.id} className="flex shrink-0 items-end" data-session-group={g.id} data-session-group-collapsed={g.collapsed || undefined}>
        <Popover open={open} onOpenChange={(o) => setMenu(o ? { group: g.id, editing: false } : undefined)}>
          <PopoverAnchor asChild>
            <button
              type="button"
              role="tab"
              aria-selected={!!here}
              aria-haspopup="dialog"
              aria-expanded={open}
              aria-label={t('sessions.group.label', { name, count: members.length })}
              title={tight >= 2 ? name : undefined}
              tabIndex={here && g.collapsed ? -1 : 0}
              data-session-group-button={g.id}
              data-drop-kind="group"
              data-drop-id={g.id}
              data-session-color={g.color}
              {...drag({ kind: 'group', id: g.id })}
              onClick={clicked(() => setMenu(open ? undefined : { group: g.id, editing: false }))}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ group: g.id, editing: true });
              }}
              className={cn(
                'mb-1.5 flex h-7 shrink-0 cursor-pointer select-none items-center gap-1.5 px-2 text-ui text-secondary outline-none transition-colors focus-visible:ring-2 focus-visible:ring-brand-400',
                joined ? 'rounded-l-md' : 'rounded-md',
                open ? 'bg-surface-strong text-primary' : 'bg-surface hover:bg-surface-raised',
                into && 'outline outline-2 outline-brand-400',
                dragging?.kind === 'group' && dragging.id === g.id && 'cursor-grabbing opacity-50',
                gapMark('group', g.id, groupIDs.at(-1) === g.id),
              )}
            >
              <GroupDot color={g.color} />
              {tight < 2 && g.name && <span className={cn('truncate', tight >= 1 ? 'max-w-24' : 'max-w-40')}>{g.name}</span>}
              <span className="text-caption tabular-nums text-faint">{members.length}</span>
              <GroupSumMark sum={sum} />
              {g.collapsed ? <ChevronDown className="size-3 text-subtle" /> : <ChevronRight className="size-3 text-subtle" />}
            </button>
          </PopoverAnchor>
          <PopoverContent align="start" className="w-72 p-1" data-session-group-menu={g.id} onOpenAutoFocus={(e) => menu?.editing || e.preventDefault()}>
            <GroupMenu
              group={g}
              sessions={members}
              current={current}
              seen={seen}
              editing={!!menu?.editing}
              onEdit={(editing) => setMenu({ group: g.id, editing })}
              onChoose={(id) => {
                setMenu(undefined);
                onChoose(id);
              }}
              onClose={onClose}
              onRemove={(id) => onGroup?.(id, '')}
              onUpdate={(patch) => onUpdateGroup?.(g.id, patch)}
              onNew={() => {
                setMenu(undefined);
                onAddToGroup?.(g.id);
              }}
              onUngroup={() => {
                setMenu(undefined);
                onUngroup?.(g.id);
              }}
              onCloseAll={() => {
                setMenu(undefined);
                for (const s of members) onClose(s.id);
              }}
            />
          </PopoverContent>
        </Popover>
        {joined && tab(here, { group: g, joined: true })}
        {!g.collapsed && <div className="ml-1 flex shrink-0 items-end gap-1">{members.map((s) => tab(s, { group: g }))}</div>}
      </div>
    );
  };

  return (
    <div ref={row} data-session-tabs data-session-tabs-tight={tight || undefined} className="relative z-10 flex min-w-0 shrink-0 items-end gap-1 overflow-x-auto px-5 pt-1">
      {/* Only the tabs and the groups are in the tablist: the row's buttons
          beside them (New, Snoozed, History) are not tabs. */}
      <div role="tablist" aria-label={t('sessions.label')} className="flex shrink-0 items-end gap-1">
        {items.map((it, i) => {
          if (it.kind === 'group') return groupItem(it.group, it.sessions);
          // A rule between the groups and the loose tabs after them.
          const rule = !it.session.pinned && items[i - 1]?.kind === 'group';
          return rule ? (
            <div key={it.session.id} role="none" className="flex shrink-0 items-end gap-1">
              <span aria-hidden className="mx-1 mb-2.5 h-4 w-px shrink-0 bg-line" />
              {tab(it.session)}
            </div>
          ) : (
            tab(it.session)
          );
        })}
        {!hasLoose && groupIDs.length > 0 && <span aria-hidden className="mx-1 mb-2.5 h-4 w-px shrink-0 bg-line" />}
      </div>
      <Tip label={`${t('sessions.new')} · ${keysHint(sessionKeys.new)}`}>
        <Button variant="ghost" size="sm" className="mb-0.5 h-7 shrink-0 gap-1 px-2 text-ui" onClick={onAdd} disabled={adding} aria-label={t('sessions.new')} data-session-new>
          <Plus className="size-3.5" />
          {sessions.length === 0 && t('sessions.new')}
        </Button>
      </Tip>
      <span className="min-w-2 flex-1" />
      {dragging?.kind === 'tab' && (drop?.kind === 'into' || drop?.kind === 'merge') && (
        <span role="status" className="mb-2 shrink-0 truncate text-caption text-brand-200" data-session-drop-hint>
          {drop.kind === 'into' ? t('sessions.group.dropInto') : t('sessions.group.dropMerge')}
        </span>
      )}
      {snoozed.length > 0 && <SnoozedSessions sessions={snoozed} narrow={tight >= 1} onWake={(id) => onWake?.(id)} />}
      <Tip label={closedCount > 0 ? t('sessions.historyCount', { count: closedCount }) : t('sessions.history')}>
        <Button variant="ghost" size="sm" className="mb-0.5 h-7 shrink-0 gap-1 px-2 text-ui" onClick={onHistory} aria-label={t('sessions.history')} data-session-history-open>
          <History className="size-3.5" />
          {closedCount > 0 && tight < 1 && <span className="tabular-nums text-caption text-faint">{closedCount}</span>}
        </Button>
      </Tip>
    </div>
  );
}

// useTight is how far the row tightens to fit: 0 shows it all; 1 puts the
// pinned tabs as icons and shortens the other names; 2 also takes the names
// off the group buttons. It steps up while the row overflows, and back down
// once it has the room the step below was measured to need; a change to
// what is in it (shape) measures again from 0.
function useTight(row: React.RefObject<HTMLElement | null>, shape: string): number {
  const [tight, setTight] = useState(0);
  const [tick, setTick] = useState(0);
  const need = useRef<number[]>([]);
  const lastShape = useRef(shape);
  useLayoutEffect(() => {
    const el = row.current;
    if (!el) return;
    if (shape !== lastShape.current) {
      lastShape.current = shape;
      need.current = [];
      if (tight > 0) {
        setTight(0);
        return;
      }
    }
    need.current[tight] = el.scrollWidth;
    if (el.scrollWidth > el.clientWidth + 1 && tight < 2) setTight(tight + 1);
    else if (tight > 0 && el.clientWidth >= (need.current[tight - 1] ?? Infinity)) setTight(tight - 1);
  }, [row, tight, shape, tick]);
  useLayoutEffect(() => {
    const el = row.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => setTick((n) => n + 1));
    ro.observe(el);
    return () => ro.disconnect();
  }, [row]);
  return tight;
}

function GroupDot({ color }: { color: string }) {
  return <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ background: `var(--ab-session-${color})` }} />;
}

// GroupSumMark draws a group's sum (lib/tabGroups groupSum) in the marks a
// tab uses: the amber count and the warning both, as both are yours to act
// on; a spinner or an unread dot only when neither shows.
function GroupSumMark({ sum }: { sum: ReturnType<typeof groupSum> }) {
  const t = useT();
  return (
    <>
      {sum.needsYou > 0 && (
        <span
          role="img"
          aria-label={t('sessions.needsYou', { count: sum.needsYou })}
          data-session-status="needsYou"
          className="flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-amber-400 px-1 text-micro font-semibold tabular-nums leading-none text-black"
        >
          {sum.needsYou}
        </span>
      )}
      {sum.failed && <TriangleAlert role="img" aria-label={t('sessions.failed')} data-session-status="failed" className="size-3 shrink-0 text-rose-300" />}
      {sum.working && <LoaderCircle role="img" aria-label={t('sessions.working')} data-session-status="working" className="size-3 shrink-0 animate-spin text-brand-300" />}
      {sum.unread && <span role="img" aria-label={t('sessions.unread')} data-session-status="unread" className="size-1.5 shrink-0 rounded-full bg-brand-400" />}
    </>
  );
}

// GroupMenu is a group's menu: its name (or, editing, its name and colour),
// its sessions with their marks, agent counts, and buttons to take one out
// of the group or close it, then a new session in it, spreading or folding
// its tabs, ungrouping, and closing every session in it.
function GroupMenu({
  group,
  sessions,
  current,
  seen,
  editing,
  onEdit,
  onChoose,
  onClose,
  onRemove,
  onUpdate,
  onNew,
  onUngroup,
  onCloseAll,
}: {
  group: T.LeadSessionGroup;
  sessions: T.LeadSession[];
  current?: string;
  seen: Record<string, string>;
  editing: boolean;
  onEdit: (editing: boolean) => void;
  onChoose: (id: string) => void;
  onClose: (id: string) => void;
  onRemove: (id: string) => void;
  onUpdate: (patch: T.LeadSessionGroupPatch) => void;
  onNew: () => void;
  onUngroup: () => void;
  onCloseAll: () => void;
}) {
  const t = useT();
  return (
    <div className="flex flex-col">
      {editing ? (
        <GroupEditor group={group} onUpdate={onUpdate} onDone={() => onEdit(false)} />
      ) : (
        <div className="flex items-center gap-2 px-2.5 pb-1 pt-1.5 text-caption font-medium text-subtle">
          <GroupDot color={group.color} />
          <span className="min-w-0 flex-1 truncate">{group.name || t('sessions.group.unnamed')}</span>
          <Tip label={t('sessions.group.edit')}>
            <button type="button" aria-label={t('sessions.group.edit')} onClick={() => onEdit(true)} className="flex size-6 items-center justify-center rounded-md text-subtle hover:bg-surface-raised hover:text-primary" data-session-group-edit>
              <Pencil className="size-3" />
            </button>
          </Tip>
        </div>
      )}
      {sessions.map((s) => {
        const on = s.id === current;
        return (
          <div key={s.id} className={cn('group/line flex items-center gap-2 rounded-lg px-2.5 py-1 text-ui', on ? 'bg-surface-raised text-primary' : 'text-secondary hover:bg-surface-raised')} data-session-group-line={s.id}>
            <button type="button" onClick={() => onChoose(s.id)} className="flex min-w-0 flex-1 items-center gap-2 py-0.5 text-left outline-none focus-visible:underline" data-session-group-choose={s.id}>
              <span className="flex w-4 shrink-0 justify-center">
                <SessionStatusMark session={s} unread={!on && isUnread(s, seen[s.ref])} />
              </span>
              <span className="min-w-0 flex-1 truncate">{s.topic}</span>
              {s.agents > 0 && <span className="shrink-0 text-caption tabular-nums text-faint">{s.agents}</span>}
            </button>
            <Tip label={t('sessions.group.remove')}>
              <button type="button" aria-label={t('sessions.group.removeNamed', { topic: s.topic })} onClick={() => onRemove(s.id)} className="flex size-5 shrink-0 items-center justify-center rounded-md text-faint opacity-0 hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover/line:opacity-100" data-session-group-remove={s.id}>
                <LogOut className="size-3" />
              </button>
            </Tip>
            <Tip label={t('sessions.close')}>
              <button type="button" aria-label={t('sessions.closeNamed', { topic: s.topic })} onClick={() => onClose(s.id)} className="flex size-5 shrink-0 items-center justify-center rounded-md text-faint hover:bg-surface-strong hover:text-primary" data-session-group-close={s.id}>
                <X className="size-3" />
              </button>
            </Tip>
          </div>
        );
      })}
      <div className="mx-1 my-1 h-px bg-line-faint" />
      <MenuButton icon={Plus} onClick={onNew} data-session-group-new>
        {t('sessions.group.newSession')}
      </MenuButton>
      <MenuButton icon={group.collapsed ? ChevronRight : ChevronDown} onClick={() => onUpdate({ collapsed: !group.collapsed })} data-session-group-collapse>
        {group.collapsed ? t('sessions.group.spread') : t('sessions.group.fold')}
      </MenuButton>
      <MenuButton icon={Ungroup} onClick={onUngroup} data-session-group-ungroup>
        {t('sessions.group.ungroup')}
      </MenuButton>
      <MenuButton icon={X} onClick={onCloseAll} data-session-group-close-all>
        {t('sessions.group.closeAll', { count: sessions.length })}
      </MenuButton>
    </div>
  );
}

function MenuButton({ icon: Icon, children, onClick, ...rest }: { icon: typeof Plus; children: ReactNode; onClick: () => void } & Record<`data-${string}`, boolean | string | undefined>) {
  return (
    <button type="button" onClick={onClick} className="flex select-none items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-ui text-secondary outline-none hover:bg-surface-raised focus-visible:bg-surface-raised" {...rest}>
      <Icon className="size-4 text-subtle" />
      <span className="min-w-0 flex-1 truncate">{children}</span>
    </button>
  );
}

// GroupEditor is a group's name, typed in place (Enter or leaving keeps it,
// Escape gives up), and its colour, one of the sessions' (SESSION_COLORS).
function GroupEditor({ group, onUpdate, onDone }: { group: T.LeadSessionGroup; onUpdate: (patch: T.LeadSessionGroupPatch) => void; onDone: () => void }) {
  const t = useT();
  const [name, setName] = useState(group.name);
  const keep = () => {
    if (name.trim() !== group.name) onUpdate({ name: name.trim() });
  };
  return (
    <div className="flex flex-col" data-session-group-editor>
      <div className="px-1.5 pt-1.5">
        <input
          autoFocus
          aria-label={t('sessions.group.name')}
          placeholder={t('sessions.group.unnamed')}
          value={name}
          maxLength={60}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              keep();
              onDone();
            }
            if (e.key === 'Escape') {
              e.stopPropagation();
              onDone();
            }
          }}
          onBlur={keep}
          className="h-8 w-full rounded-lg border border-line-strong bg-sunken px-2.5 text-ui text-primary outline-none placeholder:text-faint focus:border-brand-400"
          data-session-group-name
        />
      </div>
      <div role="radiogroup" aria-label={t('shell.sidebar.color')} className="flex items-center gap-2 px-2.5 py-2">
        {SESSION_COLORS.map((c) => (
          <Tip key={c} label={t('shell.sidebar.colorName', { color: c })} side="top">
            <button
              type="button"
              role="radio"
              aria-checked={c === group.color}
              aria-label={t('shell.sidebar.colorName', { color: c })}
              onClick={() => onUpdate({ color: c })}
              className={cn('size-5 rounded-full outline-none focus-visible:ring-2 focus-visible:ring-brand-400', c === group.color && 'outline outline-2 outline-offset-2 outline-brand-400')}
              style={{ background: `var(--ab-session-${c})` }}
              data-session-group-color={c}
            />
          </Tip>
        ))}
      </div>
      <div className="mx-1 my-1 h-px bg-line-faint" />
    </div>
  );
}

// SnoozedSessions is the row's Snoozed button and its list: each snoozed
// session with when it comes back, and Wake now.
function SnoozedSessions({ sessions, narrow, onWake }: { sessions: T.LeadSession[]; narrow?: boolean; onWake: (id: string) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tip label={t('sessions.snoozed.count', { count: sessions.length })}>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="sm" className="mb-0.5 h-7 shrink-0 gap-1 px-2 text-ui" aria-label={t('sessions.snoozed.title')} data-session-snoozed-open>
            <AlarmClock className="size-3.5" />
            {!narrow && <span className="tabular-nums text-caption text-faint">{sessions.length}</span>}
          </Button>
        </PopoverTrigger>
      </Tip>
      <PopoverContent className="w-80 p-1.5" data-session-snoozed>
        <div className="px-2 pb-1.5 pt-1 text-caption font-medium text-subtle">{t('sessions.snoozed.title')}</div>
        {sessions.map((s) => (
          <div key={s.id} className="flex min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface-raised" data-session-snoozed-row={s.id}>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-ui text-primary">{s.topic}</span>
              <span className="block text-caption text-subtle">{t('sessions.snoozed.back', { when: wakeTime(s.snoozedUntil!) })}</span>
            </span>
            <Button
              variant="secondary"
              size="sm"
              className="h-7 shrink-0 px-2 text-caption"
              onClick={() => {
                setOpen(false);
                onWake(s.id);
              }}
              data-session-wake={s.id}
            >
              {t('sessions.snoozed.wake')}
            </Button>
          </div>
        ))}
      </PopoverContent>
    </Popover>
  );
}

// TopicInput is a topic typed in place: Enter keeps it, Escape or leaving
// the field empty gives up.
export function TopicInput({ initial = '', label, placeholder, onDone }: { initial?: string; label: string; placeholder?: string; onDone: (topic: string) => void }) {
  const [value, setValue] = useState(initial);
  const done = useRef(false);
  const finish = (topic: string) => {
    if (done.current) return;
    done.current = true;
    onDone(topic.trim());
  };
  return (
    <input
      autoFocus
      aria-label={label}
      placeholder={placeholder}
      value={value}
      maxLength={200}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') finish(value);
        if (e.key === 'Escape') finish('');
      }}
      onBlur={() => finish(value === initial ? '' : value)}
      className="h-7 w-56 shrink-0 rounded-lg border border-line-strong bg-surface px-2.5 text-ui text-primary outline-none placeholder:text-faint focus:border-brand-400"
    />
  );
}
