// ConnectionBar says the phone lost AgentBox and is getting it back, until it
// is, as the web phone app's does: the first connection says nothing, and
// getting back says so for a moment.
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { reconnectNow, useConnection } from '../connection/events.ts';
import { useT } from '../lib/i18n.tsx';
import { colors } from './theme.ts';

// Whether the bar shows, for the screen's header below it, which leaves the
// status bar's inset to it meanwhile.
let shown = false;
const listeners = new Set<() => void>();
function setShown(next: boolean) {
  if (next === shown) return;
  shown = next;
  for (const fn of listeners) fn();
}

export function useBarShown(): boolean {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => shown,
    () => shown,
  );
}

export function ConnectionBar() {
  const t = useT();
  const connection = useConnection();
  const insets = useSafeAreaInsets();
  const [back, setBack] = useState(false);
  const ever = useRef(false);
  const wasLost = useRef(false);
  useEffect(() => {
    if (connection === 'connected') {
      ever.current = true;
      if (!wasLost.current) return;
      wasLost.current = false;
      setBack(true);
      const timer = setTimeout(() => setBack(false), 2_000);
      return () => clearTimeout(timer);
    }
    if (ever.current || connection === 'disconnected') {
      wasLost.current = true;
      setBack(false);
    }
  }, [connection]);
  const lost = connection === 'disconnected' || (ever.current && connection === 'connecting');
  useEffect(() => setShown(lost || back), [lost, back]);
  if (!lost && !back) return null;
  return (
    <View style={[styles.bar, { paddingTop: insets.top + 4 }, lost ? styles.lost : styles.back]} accessibilityRole="alert">
      <Text style={[styles.text, { color: lost ? colors.amber : colors.emerald }]} numberOfLines={1}>
        {lost ? t('web.phone.reconnecting') : t('web.phone.backOnline')}
      </Text>
      {lost && <ActivityIndicator size="small" color={colors.amber} />}
      {connection === 'disconnected' && (
        <Pressable onPress={reconnectNow} hitSlop={8}>
          <Text style={[styles.text, styles.retry]}>{t('web.phone.retryNow')}</Text>
        </Pressable>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  bar: { flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 16, paddingBottom: 6 },
  lost: { backgroundColor: colors.amberSoft },
  back: { backgroundColor: colors.emeraldSoft },
  text: { flex: 1, fontSize: 12, fontWeight: '500' },
  retry: { flex: 0, color: colors.amber, textDecorationLine: 'underline' },
});
