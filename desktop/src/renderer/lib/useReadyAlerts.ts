import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import type * as T from '../../shared/api';
import { api } from './api';
import { reviewKey } from './review';
import { batch, detectNewlyReady, markUnseen, playReadySound, reviewSoundOn } from './reviewAlert';

// useReadyAlerts watches Ready to review (every project's) for agents that
// newly land in it: they get the "new" marks at once, and a chime and
// onReady (the OS notice) once for all that arrive within a moment. Mount it
// once, in the app.
export function useReadyAlerts(onReady: (items: T.ReviewItem[]) => void) {
  const list = useQuery({ queryKey: reviewKey(), queryFn: () => api.readyToReview(), refetchInterval: 60_000 });
  const known = useRef<Set<string> | null>(null);
  const onReadyRef = useRef(onReady);
  onReadyRef.current = onReady;
  const pending = useRef<ReturnType<typeof batch<T.ReviewItem>> | null>(null);
  pending.current ??= batch<T.ReviewItem>(1500, (items) => {
    if (reviewSoundOn()) playReadySound();
    onReadyRef.current(items);
  });
  useEffect(() => {
    if (!list.data) return;
    const next = detectNewlyReady(known.current, list.data);
    known.current = next.known;
    markUnseen(next.fresh);
    pending.current?.add(next.fresh);
  }, [list.data]);
  useEffect(() => () => pending.current?.cancel(), []);
}
