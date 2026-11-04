import { LoaderCircle, TriangleAlert } from 'lucide-react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { needsYouCount, sessionStatus } from '../../lib/sessions';
import { cn } from '../../lib/utils';

// SessionStatusMark is where a chat session stands, in one mark before its
// name, as the tabs above the chat and the sidebar's session headers show it:
// an amber count of what waits on you, a red warning when its chat stopped or
// stalled, a spinner while its lead works, and otherwise a dot when it said
// something you haven't seen (unread, which the caller works out: the
// session on screen is never unread).
export function SessionStatusMark({ session, unread, className }: { session: T.LeadSession; unread: boolean; className?: string }) {
  const t = useT();
  const status = sessionStatus(session);
  if (status === 'needsYou') {
    const n = needsYouCount(session);
    return (
      <span
        role="img"
        aria-label={t('sessions.needsYou', { count: n })}
        title={t('sessions.needsYou', { count: n })}
        data-session-status="needsYou"
        className={cn('flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-amber-400 px-1 text-[10px] font-semibold tabular-nums leading-none text-black', className)}
      >
        {n}
      </span>
    );
  }
  if (status === 'failed') {
    return <TriangleAlert role="img" aria-label={t('sessions.failed')} data-session-status="failed" className={cn('size-3 shrink-0 text-rose-300', className)} />;
  }
  if (status === 'working') {
    return <LoaderCircle role="img" aria-label={t('sessions.working')} data-session-status="working" className={cn('size-3 shrink-0 animate-spin text-brand-300', className)} />;
  }
  if (unread) {
    return <span role="img" aria-label={t('sessions.unread')} data-session-status="unread" className={cn('size-1.5 shrink-0 rounded-full bg-brand-400', className)} />;
  }
  return null;
}
