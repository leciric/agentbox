// The phone's half of the shared i18n (src/shared/i18n.ts), after the
// desktop's renderer/lib/i18n.tsx: a hook that re-renders when the language
// changes, and rich messages whose tags become React Native nodes.
//
// The language is the computer's (Settings.language), as on the web phone
// app; until it is read, the phone's own (expo-localization).
import { Fragment, isValidElement, useSyncExternalStore, type ReactNode } from 'react';
import { getLocales } from 'expo-localization';
import { language, setLanguage, subscribe, t, tParts, type MessageKey } from '../shared/i18n.ts';

export { formatDateTime, formatNumber, formatTime, t, type MessageKey } from '../shared/i18n.ts';

export type RichValues = Record<string, ReactNode | ((chunk: ReactNode) => ReactNode)>;
export type Translate = typeof t & { rich: (key: MessageKey, values?: RichValues) => ReactNode; lang: string };

function nodes(parts: unknown[]): ReactNode {
  if (parts.every((p) => typeof p === 'string')) return parts.join('');
  return parts.map((p, i) => <Fragment key={i}>{isValidElement(p) || typeof p !== 'object' || p === null ? (p as ReactNode) : String(p)}</Fragment>);
}

function rich(key: MessageKey, values?: RichValues): ReactNode {
  const wrapped: Record<string, unknown> = {};
  for (const [name, value] of Object.entries(values ?? {})) {
    wrapped[name] = typeof value === 'function' ? (chunk: unknown[]) => value(nodes(chunk)) : value;
  }
  return nodes(tParts(key, wrapped));
}

const translators = new Map<string, Translate>();

export function useT(): Translate {
  const lang = useSyncExternalStore(subscribe, language, language);
  let fn = translators.get(lang);
  if (!fn) {
    fn = Object.assign((key: MessageKey, values?: Parameters<typeof t>[1]) => t(key, values), { rich, lang });
    translators.set(lang, fn);
  }
  return fn;
}

// speakPhoneLanguage speaks the phone's language until the computer's is known.
export function speakPhoneLanguage(): void {
  const tag = getLocales()[0]?.languageTag;
  if (tag) setLanguage(tag);
}

export { setLanguage };
