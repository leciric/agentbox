// A chat's thread as screens read it: the list peeks at each chat's latest
// turn (what it is doing, when it last answered), and a chat's screen reads
// its latest page and older ones as you scroll up. Both live in the same
// query, kept current by chat events (thread.ts). After the desktop's
// fetchThread and peekThread (renderer/lib/chat.ts).
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api.ts';
import { api } from '../../connection/api.ts';
import { chatKey, isMessage, pageSize, prependPage } from './thread.ts';

export async function fetchThread(queryClient: QueryClient, ref: string): Promise<T.ChatThread> {
  const held = queryClient.getQueryData<T.ChatThread>(chatKey(ref));
  // Read again from the oldest item held, so nothing on screen is dropped.
  const limit = Math.max(pageSize, held?.items.filter(isMessage).length ?? 0);
  return api.chat(ref, { limit, from: held && held.items.length > 1 ? held.items[0]?.id : undefined });
}

export async function loadOlder(queryClient: QueryClient, ref: string): Promise<void> {
  const held = queryClient.getQueryData<T.ChatThread>(chatKey(ref));
  const first = held?.items[0];
  if (!held?.older || !first) return;
  const page = await api.chat(ref, { before: first.id, limit: pageSize });
  queryClient.setQueryData<T.ChatThread>(chatKey(ref), (now) => (now ? prependPage(now, page) : now));
}

// useThread is a chat's thread for its screen.
export function useThread(ref: string | undefined) {
  const queryClient = useQueryClient();
  return useQuery({ queryKey: chatKey(ref ?? ''), queryFn: () => fetchThread(queryClient, ref!), enabled: !!ref });
}

// peek reads a chat's latest message only, unless it's held already.
export function peekThread(queryClient: QueryClient, ref: string): Promise<T.ChatThread> {
  if (queryClient.getQueryData<T.ChatThread>(chatKey(ref))) return fetchThread(queryClient, ref);
  return api.chat(ref, { limit: 1 });
}
