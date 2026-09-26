import { Markdown } from './chat/Markdown';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './ui/dialog';
import { whatsNewFrom } from '../lib/whatsnew';

export function WhatsNewDialog({ open, onOpenChange, version }: { open: boolean; onOpenChange: (open: boolean) => void; version: string }) {
  const sections = whatsNewFrom(version);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>What's new</DialogTitle>
          <DialogDescription>AgentBox {version}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-6">
          {sections.length === 0 ? (
            <p className="text-[13px] text-subtle">Nothing to show yet.</p>
          ) : (
            sections.map((section) => (
              <div key={section.version}>
                <h3 className="mb-1 font-mono text-[12px] font-semibold text-faint">{section.version}</h3>
                <Markdown text={section.body} />
              </div>
            ))
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
