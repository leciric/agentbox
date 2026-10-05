// The language the app speaks, and its messages. One language at a time for
// the whole app: the renderer sets it from the daemon's Settings.language and
// re-renders (renderer/lib/i18n.tsx), and the main process is told it for its
// own few dialogs. Dates and numbers are written the language's way too.
//
// en-US is the source: its catalog has every key, and another language's lacking
// one shows en-US's rather than a key. The Go side — the CLI, the brief, the
// agents' chats — stays in English.
import { enUS, type MessageKey, type Messages } from './en-US.ts';
import { format, formatNumberIn, formatParts, type Values } from './format.ts';
import { languages, type Language } from './languages.ts';

export type { MessageKey, Messages, Values, Language };
export { languages };

export const defaultLanguage = 'en-US';

let current: Language = languages[0];
const listeners = new Set<() => void>();

// resolveLanguage is the language on the list for a tag: the tag itself, else
// one with its primary language ("pt-PT" gets pt-BR), else en-US.
export function resolveLanguage(tag: string | null | undefined): Language {
  if (!tag) return languages[0];
  const lower = tag.toLowerCase();
  return (
    languages.find((l) => l.tag.toLowerCase() === lower) ??
    languages.find((l) => l.tag.split('-')[0].toLowerCase() === lower.split('-')[0]) ??
    languages[0]
  );
}

// language is the tag of the language the app speaks now.
export function language(): string {
  return current.tag;
}

// setLanguage switches the app to a language, telling every subscriber.
export function setLanguage(tag: string | null | undefined): void {
  const next = resolveLanguage(tag);
  if (next === current) return;
  current = next;
  for (const listener of listeners) listener();
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function message(key: MessageKey): string {
  return current.messages[key] ?? enUS[key] ?? key;
}

// t is a message in the current language, with its values put in.
export function t(key: MessageKey, values?: Values): string {
  return format(message(key), values, current.tag);
}

// tParts is t before the parts are joined, for values that aren't text.
export function tParts(key: MessageKey, values?: Values): unknown[] {
  return formatParts(message(key), values, current.tag);
}

export function formatNumber(n: number, options?: Intl.NumberFormatOptions): string {
  return formatNumberIn(current.tag, n, options);
}

type When = Date | string | number;

export function formatDate(when: When, options: Intl.DateTimeFormatOptions = { dateStyle: 'medium' }): string {
  return new Intl.DateTimeFormat(current.tag, options).format(new Date(when));
}

export function formatTime(when: When, options: Intl.DateTimeFormatOptions = { timeStyle: 'short' }): string {
  return new Intl.DateTimeFormat(current.tag, options).format(new Date(when));
}

export function formatDateTime(when: When, options: Intl.DateTimeFormatOptions = { dateStyle: 'medium', timeStyle: 'short' }): string {
  return new Intl.DateTimeFormat(current.tag, options).format(new Date(when));
}

// formatList joins items the language's way: "a, b and c", "a, b e c".
export function formatList(items: string[], type: Intl.ListFormatType = 'conjunction'): string {
  return new Intl.ListFormat(current.tag, { style: 'long', type }).format(items);
}
