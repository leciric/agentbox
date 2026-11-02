import { Markdown } from './chat/Markdown';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './ui/dialog';
import { useT } from '../lib/i18n';
import { whatsNewFrom } from '../lib/whatsnew';
import { revealFeature, usePendingFeatures } from './FeatureDot';

export function WhatsNewDialog({ open, onOpenChange, version }: { open: boolean; onOpenChange: (open: boolean) => void; version: string }) {
  const t = useT();
  const sections = whatsNewFrom(version);
  // The new features not used yet, each with a dot where it lives: a row
  // here closes the dialog and points at that dot.
  const marked = usePendingFeatures();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t('shell.whatsNew.title')}</DialogTitle>
          <DialogDescription>{t('shell.whatsNew.version', { version })}</DialogDescription>
        </DialogHeader>
        {marked.length > 0 && (
          <div className="rounded-lg border border-line bg-surface-faint px-3 py-2.5" data-whats-new-dots>
            <p className="flex items-center gap-2 text-[13px] text-secondary">
              <span className="size-2 shrink-0 rounded-full bg-brand-400" />
              {t('featureDots.whatsNew', { count: marked.length })}
            </p>
            <ul className="mt-1.5 grid gap-0.5">
              {marked.map((f) => (
                <li key={f.id}>
                  <button
                    type="button"
                    className="w-full rounded-md px-2 py-1 text-left text-[12.5px] text-tertiary hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
                    onClick={() => {
                      onOpenChange(false);
                      revealFeature(f);
                    }}
                    data-whats-new-dot={f.id}
                  >
                    {t(f.hint)}
                    <span className="ml-1.5 font-mono text-[11px] text-faint">{f.version}</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
        <div className="grid gap-6">
          {sections.length === 0 ? (
            <p className="text-ui text-subtle">{t('shell.whatsNew.empty')}</p>
          ) : (
            sections.map((section) => (
              <div key={section.version}>
                <h3 className="mb-1 font-mono text-caption font-semibold text-faint">{section.version}</h3>
                <Markdown text={section.body} />
              </div>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
