import { Camera, File, FileChartColumn, ScrollText, StickyNote, Video } from 'lucide-react';
import type { ComponentType } from 'react';
import type * as T from '../../shared/api';
import { language, t } from '../../shared/i18n/index.ts';

// mediaUrl is where the renderer loads an item's file from: the desktop app's
// media protocol, or the hub, in a browser.
export function mediaUrl(item: Pick<T.MediaItem, 'id'>, path?: string): string {
  return window.agentbox.mediaUrl(item.id, path);
}

export const mediaKinds: { kind: string; label: string; one: string; icon: ComponentType<{ className?: string }> }[] = [
  // label and one are read when they're shown, in the language of the moment.
  {
    kind: 'screenshot',
    get label() {
      return t('agent.media.kind.screenshot');
    },
    get one() {
      return noun('screenshot', 1);
    },
    icon: Camera,
  },
  {
    kind: 'recording',
    get label() {
      return t('agent.media.kind.recording');
    },
    get one() {
      return noun('recording', 1);
    },
    icon: Video,
  },
  {
    kind: 'report',
    get label() {
      return t('agent.media.kind.report');
    },
    get one() {
      return noun('report', 1);
    },
    icon: FileChartColumn,
  },
  {
    kind: 'log',
    get label() {
      return t('agent.media.kind.log');
    },
    get one() {
      return noun('log', 1);
    },
    icon: ScrollText,
  },
  {
    kind: 'note',
    get label() {
      return t('agent.media.kind.note');
    },
    get one() {
      return noun('note', 1);
    },
    icon: StickyNote,
  },
  {
    kind: 'file',
    get label() {
      return t('agent.media.kind.file');
    },
    get one() {
      return noun('file', 1);
    },
    icon: File,
  },
];

// noun is what a kind of item is called, for count of them.
function noun(kind: string, count: number): string {
  switch (kind) {
    case 'screenshot':
      return t('agent.media.noun.screenshot', { count });
    case 'recording':
      return t('agent.media.noun.recording', { count });
    case 'report':
      return t('agent.media.noun.report', { count });
    case 'log':
      return t('agent.media.noun.log', { count });
    case 'note':
      return t('agent.media.noun.note', { count });
    case 'file':
      return t('agent.media.noun.file', { count });
    default:
      return t('agent.media.noun.item', { count });
  }
}

export function kindInfo(kind: string) {
  return mediaKinds.find((k) => k.kind === kind) ?? mediaKinds[mediaKinds.length - 1];
}

// describeAll names what a "Delete all" covers, so a confirmation says which
// filter it obeys: "all 42 screenshots of agent-12". Mirrors describeAllMedia
// in internal/cli/media.go, which words the CLI's own question the same way;
// the CLI has no search, so only the app's says what was searched for.
export function describeAll(count: number, kind: string, agent: string, query = ''): string {
  const q = query.trim();
  let label = t('agent.media.all', { count, what: noun(kind, count) });
  if (agent) label = t('agent.media.allOf', { base: label, agent });
  if (q) label = t('agent.media.allMatching', { base: label, query: q });
  return label;
}

// kindWords are the other words a search finds each kind by, beyond its own
// name and label: "image" finds the screenshots, "video" the recordings. A
// file's MIME type is searched too, so an image added as a file is found by
// "image" as well.
const kindWords: Record<string, string> = {
  screenshot: 'image picture',
  recording: 'video screencast',
  report: 'test',
  note: 'text',
};

// fold is how a search and what it searches are compared: case and accents
// ignored, so "resume" finds "Résumé".
function fold(s: string): string {
  return s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
}

// haystacks caches each item's searchable text, folded once. The lists come
// from react-query, which keeps an item's object while it's unchanged, so a
// keystroke on a gallery of hundreds only compares strings.
const haystacks = new WeakMap<T.MediaItem, { lang: string; text: string }>();

function haystack(item: T.MediaItem): string {
  const lang = language();
  const cached = haystacks.get(item);
  let text = cached?.lang === lang ? cached.text : undefined;
  if (text === undefined) {
    const info = kindInfo(item.kind);
    text = fold(
      [
        item.name,
        item.file,
        item.kind,
        info.label,
        kindWords[item.kind],
        item.mime,
        item.meta.url,
        item.agentName,
        item.agentTitle,
        // Only a note's text is in the list; a log's or a report's is a file
        // the daemon would have to read, so they're found by name and type.
        item.kind === 'note' ? item.text : undefined,
      ]
        .filter(Boolean)
        .join('\n'),
    );
    haystacks.set(item, { lang, text });
  }
  return text;
}

// searchTerms splits a search into what every match has to contain: its
// words, or a "quoted phrase" kept whole.
export function searchTerms(query: string): string[] {
  return [...fold(query).matchAll(/"([^"]*)"|(\S+)/g)].map((m) => (m[1] ?? m[2]).trim()).filter(Boolean);
}

// searchMedia keeps the items that contain every term of the search, in
// their name, file name, type or, for a note, its text. An empty search
// keeps everything.
export function searchMedia(items: T.MediaItem[], query: string): T.MediaItem[] {
  const terms = searchTerms(query);
  if (terms.length === 0) return items;
  return items.filter((item) => {
    const text = haystack(item);
    return terms.every((term) => text.includes(term));
  });
}

export function clock(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}
