// The renderer's half of shared/i18n: the hooks that make a component speak
// the current language and re-render when it changes, and rich messages whose
// values and tags are React nodes.
//
//   const t = useT();
//   t('sidebar.newAgent')                                  // a string
//   t.rich('settings.cliHint', { code: (c) => <code>{c}</code> })   // a node
//
// useT's t changes identity with the language, so a useMemo or useCallback that
// lists it recomputes. Code that isn't a component (a toast in a callback, a
// helper that formats a status) imports t directly: it reads the language at
// the moment it's called.
import { Fragment, isValidElement, useSyncExternalStore, type ReactNode } from 'react';
import { language, resolveLanguage, setLanguage, subscribe, t, tParts, type MessageKey } from '../../shared/i18n/index.ts';

export { formatDate, formatDateTime, formatList, formatNumber, formatTime, language, languages, t, type MessageKey } from '../../shared/i18n/index.ts';

export type RichValues = Record<string, ReactNode | ((chunk: ReactNode) => ReactNode)>;

export type Translate = typeof t & { rich: typeof tr; lang: string };

function nodes(parts: unknown[]): ReactNode {
  if (parts.every((p) => typeof p === 'string')) return parts.join('');
  return parts.map((p, i) => <Fragment key={i}>{isValidElement(p) || typeof p !== 'object' || p === null ? (p as ReactNode) : String(p)}</Fragment>);
}

// tr is t for a message with markup in it: values may be nodes, and a
// <tag>chunk</tag> becomes whatever values.tag makes of its chunk.
export function tr(key: MessageKey, values?: RichValues): ReactNode {
  const wrapped: Record<string, unknown> = {};
  for (const [name, value] of Object.entries(values ?? {})) {
    wrapped[name] = typeof value === 'function' ? (chunk: unknown[]) => value(nodes(chunk)) : value;
  }
  return nodes(tParts(key, wrapped));
}

const translators = new Map<string, Translate>();

function translator(lang: string): Translate {
  let fn = translators.get(lang);
  if (!fn) {
    fn = Object.assign((key: MessageKey, values?: Parameters<typeof t>[1]) => t(key, values), { rich: tr, lang });
    translators.set(lang, fn);
  }
  return fn;
}

// useLanguage is the current language's tag, re-rendering when it changes.
export function useLanguage(): string {
  return useSyncExternalStore(subscribe, language);
}

export function useT(): Translate {
  return translator(useLanguage());
}

const storageKey = 'agentbox.language';

// applyLanguage makes a tag the app's language: the messages, the page's lang
// (which hyphenation and screen readers go by), the main process's dialogs,
// and the copy kept for the next start.
export function applyLanguage(tag: string | null | undefined): void {
  const next = resolveLanguage(tag).tag;
  setLanguage(next);
  document.documentElement.lang = next;
  try {
    localStorage.setItem(storageKey, next);
  } catch {
    // A page with no storage still speaks it, until it reloads.
  }
  window.agentbox?.setLanguage?.(next);
}

// restoreLanguage starts the window in the language it was last in, before the
// first paint; the daemon's settings confirm or correct it a moment later.
// With nothing kept it's en-US, the daemon's default, rather than the system's:
// the setting is the one place the language is chosen.
export function restoreLanguage(): void {
  let kept: string | null = null;
  try {
    kept = localStorage.getItem(storageKey);
  } catch {
    // Nothing kept.
  }
  applyLanguage(kept);
}
