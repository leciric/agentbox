import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api } from '../lib/api';
import { diskCategoryLabel, diskItemLabel } from '../lib/diskUsage';
import { useT } from '../lib/i18n';
import { errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { Button } from './ui/button';
import { SettingRow } from './ui/settings';

// StorageUsage is Settings → Storage and machine's breakdown of what AgentBox
// takes on disk, measured while it's on screen (the daemon walks worktrees
// and asks Incus about every machine, so not on a poll), and in a VM the
// last trim, with a button that trims now.
export function StorageUsage({ inVM }: { inVM: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const usage = useQuery({ queryKey: ['diskUsage'], queryFn: api.diskUsage, refetchOnMount: 'always' });
  const trim = useMutation({
    mutationFn: api.trimDisk,
    onSuccess: (res) => {
      if (res.error) toast.error(t('storage.trimFailed', { error: res.error }));
      else toast.success(t('storage.trimDone', { bytes: humanBytes(res.bytes) }));
      void queryClient.invalidateQueries({ queryKey: ['diskUsage'] });
      void queryClient.invalidateQueries({ queryKey: ['usage'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const last = usage.data?.trim;
  return (
    <SettingRow
      label={t('settings.entry.disk-usage.label')}
      description={t('storage.description')}
      details={inVM ? t('storage.trimNote') : undefined}
      control={
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" disabled={usage.isFetching} onClick={() => void usage.refetch()}>
            {usage.isFetching ? t('storage.measuring') : t('storage.measure')}
          </Button>
          {inVM && (
            <Button variant="outline" size="sm" data-trim-disk disabled={trim.isPending} onClick={() => trim.mutate()}>
              {trim.isPending ? t('storage.trimming') : t('storage.trimNow')}
            </Button>
          )}
        </div>
      }
    >
      <div className="grid max-w-xl gap-3" data-storage-usage>
        {usage.isPending ? (
          <span className="text-caption text-muted">{t('storage.measuring')}</span>
        ) : usage.isError ? (
          <span className="text-caption text-rose-300">{errorMessage(usage.error)}</span>
        ) : (
          <>
            {(usage.data.categories ?? [])
              .filter((cat) => cat.bytes > 0)
              .map((cat) => (
                <details key={cat.kind || cat.label} className="group grid gap-1">
                  <summary className="flex cursor-pointer items-center justify-between gap-3 text-ui text-secondary">
                    <span className="min-w-0 truncate font-medium">{diskCategoryLabel(cat)}</span>
                    <span className="flex shrink-0 items-center gap-3">
                      <Share bytes={cat.bytes} total={usage.data.total} />
                      <span className="w-20 text-right font-mono tabular-nums text-tertiary">{humanBytes(cat.bytes)}</span>
                    </span>
                  </summary>
                  {cat.items && cat.items.length > 0 && (
                    <div className="mt-1 grid gap-0.5 border-l border-line pl-2.5">
                      {cat.items.map((item) => (
                        <div key={item.label} className="flex items-center justify-between gap-3 text-caption text-muted">
                          <span className="min-w-0 truncate">{diskItemLabel(item)}</span>
                          <span className="shrink-0 font-mono tabular-nums text-faint">{humanBytes(item.bytes)}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </details>
              ))}
            <div className="flex items-center justify-between border-t border-line pt-2 text-ui font-medium text-primary">
              <span>{t('storage.total')}</span>
              <span className="font-mono tabular-nums">{humanBytes(usage.data.total)}</span>
            </div>
            {inVM && (
              <span className="text-caption text-muted" data-last-trim>
                {last ? t('storage.lastTrim', { when: timeAgo(last.at), bytes: humanBytes(last.bytes) }) : t('storage.notTrimmedYet')}
              </span>
            )}
          </>
        )}
      </div>
    </SettingRow>
  );
}

// Share is a category's part of the total, as a bar.
function Share({ bytes, total }: { bytes: number; total: number }) {
  const pct = total > 0 ? Math.max(2, Math.round((bytes / total) * 100)) : 0;
  return (
    <span className="hidden h-1.5 w-24 overflow-hidden rounded-full bg-line sm:block" aria-hidden>
      <span className="block h-full rounded-full bg-brand-400" style={{ width: `${pct}%` }} />
    </span>
  );
}
