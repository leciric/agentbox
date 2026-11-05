// The projects list's order this phone chose last, kept across launches as
// the merge method is (review/merge.ts).
import * as SecureStore from 'expo-secure-store';
import { useSyncExternalStore } from 'react';
import { Platform } from 'react-native';
import { projectOrders, type ProjectOrder } from './projectList.ts';

const key = 'agentbox.projectOrder';
let order: ProjectOrder = 'default';
const listeners = new Set<() => void>();

void (Platform.OS === 'web' ? Promise.resolve(globalThis.localStorage?.getItem(key) ?? null) : SecureStore.getItemAsync(key)).then((v) => {
  if (!projectOrders.includes(v as ProjectOrder)) return;
  order = v as ProjectOrder;
  for (const fn of listeners) fn();
});

export function useProjectOrder(): ProjectOrder {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => order,
    () => order,
  );
}

export async function rememberProjectOrder(next: ProjectOrder): Promise<void> {
  order = next;
  for (const fn of listeners) fn();
  if (Platform.OS === 'web') globalThis.localStorage?.setItem(key, next);
  else await SecureStore.setItemAsync(key, next);
}
