// The merge method this phone used last, kept beside its pairing.
import * as SecureStore from 'expo-secure-store';
import { useSyncExternalStore } from 'react';
import { Platform } from 'react-native';

export { mergeBlock, pickMethod } from './mergeRules.ts';

const key = 'agentbox.mergeMethod';
let method: string | null = null;
const listeners = new Set<() => void>();

void (Platform.OS === 'web' ? Promise.resolve(globalThis.localStorage?.getItem(key) ?? null) : SecureStore.getItemAsync(key)).then((v) => {
  method = v;
  for (const fn of listeners) fn();
});

export function useMergeMethod(): string | null {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => method,
    () => method,
  );
}

export async function rememberMethod(next: string): Promise<void> {
  method = next;
  for (const fn of listeners) fn();
  if (Platform.OS === 'web') globalThis.localStorage?.setItem(key, next);
  else await SecureStore.setItemAsync(key, next);
}
