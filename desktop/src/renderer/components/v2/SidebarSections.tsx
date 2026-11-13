import { Check, ChevronRight, Folder, GripVertical, MoreHorizontal, Palette, Pencil, Trash2 } from 'lucide-react';
import type { DragEvent, KeyboardEvent, ReactElement } from 'react';
import { useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { t as tNow, useT } from '../../lib/i18n';
import { nextRadio, sectionChanges } from '../../lib/sectionEdit';
import { SESSION_COLORS } from '../../lib/sessions';
import type { Dragging, SidebarList } from '../../lib/sidebar';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '../ui/dialog';
import { Field, Input } from '../ui/input';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuSub, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';

// The sidebar's user sections, which the app calls folders: the keyboard and
// drag vocabulary, the header of a section, and the rows that name or edit one
// (components/v2/SidebarV2.tsx).

// SECTION_COLORS are the colours a section can have (state.SectionColors):
// a chat session's palette, drawn by styles.css' [data-session-color].
export const SECTION_COLORS = SESSION_COLORS;

// FolderIcon is a section's folder, in its colour, or quiet with none.
export function FolderIcon({ color, className }: { color: string; className?: string }) {
  return (
    <span data-session-color={color || undefined} className="inline-flex shrink-0">
      <Folder
        aria-hidden
        className={cn('size-3', !color && 'text-faint', className)}
        style={color ? { color: 'var(--session)', fill: 'color-mix(in oklab, var(--session) 30%, transparent)' } : undefined}
      />
    </span>
  );
}

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
  onColor,
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
  onColor: (color: string) => void;
  onDelete: () => void;
  onDragStart: (e: DragEvent) => void;
  onDragEnd: (e: DragEvent) => void;
  // A project dropped on the header joins the section, at its top.
  dropZone: { onDragOver: (e: DragEvent) => void; onDrop: (e: DragEvent) => void };
  moveKeys: (move: (delta: -1 | 1) => void) => (e: KeyboardEvent) => void;
}) {
  const t = useT();
  // A description is the folder's tooltip; a folder with none has no tooltip,
  // its name being all there is to say. It opens above the row: to the right
  // it would sit on the ⋯, and the menu couldn't be reached while it shows.
  const withTip = (button: ReactElement) =>
    section.description ? (
      <Tip label={section.description} side="top" className="max-w-64 whitespace-normal">
        {button}
      </Tip>
    ) : (
      button
    );
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
      {withTip(
      <button
        data-v2-section-toggle
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
        <FolderIcon color={section.color} />
        <span className="truncate">{section.name}</span>
        <span className="ml-auto rounded-full px-1 text-caption tabular-nums text-faint">{count}</span>
      </button>,
      )}
      <Menu>
        <MenuTrigger asChild>
          <button
            aria-label={t('shell.sidebar.sectionMenu', { name: section.name })}
            className="rounded-md p-1 text-subtle opacity-0 transition hover:bg-surface-strong hover:text-primary focus-visible:opacity-100 group-hover/section:opacity-100"
          >
            <MoreHorizontal className="size-3.5" />
          </button>
        </MenuTrigger>
        <MenuContent className="max-w-72" data-v2-section-menu>
          <MenuLabel>
            <span className="flex items-center gap-1.5 text-primary">
              <FolderIcon color={section.color} className="size-3.5" />
              <span className="truncate">{section.name}</span>
            </span>
            {section.description && <span className="mt-0.5 block whitespace-normal font-normal text-subtle" data-v2-section-description>{section.description}</span>}
          </MenuLabel>
          <MenuSeparator />
          <MenuItem icon={Pencil} onSelect={onRename} data-v2-edit-section-item>
            {t('shell.sidebar.editSection')}
          </MenuItem>
          <MenuSub data-v2-section-colour-menu icon={Palette} label={t('shell.sidebar.color')} value={section.color ? t('shell.sidebar.colorName', { color: section.color }) : t('shell.sidebar.colorNone')}>
            <ColorItems value={section.color} onPick={onColor} />
          </MenuSub>
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

// ColorItems are a colour submenu's items: no colour, then the palette.
function ColorItems({ value, onPick }: { value: string; onPick: (color: string) => void }) {
  const t = useT();
  return (
    <>
      {['', ...SECTION_COLORS].map((c) => (
        <MenuItem key={c || 'none'} onSelect={() => onPick(c)} hint={value === c ? <Check className="size-3.5" /> : undefined} data-v2-section-color={c || 'none'}>
          <span className="flex items-center gap-2">
            <Swatch color={c} />
            {c ? t('shell.sidebar.colorName', { color: c }) : t('shell.sidebar.colorNone')}
          </span>
        </MenuItem>
      ))}
    </>
  );
}

// Swatch is one colour's dot, or an empty ring for none.
function Swatch({ color }: { color: string }) {
  return (
    <span data-session-color={color || undefined} className="inline-flex">
      <span aria-hidden className={cn('size-3 rounded-full', !color && 'border border-line-strong')} style={color ? { backgroundColor: 'var(--session)' } : undefined} />
    </span>
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

// EditSection is a section's name, description and colour in a dialog, which
// is where the app puts anything it asks for a word.
export function EditSection({ section, onSave, onClose }: { section: T.Section | undefined; onSave: (req: T.UpdateSectionRequest) => void; onClose: () => void }) {
  if (!section) return null;
  return <EditSectionDialog section={section} onSave={onSave} onClose={onClose} />;
}

function EditSectionDialog({ section, onSave, onClose }: { section: T.Section; onSave: (req: T.UpdateSectionRequest) => void; onClose: () => void }) {
  const t = useT();
  // What the dialog opened with. The section prop is live and may refresh while
  // this is open; comparing against it would send a field the user never touched
  // and overwrite a change made meanwhile, so the changes are measured from here.
  const [opened] = useState(() => ({ name: section.name, description: section.description, color: section.color }));
  const [name, setName] = useState(opened.name);
  const [description, setDescription] = useState(opened.description);
  const [color, setColor] = useState(opened.color);
  const radios = useRef<(HTMLButtonElement | null)[]>([]);
  const colors: string[] = ['', ...SECTION_COLORS];
  // The tab stop is the chosen colour, or the first when it's one the palette lost.
  const stop = colors.includes(color) ? color : '';
  const save = () => {
    const req = sectionChanges(opened, { name, description, color });
    if (Object.keys(req).length) onSave(req);
    else onClose();
  };
  // A radio group is one Tab stop: the chosen colour holds it and the arrows
  // move within, selecting as they go.
  const pickWithKeys = (e: KeyboardEvent<HTMLButtonElement>, i: number) => {
    const next = nextRadio(e.key, i, colors.length);
    if (next === undefined) return;
    e.preventDefault();
    setColor(colors[next]!);
    radios.current[next]?.focus();
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-sm" data-v2-edit-section>
        <DialogHeader>
          <DialogTitle>{t('shell.sidebar.editTitle', { name: section.name })}</DialogTitle>
        </DialogHeader>
        <Field label={t('shell.sidebar.sectionName')} htmlFor="section-name">
          <Input id="section-name" autoFocus autoComplete="off" maxLength={40} value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && save()} />
        </Field>
        <Field label={t('shell.sidebar.sectionDescription')} htmlFor="section-description">
          <Input
            id="section-description"
            // Off, or the browser's suggestions drop over the colours below.
            autoComplete="off"
            maxLength={200}
            value={description}
            placeholder={t('shell.sidebar.sectionDescriptionHint')}
            onChange={(e) => setDescription(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && save()}
          />
        </Field>
        <div role="radiogroup" aria-label={t('shell.sidebar.color')} className="flex flex-col gap-1.5">
          <span className="text-caption font-medium text-muted">{t('shell.sidebar.color')}</span>
          <div className="flex flex-wrap gap-1.5">
            {colors.map((c, i) => (
              <Tip key={c || 'none'} label={c ? t('shell.sidebar.colorName', { color: c }) : t('shell.sidebar.colorNone')} side="top">
                <button
                  ref={(el) => {
                    radios.current[i] = el;
                  }}
                  type="button"
                  role="radio"
                  aria-checked={color === c}
                  tabIndex={stop === c ? 0 : -1}
                  onKeyDown={(e) => pickWithKeys(e, i)}
                  aria-label={c ? t('shell.sidebar.colorName', { color: c }) : t('shell.sidebar.colorNone')}
                  onClick={() => setColor(c)}
                  data-v2-pick-color={c || 'none'}
                  className={cn('flex size-7 items-center justify-center rounded-full border border-transparent transition hover:bg-surface-strong', color === c && 'border-brand-400 bg-surface-raised')}
                >
                  <Swatch color={c} />
                </button>
              </Tip>
            ))}
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={save}>{t('common.save')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
