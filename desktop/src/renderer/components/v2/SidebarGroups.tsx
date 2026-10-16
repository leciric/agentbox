// The v2 sidebar's groups (lib/sidebarGroups.ts): naming one, the Groups
// dialog from the sidebar's foot where they're renamed, reordered and
// deleted, the switcher beside the filters, and a group's header with its
// right-click menu.
import { ArrowDown, ArrowUp, ChevronRight, EyeOff, Folders, Pencil, Plus, Trash2 } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { addGroup, moveGroup, removeGroup, renameGroup } from '../../lib/sidebarGroups';
import { newGroupId, useSidebarLayout } from '../../lib/useSidebarLayout';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from '../ui/context-menu';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../ui/dialog';
import { Input } from '../ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { Tip } from '../ui/tooltip';

// GroupNameDialog asks for a group's name: a new one's, or a rename.
export function GroupNameDialog({
  open,
  onOpenChange,
  title,
  description,
  initial = '',
  confirm,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  initial?: string;
  confirm: string;
  onSubmit: (name: string) => void;
}) {
  const t = useT();
  const [name, setName] = useState(initial);
  useEffect(() => {
    if (open) setName(initial);
  }, [open, initial]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm" data-v2-group-name-dialog>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!name.trim()) return;
            onSubmit(name.trim());
            onOpenChange(false);
          }}
        >
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={t('v2.groups.placeholder')} aria-label={t('v2.groups.name')} data-v2-group-name />
          <DialogFooter>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!name.trim()} data-v2-group-name-submit>
              {confirm}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// GroupsDialog lists the groups in their order, each renamed in place (on
// Enter or leaving the field), moved up or down, or deleted, which leaves its
// projects in no group; and adds one at the end.
export function GroupsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const { layout, save } = useSidebarLayout();
  const [name, setName] = useState('');
  const last = layout.groups.length - 1;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md" data-v2-groups-dialog>
        <DialogHeader>
          <DialogTitle>{t('v2.groups.title')}</DialogTitle>
          <DialogDescription>{t('v2.groups.description')}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-1.5">
          {layout.groups.length === 0 && <div className="text-[12.5px] text-subtle">{t('v2.groups.empty')}</div>}
          {layout.groups.map((g, i) => (
            <div key={g.id} className="flex items-center gap-1.5" data-v2-groups-row={g.id}>
              <RenameField group={g} onRename={(n) => save(renameGroup(layout, g.id, n))} />
              <span className="w-20 shrink-0 text-right text-[11px] tabular-nums text-subtle">{t('v2.groups.count', { count: g.projects.length })}</span>
              <Button size="icon-sm" variant="ghost" disabled={i === 0} aria-label={t('v2.groups.moveUp', { name: g.name })} onClick={() => save(moveGroup(layout, g.id, -1))}>
                <ArrowUp />
              </Button>
              <Button size="icon-sm" variant="ghost" disabled={i === last} aria-label={t('v2.groups.moveDown', { name: g.name })} onClick={() => save(moveGroup(layout, g.id, 1))}>
                <ArrowDown />
              </Button>
              <Tip label={t('v2.groups.delete', { name: g.name })}>
                <Button size="icon-sm" variant="danger" aria-label={t('v2.groups.delete', { name: g.name })} onClick={() => save(removeGroup(layout, g.id))} data-v2-groups-delete={g.id}>
                  <Trash2 />
                </Button>
              </Tip>
            </div>
          ))}
        </div>
        <form
          className="flex gap-2 border-t border-line pt-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!name.trim()) return;
            save(addGroup(layout, newGroupId(), name));
            setName('');
          }}
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('v2.groups.placeholder')} aria-label={t('v2.groups.name')} className="h-8 text-[13px]" data-v2-groups-new />
          <Button size="sm" type="submit" disabled={!name.trim()} data-v2-groups-add>
            <Plus />
            {t('v2.groups.add')}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RenameField({ group, onRename }: { group: T.SidebarGroup; onRename: (name: string) => void }) {
  const t = useT();
  const [value, setValue] = useState(group.name);
  useEffect(() => setValue(group.name), [group.name]);
  const commit = () => {
    if (value.trim() && value.trim() !== group.name) onRename(value);
    else setValue(group.name);
  };
  return (
    <Input
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), commit())}
      aria-label={t('v2.groups.renameTitle', { name: group.name })}
      className="h-8 min-w-0 flex-1 text-[13px]"
      data-v2-groups-rename={group.id}
    />
  );
}

// GroupSwitcher is the chip beside the filters: All, or one group, which
// narrows the whole sidebar to its projects.
export function GroupSwitcher({ groups, value, onChange }: { groups: T.SidebarGroup[]; value: string; onChange: (id: string) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const chosen = groups.find((g) => g.id === value);
  const pick = (id: string) => {
    onChange(id);
    setOpen(false);
  };
  const option = (id: string, label: ReactNode) => (
    <button
      key={id || 'all'}
      type="button"
      aria-pressed={value === id || (!chosen && id === '')}
      onClick={() => pick(id)}
      className={cn('flex w-full min-w-0 items-center rounded-md px-2 py-1.5 text-left text-[12.5px] text-secondary hover:bg-surface-raised', (value === id || (!chosen && id === '')) && 'bg-surface-strong text-title')}
      data-v2-group-option={id || 'all'}
    >
      <span className="min-w-0 truncate">{label}</span>
    </button>
  );
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tip label={t('v2.groups.switcher')}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={t('v2.groups.switcher')}
            className={cn(
              'flex h-8 max-w-[96px] shrink-0 items-center gap-1 rounded-lg border px-2 text-[12px] transition',
              chosen ? 'border-line-heavy bg-surface-strong text-title' : 'border-line bg-surface-faint text-subtle hover:text-title',
            )}
            data-v2-group-switcher={chosen?.id ?? 'all'}
          >
            <Folders className="size-3.5 shrink-0" />
            <span className="min-w-0 truncate">{chosen ? chosen.name : t('v2.groups.all')}</span>
          </button>
        </PopoverTrigger>
      </Tip>
      <PopoverContent align="end" className="grid w-[200px] gap-0.5 p-1" data-v2-group-popover>
        {option('', t('v2.groups.all'))}
        {groups.map((g) => option(g.id, g.name))}
      </PopoverContent>
    </Popover>
  );
}

// GroupHeader is a group's header in the sidebar: a click folds it, and a
// right-click offers Collapse, Hide group (all its projects) and Rename.
export function GroupHeader({ group, count, collapsed, onCollapse, onHide, extra }: { group: T.SidebarGroup; count: number; collapsed: boolean; onCollapse: () => void; onHide: () => void; extra?: ReactNode }) {
  const t = useT();
  const { layout, save } = useSidebarLayout();
  const [renaming, setRenaming] = useState(false);
  return (
    <>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div className="flex h-7 items-center gap-1 px-1.5 pt-2" data-v2-group={group.id}>
            <button
              type="button"
              aria-expanded={!collapsed}
              onClick={onCollapse}
              className="flex min-w-0 items-center gap-1 rounded px-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle hover:text-title"
              data-v2-group-toggle={group.id}
            >
              <ChevronRight className={cn('size-3 shrink-0 transition-transform', !collapsed && 'rotate-90')} />
              <span className="min-w-0 truncate">{group.name}</span>
              {collapsed && <span className="font-normal normal-case tracking-normal tabular-nums text-faint">· {count}</span>}
            </button>
            {extra}
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent data-v2-group-menu={group.id}>
          <ContextMenuItem icon={ChevronRight} onSelect={onCollapse} data-v2-group-action="collapse">
            {t(collapsed ? 'v2.groups.expand' : 'v2.groups.collapse')}
          </ContextMenuItem>
          <ContextMenuItem icon={EyeOff} onSelect={onHide} disabled={group.projects.length === 0} data-v2-group-action="hide">
            {t('v2.groups.hideGroup')}
          </ContextMenuItem>
          <ContextMenuItem icon={Pencil} onSelect={() => setTimeout(() => setRenaming(true), 0)} data-v2-group-action="rename">
            {t('common.rename')}
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      <GroupNameDialog
        open={renaming}
        onOpenChange={setRenaming}
        title={t('v2.groups.renameTitle', { name: group.name })}
        initial={group.name}
        confirm={t('common.save')}
        onSubmit={(name) => save(renameGroup(layout, group.id, name))}
      />
    </>
  );
}
