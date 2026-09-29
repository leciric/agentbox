// Reading a chat's replies aloud as they stream, a sentence at a time, as
// each one ends (tracker.ts says which), each reply in its own language's
// voice. Leaving the chat, or turning it off, stops it.
import { useEffect, useRef } from 'react';
import type * as T from '../../../shared/api';
import { speak, stop } from './reader.ts';
import { fallbackLanguage, useReadAloudSettings } from './settings.ts';
import { ReplyTracker } from './tracker.ts';

export function useReadAloud(ref: string, items: T.ChatItem[] | undefined): void {
  const { on } = useReadAloudSettings();
  const tracker = useRef<ReplyTracker | null>(null);

  // Turning it on, or off, or another chat: start over, with nothing queued.
  useEffect(() => {
    tracker.current = on ? new ReplyTracker(fallbackLanguage()) : null;
    return () => stop();
  }, [ref, on]);

  useEffect(() => {
    if (!items || !tracker.current) return;
    for (const { text, language } of tracker.current.next(items)) speak(text, language);
  }, [items, on, ref]);
}
