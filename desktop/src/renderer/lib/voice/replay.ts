// Reading one reply aloud again, from the start, when its speaker is clicked
// under it in the chat. Whatever is being read stops first, so the replay
// never waits behind the live stream; clicked again while it reads, it stops.
import { detectLanguage } from './language.ts';
import type { ReaderState, speak, stop } from './reader.ts';
import { sentences, speakableText, type VoiceLanguage } from './speakable.ts';
import type { Utterance } from './tracker.ts';

// The reader replay drives: reader.ts's, or a test's.
export type Reader = { speak: typeof speak; stop: typeof stop };

// replayUtterances is what reading a reply says: all of it, in the voice of
// the language it's in, as the live stream would have read it.
export function replayUtterances(markdown: string, fallback: VoiceLanguage): Utterance[] {
  const language = detectLanguage(markdown, fallback);
  return sentences(speakableText(markdown, language)).map((text) => ({ text, language }));
}

// replaying says whether the reader is reading reply id, for a replay.
export function replaying(state: ReaderState, id: string): boolean {
  return state.status !== 'idle' && state.owner === id;
}

// toggleReplay stops the reader, and reads reply id from the start unless it
// was what it read.
export function toggleReplay(reader: Reader, state: ReaderState, id: string, markdown: string, fallback: VoiceLanguage): void {
  const again = !replaying(state, id);
  reader.stop();
  if (!again) return;
  for (const { text, language } of replayUtterances(markdown, fallback)) reader.speak(text, language, id);
}
