// ShortcutSheet is v2's list of keyboard shortcuts, Ctrl+/ (⌘/ on a Mac)
// from anywhere: lib/shortcuts.ts has the list.
import { useT } from '../../lib/i18n';
import { setSheetOpen, sheet, useSheetOpen } from '../../lib/shortcuts';
import { onMac, shortcut } from '../../lib/utils';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../ui/dialog';

const keyName = (k: string) => (k === 'Mod' ? (onMac ? '⌘' : 'Ctrl') : k === 'Shift' && onMac ? '⇧' : k);

export function ShortcutSheet() {
  const t = useT();
  const open = useSheetOpen();
  return (
    <Dialog open={open} onOpenChange={setSheetOpen}>
      <DialogContent className="max-w-2xl" data-shortcut-sheet>
        <DialogHeader>
          <DialogTitle>{t('keys.title')}</DialogTitle>
          <DialogDescription>{t('keys.description', { keys: shortcut('/') })}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-x-8 gap-y-5 sm:grid-cols-2">
          {sheet.map((group) => (
            <section key={group.title} className="min-w-0">
              <h3 className="mb-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle">{t(group.title)}</h3>
              <dl className="grid gap-1.5">
                {group.rows.map((row) => (
                  <div key={row.label} className="flex items-center justify-between gap-3 text-[13px]">
                    <dt className="min-w-0 text-secondary">{t(row.label)}</dt>
                    <dd className="flex shrink-0 items-center gap-1">
                      {row.keys.map((k) => (
                        <kbd key={k} className="min-w-6 rounded-md border border-line-strong bg-surface-raised px-1.5 py-0.5 text-center font-mono text-[11px] text-primary">
                          {keyName(k)}
                        </kbd>
                      ))}
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
