import { Camera, File, FileChartColumn, ScrollText, StickyNote, Video } from 'lucide-react';
import type { ComponentType } from 'react';
import type * as T from '../../shared/api';

// mediaUrl is where the renderer loads an item's file from: the desktop app's
// media protocol, or the hub, in a browser.
export function mediaUrl(item: Pick<T.MediaItem, 'id'>, path?: string): string {
  return window.agentbox.mediaUrl(item.id, path);
}

export const mediaKinds: { kind: string; label: string; one: string; icon: ComponentType<{ className?: string }> }[] = [
  { kind: 'screenshot', label: 'Screenshots', one: 'screenshot', icon: Camera },
  { kind: 'recording', label: 'Recordings', one: 'recording', icon: Video },
  { kind: 'report', label: 'Reports', one: 'report', icon: FileChartColumn },
  { kind: 'log', label: 'Logs', one: 'log', icon: ScrollText },
  { kind: 'note', label: 'Notes', one: 'note', icon: StickyNote },
  { kind: 'file', label: 'Files', one: 'file', icon: File },
];

export function kindInfo(kind: string) {
  return mediaKinds.find((k) => k.kind === kind) ?? mediaKinds[mediaKinds.length - 1];
}

// describeAll names what a "Delete all" covers, so a confirmation says which
// filter it obeys: "all 42 screenshots of agent-12". Mirrors describeAllMedia
// in internal/cli/media.go, which words the CLI's own question the same way;
// the CLI has no search, so only the app's says what was searched for.
export function describeAll(count: number, kind: string, agent: string, query = ''): string {
  const what = kind || 'item';
  const q = query.trim();
  return `all ${count} ${count === 1 ? what : `${what}s`}${agent ? ` of ${agent}` : ''}${q ? ` matching “${q}”` : ''}`;
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
const haystacks = new WeakMap<T.MediaItem, string>();

function haystack(item: T.MediaItem): string {
  let text = haystacks.get(item);
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
    haystacks.set(item, text);
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
