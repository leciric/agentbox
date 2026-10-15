// What this phone has read of each chat, for the chat list's unread marker:
// when each chat was last on screen here, kept in the browser. A chat whose
// latest reply (lib/chatActivity.ts's lastReply) is newer than that is unread.
// Another phone, or the app on the computer, keeps its own.

const storageKey = 'agentbox.phone.seen';

type Seen = Record<string, string>;

export interface SeenStore {
  // unread says whether a chat said something since it was last read here.
  // A chat this phone has never shown is taken as read at its latest reply,
  // so a phone paired today doesn't open on everything unread.
  unread: (ref: string, reply: string | undefined) => boolean;
  // read marks a chat read up to a time, its latest reply or now.
  read: (ref: string, at: string) => void;
  subscribe: (fn: () => void) => () => void;
  version: () => number;
}

export function seenStore(storage: Pick<Storage, 'getItem' | 'setItem'> | undefined): SeenStore {
  let seen: Seen = {};
  try {
    seen = JSON.parse(storage?.getItem(storageKey) ?? '{}') ?? {};
  } catch {
    // nothing saved, or not ours
  }
  let version = 0;
  const listeners = new Set<() => void>();
  const save = () => {
    version++;
    try {
      storage?.setItem(storageKey, JSON.stringify(seen));
    } catch {
      // private browsing, or full: the marker lasts as long as the page
    }
    for (const fn of listeners) fn();
  };
  return {
    unread: (ref, reply) => {
      if (!reply) return false;
      const at = seen[ref];
      if (!at) {
        // Silently: this is a read during render.
        seen[ref] = reply;
        return false;
      }
      return Date.parse(reply) > Date.parse(at);
    },
    read: (ref, at) => {
      const was = seen[ref];
      if (was && Date.parse(was) >= Date.parse(at)) return;
      seen[ref] = at;
      save();
    },
    subscribe: (fn) => {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
    version: () => version,
  };
}

export const seen = seenStore(typeof localStorage === 'undefined' ? undefined : localStorage);
