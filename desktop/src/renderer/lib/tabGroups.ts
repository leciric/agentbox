import type * as T from '../../shared/api';
import { isUnread, moveTab, needsYouCount, sessionStatus, sortTabs } from './sessions.ts';

// A chat's tab groups (components/chat/SessionTabs.tsx), kept by the daemon
// (GET …/session-groups). The row reads: the pinned tabs, then one button per
// group with its sessions in a menu (or spread as tabs after it when the
// group isn't collapsed), then the loose tabs. A pinned session is in no
// group. What here is pure is so the node tests can reach it.

export type RowItem = { kind: 'tab'; session: T.LeadSession } | { kind: 'group'; group: T.LeadSessionGroup; sessions: T.LeadSession[] };

// tabRow is what the row shows, in order, of the open sessions as the daemon
// sorted them (sortTabs): a group with no open session in it (every one of
// its sessions closed or snoozed) shows nothing.
export function tabRow(sessions: readonly T.LeadSession[], groups: readonly T.LeadSessionGroup[]): RowItem[] {
  const known = new Set(groups.map((g) => g.id));
  const inGroup = (s: T.LeadSession) => !s.pinned && !!s.group && known.has(s.group);
  const sorted = sortTabs(sessions, groups);
  const out: RowItem[] = sorted.filter((s) => s.pinned).map((session) => ({ kind: 'tab', session }));
  for (const group of groups) {
    const members = sorted.filter((s) => inGroup(s) && s.group === group.id);
    if (members.length > 0) out.push({ kind: 'group', group, sessions: members });
  }
  for (const session of sorted) if (!session.pinned && !inGroup(session)) out.push({ kind: 'tab', session });
  return out;
}

// rowSessions is every open session in the row's order, those folded in a
// group's menu too: what Ctrl+Tab and Ctrl+Shift+Tab walk, so the keys still
// reach every session.
export const rowSessions = (row: readonly RowItem[]): T.LeadSession[] => row.flatMap((it) => (it.kind === 'tab' ? [it.session] : it.sessions));

// jumpTo is the session Mod+n goes to: the nth thing in the row, a tab or a
// group. A group goes to the session of it on screen, else the one last shown
// from it (last, by group), else its first.
export function jumpTo(row: readonly RowItem[], n: number, current: string | undefined, last: Readonly<Record<string, string>> = {}): T.LeadSession | undefined {
  const it = row[n - 1];
  if (!it) return undefined;
  if (it.kind === 'tab') return it.session;
  return it.sessions.find((s) => s.id === current) ?? it.sessions.find((s) => s.id === last[it.group.id]) ?? it.sessions[0];
}

// GroupSum is what a group's button says of the sessions in its menu: how
// many things wait on you, whether one stopped, and, when neither, whether
// one works or said something unseen. The session on screen isn't counted:
// it is drawn joined to the button with its own mark.
export interface GroupSum {
  needsYou: number;
  failed: boolean;
  working: boolean;
  unread: boolean;
}

export function groupSum(sessions: readonly T.LeadSession[], current: string | undefined, seen: Readonly<Record<string, string>>): GroupSum {
  const rest = sessions.filter((s) => s.id !== current);
  const needsYou = rest.reduce((n, s) => n + needsYouCount(s), 0);
  const failed = rest.some((s) => sessionStatus(s) === 'failed');
  const calm = needsYou === 0 && !failed;
  const working = calm && rest.some((s) => sessionStatus(s) === 'working');
  const unread = calm && !working && rest.some((s) => isUnread(s, seen[s.ref]));
  return { needsYou, failed, working, unread };
}

// A drag in the row, measured from its elements' boxes (SessionTabs):
// a tab's box (data-session), a group's button and the session joined to it
// (data-session-group-drop), each from left over width.
export interface DropBox {
  kind: 'tab' | 'group';
  id: string; // a session's id, or a group's
  left: number;
  width: number;
  pinned?: boolean;
  group?: string; // a tab spread from a group's
}

export type Dragged = { kind: 'tab'; id: string; pinned?: boolean; group?: string } | { kind: 'group'; id: string };

// Drop is what letting go does: move into a gap before a tab or group (or,
// with none, after the last), join a group, or make a new group of the
// dragged tab and another loose one.
export type Drop = { kind: 'gap'; before?: string } | { kind: 'into'; group: string } | { kind: 'merge'; with: string };

// dropAt is what letting go at x does. A group's button, or the middle third
// of a tab of a group, takes a tab into that group; the middle third of
// another loose tab makes a group of the two; anywhere else is the gap at
// the nearest edge. A pinned tab is in no group, so it only moves; a group
// only moves among the groups.
export function dropAt(boxes: readonly DropBox[], x: number, dragged: Dragged): Drop {
  const gap = (among: readonly DropBox[]): Drop => ({ kind: 'gap', before: among.find((b) => x < b.left + b.width / 2)?.id });
  if (dragged.kind === 'group') return gap(boxes.filter((b) => b.kind === 'group'));
  const over = boxes.find((b) => x >= b.left && x < b.left + b.width);
  if (over && !dragged.pinned && !(over.kind === 'tab' && over.id === dragged.id)) {
    if (over.kind === 'group') {
      if (over.id !== dragged.group) return { kind: 'into', group: over.id };
    } else if (!over.pinned && x >= over.left + over.width / 3 && x < over.left + (over.width * 2) / 3) {
      if (over.group) {
        if (over.group !== dragged.group) return { kind: 'into', group: over.group };
      } else {
        return { kind: 'merge', with: over.id };
      }
    }
  }
  return gap(boxes.filter((b) => b.kind === 'tab'));
}

// moveBefore is the open sessions with id moved before another (or last with
// none), kept in its own lot: the pinned, its group's, or the loose ones.
export function moveBefore(sessions: readonly T.LeadSession[], id: string, before: string | undefined, groups: readonly T.LeadSessionGroup[]): T.LeadSession[] {
  const to = before === undefined ? sessions.length : sessions.findIndex((s) => s.id === before);
  return moveTab(sessions, id, to < 0 ? sessions.length : to, groups);
}

// moveGroupBefore is the groups with id moved before another, or last.
export function moveGroupBefore(groups: readonly T.LeadSessionGroup[], id: string, before: string | undefined): T.LeadSessionGroup[] {
  const moving = groups.find((g) => g.id === id);
  if (!moving || before === id) return [...groups];
  const rest = groups.filter((g) => g.id !== id);
  const at = before === undefined ? rest.length : rest.findIndex((g) => g.id === before);
  rest.splice(at < 0 ? rest.length : at, 0, moving);
  return rest;
}

// withGroup is the sessions with id moved into group (or out, with ''), as
// the daemon will have it: pinning aside, it goes last of its new lot.
export function withGroup(sessions: readonly T.LeadSession[], id: string, group: string, groups: readonly T.LeadSessionGroup[]): T.LeadSession[] {
  const moved = sessions.find((s) => s.id === id);
  if (!moved || moved.pinned) return [...sessions];
  return sortTabs([...sessions.filter((s) => s.id !== id), { ...moved, group: group || undefined }], groups);
}
