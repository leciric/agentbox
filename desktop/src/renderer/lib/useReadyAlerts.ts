import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import type * as T from '../../shared/api';
import { api } from './api';
import { mayNotify } from './notifyPrefs';
import { reviewKey } from './review';
import { batch, detectNewlyReady, markAllSeen, markUnseen, playReadySound, useReviewing } from './reviewAlert';

// useReadyAlerts watches Ready to review (every project's) for agents that
// newly land in it: they get the "new" marks at once, and a chime and
// onReady (the OS notice) once for all that arrive within a moment. Mount it
// once, in the app. With reviewing turned off in Settings it says nothing,
// and what lands meanwhile isn't news when it's turned back on.
export function useReadyAlerts(onReady: (items: T.ReviewItem[]) => void) {
  const on = useReviewing();
  const list = useQuery({ queryKey: reviewKey(), queryFn: () => api.readyToReview(), refetchInterval: 60_000, enabled: on });
  const known = useRef<Set<string> | null>(null);
  const onReadyRef = useRef(onReady);
  onReadyRef.current = onReady;
  const pending = useRef<ReturnType<typeof batch<T.ReviewItem>> | null>(null);
  pending.current ??= batch<T.ReviewItem>(1500, (items) => {
    if (mayNotify('finished', 'sound')) playReadySound();
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
    const next = detectNewlyReady(known.current, list.data);
    known.current = next.known;
    markUnseen(next.fresh);
    pending.current?.add(next.fresh);
  }, [list.data, list.dataUpdatedAt, on]);
  useEffect(() => () => pending.current?.cancel(), []);
}
