// Which of a chat's replies to read aloud, and how much of each: a pure
// follower of the thread, so it's tested without a chat or a voice.
import type * as T from '../../../shared/api';
import { SentenceFeed, type VoiceLanguage } from './speakable.ts';

// Only what the agent says to you: its assistant messages, not its thoughts,
// its tools, what AgentBox hides, or its subagents' words.
const spoken = (it: T.ChatItem) => it.kind === 'assistant' && !it.parent && !it.hidden;

export class ReplyTracker {
  private readonly language: VoiceLanguage;
  private readonly seen = new Map<string, SentenceFeed | null>();
  private primed = false;

  constructor(language: VoiceLanguage) {
    this.language = language;
  }

  // next takes the thread's items as they are now and returns the sentences
  // to say. What's there the first time has been said already, and a page of
  // older messages loaded in front isn't new either: an item is new when it
  // comes after one already seen, or the whole thread was replaced (a new chat).
  next(items: T.ChatItem[]): string[] {
    const out: string[] = [];
    const replaced = this.primed && !items.some((it) => this.seen.has(it.id));
    let afterSeen = false;
    for (const it of items) {
      const known = this.seen.has(it.id);
      if (!known) {
        const fresh = this.primed && (afterSeen || replaced);
        const text = it.text ?? '';
        this.seen.set(it.id, spoken(it) ? (fresh ? new SentenceFeed(this.language) : new SentenceFeed(this.language, text, !it.streaming)) : null);
      }
      afterSeen ||= known;
      const feed = this.seen.get(it.id);
      if (feed) out.push(...feed.next(it.text ?? '', !it.streaming));
    }
    this.primed = true;
    return out;
  }
}
