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
// in internal/cli/media.go, which words the CLI's own question the same way.
export function describeAll(count: number, kind: string, agent: string): string {
  const what = kind || 'item';
  return `all ${count} ${count === 1 ? what : `${what}s`}${agent ? ` of ${agent}` : ''}`;
}

export function clock(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}
