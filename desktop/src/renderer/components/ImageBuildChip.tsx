import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Layers, LoaderCircle, RefreshCw, TriangleAlert } from 'lucide-react';
import { useEffect, useRef } from 'react';
import { toast } from 'sonner';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { JobProgress } from './JobProgress';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// ImageBuildChip is the base image build in the top bar, wherever you are:
// it runs in the background while you use the app, and what needs the image —
// making an agent — waits for it. A sky chip while it builds, with its latest
// step and how many jobs wait for it; a rose one with a retry once it failed;
// nothing otherwise. Its log is one click away, in the popover.
export function ImageBuildChip({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const image = useQuery({
    queryKey: ['image'],
    queryFn: api.image,
    refetchInterval: (query) => (query.state.data?.build === 'building' ? 2_000 : 10_000),
    retry: false,
  });
  const retry = useMutation({
    mutationFn: () => api.buildImage(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['image'] });
      void queryClient.invalidateQueries({ queryKey: ['setup'] });
    },
  });

  // Say so once when a build you watched here finishes.
  const was = useRef<string | undefined>(undefined);
  const build = image.data?.build;
  useEffect(() => {
    if (was.current === 'building' && build === 'idle' && image.data?.ready) {
      toast.success(t('shell.imageChip.readyToast'));
      void queryClient.invalidateQueries({ queryKey: ['setup'] });
    }
    if (was.current === 'building' && build === 'failed') void queryClient.invalidateQueries({ queryKey: ['setup'] });
    was.current = build;
  }, [build, image.data?.ready, queryClient, t]);

  const data = image.data;
  if (!data || !data.job || (data.build !== 'building' && data.build !== 'failed')) return null;
  const failed = data.build === 'failed';
  const label = failed ? t('shell.imageChip.failed') : data.ready ? t('shell.imageChip.rebuilding') : t('shell.imageChip.building');
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex min-w-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ring-1 ring-inset transition',
            failed
              ? 'bg-rose-400/10 text-rose-200 ring-rose-400/30 hover:bg-rose-400/15'
              : 'bg-sky-400/10 text-sky-200 ring-sky-400/25 hover:bg-sky-400/15',
          )}
          aria-label={label}
          data-image-build={data.build}
        >
          {failed ? <TriangleAlert className="size-3.5 shrink-0" /> : <LoaderCircle className="size-3.5 shrink-0 animate-spin" />}
          <span className="hidden sm:inline">{label}</span>
          {!failed && data.waiting > 0 && (
            <span className="rounded-full bg-sky-400/20 px-1.5 text-[10.5px] tabular-nums">{data.waiting}</span>
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[34rem] max-w-[calc(100vw-2rem)]">
        <div className="grid gap-3">
          <div className="grid gap-1">
            <span className={cn('flex items-center gap-1.5 text-[13px] font-medium', failed ? 'text-rose-200' : 'text-sky-200')}>
              <Layers className="size-3.5" />
              {label}
            </span>
            <span className="text-[12px] leading-relaxed text-secondary">
              {failed
                ? data.ready
                  ? t('shell.imageChip.failedKeeps')
                  : t('shell.imageChip.failedBody')
                : data.ready
                  ? t('shell.imageChip.rebuildingBody')
                  : t('shell.imageChip.buildingBody')}
            </span>
            {!failed && data.step && <span className="truncate font-mono text-[11.5px] text-muted" title={data.step}>{data.step}</span>}
            {!failed && data.waiting > 0 && (
              <span className="text-[12px] text-secondary">{t('shell.imageChip.waiting', { count: data.waiting })}</span>
            )}
          </div>
          <JobProgress key={data.job} jobId={data.job} header={false} logClassName="h-56" />
          {retry.error && <Notice>{errorMessage(retry.error)}</Notice>}
          <div className="flex items-center justify-between gap-3 border-t border-line pt-2.5">
            <button
              type="button"
              className="rounded-md px-2 py-1 text-[12px] font-medium text-brand-300 transition hover:bg-surface-raised"
              onClick={() => onSelect({ kind: 'jobs' })}
            >
              {t('shell.imageChip.allJobs')}
            </button>
            {failed && (
              <Button size="sm" variant="primary" disabled={retry.isPending} onClick={() => retry.mutate()}>
                {retry.isPending ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
                {t('shell.imageChip.retry')}
              </Button>
            )}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
