import { useEffect, useRef } from 'react';
import type * as T from '../../shared/api';
import { useReviewHead } from './inboxPages';
import { mayNotify } from './notifyPrefs';
import { batch, detectNewlyReady, markAllSeen, markUnseen, playReadySound, useReviewing } from './reviewAlert';

// useReadyAlerts watches Ready to review (every project's, the newest page of
// it: lib/inboxPages.ts) for agents that newly land in it, among them one that
// was reviewed and changed again: they get the "new" marks at once, and a
// chime and onReady (the OS notice) once for all that arrive within a moment.
// Mount it once, in the app. With reviewing turned off in Settings it says
// nothing, and what lands meanwhile isn't news when it's turned back on.
export function useReadyAlerts(onReady: (items: T.ReviewItem[]) => void) {
  const on = useReviewing();
  const list = useReviewHead(on);
  const known = useRef<Set<string> | null>(null);
  const onReadyRef = useRef(onReady);
  onReadyRef.current = onReady;
  const pending = useRef<ReturnType<typeof batch<T.ReviewItem>> | null>(null);
  pending.current ??= batch<T.ReviewItem>(1500, (items) => {
    if (mayNotify('review', 'sound')) playReadySound();
    onReadyRef.current(items);
  });
  // When it was last turned on: a list read before then is the baseline's
  // to set, not news.
  const onSince = useRef(0);
  useEffect(() => {
    if (!on) {
      known.current = null;
      onSince.current = Date.now();
      pending.current?.cancel();
      markAllSeen();
      return;
    }
    if (!list.data || list.dataUpdatedAt < onSince.current) return;
    const next = detectNewlyReady(known.current, list.data.items);
    known.current = next.known;
    markUnseen(next.fresh);
    pending.current?.add(next.fresh);
  }, [list.data, list.dataUpdatedAt, on]);
  useEffect(() => () => pending.current?.cancel(), []);
}
