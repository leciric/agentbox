import { useQuery } from '@tanstack/react-query';
import { Layers, LoaderCircle } from 'lucide-react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { percentOf } from '../lib/imageProgress';

// ImageWait is what an agent asked for before the base image was ready shows
// over its composer: it waits for the image, which is downloading or being
// built in the background, and its machine is made by itself once it's there
// (T.Agent.waitingForImage). The build's step and progress are the top bar's
// chip's, from the same query.
export function ImageWait({ agent }: { agent: T.Agent }) {
  const t = useT();
  const image = useQuery({
    queryKey: ['image'],
    queryFn: api.image,
    refetchInterval: (query) => (query.state.data?.build === 'building' ? 2_000 : 10_000),
    retry: false,
  });
  const data = image.data;
  const percent = percentOf(data?.progress);
  return (
    <div className="mb-2 grid gap-2 rounded-2xl border border-sky-400/25 bg-overlay px-4 py-3 shadow-lg backdrop-blur-xl" data-image-wait>
      <div className="flex items-start gap-2.5">
        <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin text-sky-400" />
        <div className="grid min-w-0 flex-1 gap-0.5">
          <span className="text-[13px] font-medium text-title">{t('agent.imageWait.title')}</span>
          <span className="text-[12px] leading-relaxed text-secondary">{t('agent.imageWait.body', { name: agent.name })}</span>
        </div>
      </div>
      {data?.build === 'building' && (data.step || data.progress) && (
        <div className="grid min-w-0 gap-1.5 pl-6.5">
          <span className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-muted">
            <Layers className="size-3 shrink-0" />
            <span className="truncate font-mono" title={data.step}>{data.step}</span>
            {data.progress && <span className="ml-auto shrink-0 font-mono tabular-nums">{data.progress}</span>}
          </span>
          {percent !== null && (
            <div className="h-1 overflow-hidden rounded-full bg-surface-raised" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} aria-label={t('agent.imageWait.title')}>
              <div className="h-full rounded-full bg-sky-400 transition-[width]" style={{ width: `${percent}%` }} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
