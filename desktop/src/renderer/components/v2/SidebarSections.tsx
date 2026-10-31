import { ChevronRight, GripVertical, MoreHorizontal, Pencil, Trash2 } from 'lucide-react';
import type { DragEvent, KeyboardEvent } from 'react';
import { useState } from 'react';
import type * as T from '../../../shared/api';
import { t as tNow, useT } from '../../lib/i18n';
import type { Dragging, SidebarList } from '../../lib/sidebar';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '../ui/dialog';
import { Field, Input } from '../ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';

// The sidebar's user sections: the keyboard and drag vocabulary, the header of
// a section, and the rows that name or rename one (components/v2/SidebarV2.tsx).

// Alt with an arrow moves the focused row, which is the keyboard's whole
// vocabulary here: down past the end of a list is the top of the next one,
// so the same two keys reorder a list and move a project between sections.
export const moveKeys = (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => {
  if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
  e.preventDefault();
  move(e.key === 'ArrowUp' ? -1 : 1);
};

// Drawn over the rows rather than between them: a line that took room would
// push the row under the pointer away, and the next dragover would aim
// somewhere else.
export const dropLine = (edge: 'before' | 'after', gap = false) => (
  <div className={cn('pointer-events-none absolute inset-x-2 z-10 h-0.5 rounded-full bg-brand-400', edge === 'before' ? (gap ? '-top-[3px]' : '-top-px') : gap ? 'bottom-px' : '-bottom-px')} />
);


// describeDelete says what deleting a section does, which is the one thing
// worth being clear about: it takes the section and keeps every project.
export function describeDelete(lists: SidebarList[], id: string | undefined): string {
  const n = lists.find((l) => l.section?.id === id)?.projects.length ?? 0;
  return tNow('shell.sidebar.deleteBody', { count: n });
}

export function SectionHeader({
  section,
  count,
  dragging,
  targeted,
  onToggle,
  onMove,
  onRename,
  onDelete,
  onDragStart,
  onDragEnd,
  dropZone,
  moveKeys,
}: {
  section: T.Section;
  count: number;
  dragging: Dragging | null;
  targeted: boolean;
  onToggle: () => void;
  onMove: (delta: -1 | 1) => void;
  onRename: () => void;
  onDelete: () => void;
  onDragStart: (e: DragEvent) => void;
  onDragEnd: (e: DragEvent) => void;
  // A project dropped on the header joins the section, at its top.
  dropZone: { onDragOver: (e: DragEvent) => void; onDrop: (e: DragEvent) => void };
  moveKeys: (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => void;
}) {
  const t = useT();
  return (
    <div
      className={cn(
        'group/section flex items-center gap-1 rounded-lg pr-1 transition-colors hover:bg-surface-faint',
        targeted && 'bg-brand-400/10 ring-1 ring-brand-400/50',
        dragging?.kind === 'section' && dragging.id === section.id && 'opacity-40',
      )}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      {...dropZone}
    >
      <GripVertical className="size-3 shrink-0 text-ghost opacity-0 transition group-hover/section:opacity-100" />
      <button
        className="flex min-w-0 flex-1 items-center gap-1 py-1 text-left text-caption font-semibold uppercase tracking-[0.06em] text-muted hover:text-primary"
        aria-expanded={!section.collapsed}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === 'ArrowLeft' && !section.collapsed) return onToggle();
          if (e.key === 'ArrowRight' && section.collapsed) return onToggle();
          moveKeys(onMove)(e);
        }}
        aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
      >
        <ChevronRight className={cn('size-3 shrink-0 text-faint transition-transform', !section.collapsed && 'rotate-90')} />
        <span className="truncate">{section.name}</span>
        <span className="ml-auto rounded-full px-1 text-caption tabular-nums text-faint">{count}</span>
      </button>
      <Menu>
        <MenuTrigger asChild>
          <button
            aria-label={t('shell.sidebar.sectionMenu', { name: section.name })}
            className="rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover/section:opacity-100"
          >
            <MoreHorizontal className="size-3.5" />
          </button>
        </MenuTrigger>
        <MenuContent>
          <MenuItem icon={Pencil} onSelect={onRename}>
            {t('common.rename')}
          </MenuItem>
          <MenuItem icon={ChevronRight} onSelect={onToggle}>
            {section.collapsed ? t('shell.sidebar.expand') : t('shell.sidebar.collapse')}
          </MenuItem>
          <MenuItem icon={Trash2} destructive onSelect={onDelete}>
            {t('shell.sidebar.deleteSection')}
          </MenuItem>
        </MenuContent>
      </Menu>
    </div>
  );
}

// NameSection is the row a new section is typed into, where it will appear:
// the section is made when you press Enter, so nothing empty is left behind by
// changing your mind.
export function NameSection({ onSave, onCancel }: { onSave: (name: string) => void; onCancel: () => void }) {
  const t = useT();
  return (
    <Input
      autoFocus
      maxLength={40}
      aria-label={t('shell.sidebar.newSectionName')}
      placeholder={t('shell.sidebar.sectionName')}
      className="mb-1 h-8 text-caption"
      onBlur={onCancel}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onCancel();
        if (e.key !== 'Enter') return;
        const name = e.currentTarget.value.trim();
        if (name) onSave(name);
        else onCancel();
      }}
    />
  );
}

// RenameSection is the section's name in a dialog, which is where the app puts
// anything it asks for a word.
export function RenameSection({ section, onSave, onClose }: { section: T.Section | undefined; onSave: (name: string) => void; onClose: () => void }) {
  const t = useT();
  const [name, setName] = useState(section?.name ?? '');
  if (!section) return null;
  const save = () => {
    const trimmed = name.trim();
    if (trimmed && trimmed !== section.name) onSave(trimmed);
    else onClose();
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{t('shell.sidebar.renameTitle', { name: section.name })}</DialogTitle>
        </DialogHeader>
        <Field label={t('shell.sidebar.sectionName')} htmlFor="section-name">
          <Input
            id="section-name"
            autoFocus
            maxLength={40}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && save()}
          />
        </Field>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={save}>{t('common.rename')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
