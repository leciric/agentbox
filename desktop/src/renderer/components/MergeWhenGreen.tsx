// "Resolved — merge when CI is green" (internal/daemon/mergewhengreen.go):
// what an item shows of its pull request's mark, waiting or given up, and
// the one way out of each, Cancel or Dismiss. The button itself is in the
// review's footer (ReadyToReview).
import { useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { CircleAlert, Clock } from 'lucide-react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { refreshInbox } from '../lib/inboxPages';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { Badge } from './ui/badge';
import { Button } from './ui/button';

// cancelMergeWhenGreen takes a mark back, or dismisses a given-up one: the
// daemon returns the agent to the review either way.
export function cancelMergeWhenGreen(queryClient: QueryClient, project: string, number: number, t: ReturnType<typeof useT>, quiet = false) {
  return api
    .cancelMergeWhenGreen(project, number)
    .then(() => !quiet && toast(t('review.mergeWhenGreen.cancelled', { number })))
    .catch((err) => toast.error(errorMessage(err)))
    .finally(() => refreshInbox(queryClient));
}

// waitText is what a pending mark waits on now, in words: checks running,
// the agent fixing or rebasing, with the failing checks or GitHub's reason.
// Undefined before the daemon's first look.
export function waitText(mark: T.MergeWhenGreen, t: ReturnType<typeof useT>): string | undefined {
  const words: Record<string, string> = {
    checks: t('mergeWhenGreen.wait.checks'),
    failing: t('mergeWhenGreen.wait.failing'),
    conflict: t('mergeWhenGreen.wait.conflict'),
    draft: t('mergeWhenGreen.wait.draft'),
    retry: t('mergeWhenGreen.wait.retry'),
  };
  const word = mark.waiting ? words[mark.waiting] : undefined;
  if (!word) return undefined;
  return mark.detail && mark.waiting !== 'conflict' ? `${word} (${mark.detail})` : word;
}

// MergeWhenGreenNote is the strip over the review's footer while an item's
// pull request waits to merge, and on what, or why it didn't.
export function MergeWhenGreenNote({ item, className }: { item: T.ReviewItem; className?: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const mark = item.mergeWhenGreen;
  const cancel = useMutation({ mutationFn: (n: number) => cancelMergeWhenGreen(queryClient, item.project, n, t, !!mark?.error) });
  if (!mark) return null;
  const failed = !!mark.error;
  const wait = failed ? undefined : waitText(mark, t);
  const text = failed
    ? t('review.mergeWhenGreen.notMerged', { error: mark.error ?? '' })
    : wait
      ? t('review.mergeWhenGreen.waiting', { number: mark.number, wait })
      : t('review.mergeWhenGreen.pending', { number: mark.number });
  return (
    <div className={cn('flex min-w-0 items-center gap-2 border-t border-line px-4 py-2 text-caption', failed ? 'text-rose-300' : 'text-amber-200', className)} data-merge-when-green={failed ? 'failed' : 'pending'}>
      {failed ? <CircleAlert className="size-3.5 shrink-0" /> : <Clock className="size-3.5 shrink-0" />}
      <span className="min-w-0 flex-1 truncate" title={text} data-merge-when-green-waiting={mark.waiting}>
        {text}
      </span>
      <Button size="sm" variant="ghost" className="h-6 shrink-0" disabled={cancel.isPending} onClick={() => cancel.mutate(mark.number)} data-merge-when-green-cancel>
        {t(failed ? 'review.mergeWhenGreen.dismiss' : 'review.mergeWhenGreen.cancel')}
      </Button>
    </div>
  );
}

// MergeWhenGreenBadge is the same on a Work page row: what it waits on
// beside it, the reason it was given up on hover.
export function MergeWhenGreenBadge({ mark }: { mark?: T.MergeWhenGreen }) {
  const t = useT();
  if (!mark) return null;
  const wait = mark.error ? undefined : waitText(mark, t);
  return (
    <>
      <Badge variant={mark.error ? 'danger' : 'warning'} className="shrink-0" title={mark.error ?? wait} data-work-merge-when-green={mark.error ? 'failed' : 'pending'}>
        {t(mark.error ? 'work.mergeWhenGreen.failed' : 'work.mergeWhenGreen.pending')}
      </Badge>
      {wait && (
        <span className="min-w-0 truncate text-caption text-amber-200" title={wait} data-work-merge-when-green-waiting={mark.waiting}>
          {wait}
        </span>
      )}
    </>
  );
}
