import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';

// UsageSentDialog is "See what's sent": the anonymous usage stats' two
// requests exactly as the next update check would post them
// (GET /v1/usage-stats/pending), read afresh each time it opens.
export function UsageSentDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const pending = useQuery({
    queryKey: ['usage-stats-pending'],
    queryFn: api.usageStatsPending,
    enabled: open,
    staleTime: 0,
    gcTime: 0,
  });
  const p = pending.data;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl" data-usage-sent>
        <DialogHeader>
          <DialogTitle>{t('settings.usageStats.sentTitle')}</DialogTitle>
          <DialogDescription>{t('settings.usageStats.sentDescription')}</DialogDescription>
        </DialogHeader>
        {pending.isPending && <p className="text-sm text-secondary">{t('common.loading')}</p>}
        {pending.error && <Notice>{errorMessage(pending.error)}</Notice>}
        {p && !p.on && <Notice>{t('settings.usageStats.sentOff')}</Notice>}
        {p?.on && !p.usage && !p.events && <p className="text-sm text-secondary">{t('settings.usageStats.sentNothing')}</p>}
        {p?.on && p.usage && <Payload title={t('settings.usageStats.sentCounts', { url: p.usageUrl })} body={p.usage} />}
        {p?.on && p.events && (
          <Payload
            title={t('settings.usageStats.sentEvents', { url: p.eventsUrl })}
            body={p.events}
            note={p.eventsWaiting > 500 ? t('settings.usageStats.sentMore', { count: p.eventsWaiting }) : undefined}
          />
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Payload({ title, body, note }: { title: string; body: string; note?: string }) {
  return (
    <section className="min-w-0 space-y-1">
      <h3 className="break-all font-mono text-xs text-secondary">{title}</h3>
      <pre className="max-h-72 min-w-0 overflow-auto whitespace-pre-wrap [overflow-wrap:anywhere] rounded border border-line bg-sunken px-3 py-2 font-mono text-[11px] leading-relaxed text-secondary">
        {body}
      </pre>
      {note && <p className="text-xs text-secondary">{note}</p>}
    </section>
  );
}
