// The small parts every screen shares: a chat's status line, a screen's
// header, buttons. After the web phone app's (desktop/src/renderer/web/PhoneApp.tsx).
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ActivityIndicator, Animated, Pressable, StyleSheet, Text, View, type StyleProp, type ViewStyle } from 'react-native';
import { useRouter } from 'expo-router';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import type { ChatStatus } from '../features/threads/activity.ts';
import { useT } from '../lib/i18n.tsx';
import { useBarShown } from './ConnectionBar.tsx';
import { colors, mono, toneDot, toneText } from './theme.ts';

// elapsed is how long since a moment: 12s, 3m 4s, 1h 2m.
export function elapsed(since: string, now = Date.now()): string {
  const s = Math.max(0, Math.floor((now - Date.parse(since)) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

export function Elapsed({ since }: { since: string }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  return <>{elapsed(since, now)}</>;
}

// Dot is a status's dot, pulsing while the chat is live.
export function Dot({ status }: { status: Pick<ChatStatus, 'tone' | 'live'> }) {
  const pulse = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    if (!status.live) return;
    const loop = Animated.loop(Animated.timing(pulse, { toValue: 1, duration: 1200, useNativeDriver: true }));
    loop.start();
    return () => loop.stop();
  }, [status.live, pulse]);
  const color = toneDot[status.tone];
  return (
    <View style={styles.dotBox}>
      {status.live && (
        <Animated.View
          style={[styles.dotPulse, { backgroundColor: color, opacity: pulse.interpolate({ inputRange: [0, 1], outputRange: [0.6, 0] }), transform: [{ scale: pulse.interpolate({ inputRange: [0, 1], outputRange: [1, 2.2] }) }] }]}
        />
      )}
      <View style={[styles.dot, { backgroundColor: color }]} />
    </View>
  );
}

// StatusLine is a chat's status in one line: the dot, what it is doing, the
// command it runs, for how long, and what follows, fainter.
export function StatusLine({ status, after, size = 12 }: { status: ChatStatus; after?: string; size?: number }) {
  return (
    <View style={styles.statusLine}>
      <Dot status={status} />
      <Text numberOfLines={1} style={[styles.statusText, { fontSize: size }]}>
        <Text style={{ color: toneText[status.tone] }}>{status.text}</Text>
        {status.command ? <Text style={[mono, { color: colors.secondary, fontSize: size - 1 }]}> {status.command}</Text> : null}
        {status.since ? (
          <Text style={{ color: colors.subtle }}>
            {' · '}
            <Elapsed since={status.since} />
          </Text>
        ) : null}
        {after ? <Text style={{ color: colors.faint }}> · {after}</Text> : null}
      </Text>
    </View>
  );
}

// useTopInset is the status bar's height, unless the connection bar above
// the screen already keeps clear of it.
export function useTopInset(): number {
  const insets = useSafeAreaInsets();
  return useBarShown() ? 0 : insets.top;
}

export function ScreenHeader({ title, subtitle, status, right }: { title: string; subtitle?: string; status?: ChatStatus; right?: ReactNode }) {
  const t = useT();
  const router = useRouter();
  const top = useTopInset();
  return (
    <View style={[styles.header, { paddingTop: top + 6 }]}>
      <Pressable accessibilityRole="button" accessibilityLabel={t('web.phone.back')} onPress={() => (router.canGoBack() ? router.back() : router.replace('/'))} style={styles.back} hitSlop={8}>
        <Text style={styles.backArrow}>‹</Text>
      </Pressable>
      <View style={styles.headerText}>
        <Text numberOfLines={1} style={styles.headerTitle}>
          {title}
        </Text>
        {status ? <StatusLine status={status} after={subtitle} size={11.5} /> : subtitle ? <Text numberOfLines={1} style={styles.headerSub}>{subtitle}</Text> : null}
      </View>
      {right}
    </View>
  );
}

export function Button({
  label,
  onPress,
  kind = 'primary',
  busy,
  disabled,
  style,
}: {
  label: string;
  onPress: () => void;
  kind?: 'primary' | 'secondary' | 'danger';
  busy?: boolean;
  disabled?: boolean;
  style?: StyleProp<ViewStyle>;
}) {
  const off = disabled || busy;
  return (
    <Pressable
      accessibilityRole="button"
      disabled={off}
      onPress={onPress}
      style={({ pressed }) => [styles.button, kind === 'primary' ? styles.buttonPrimary : kind === 'danger' ? styles.buttonDanger : styles.buttonSecondary, (pressed || off) && { opacity: 0.6 }, style]}
    >
      {busy ? <ActivityIndicator size="small" color={colors.title} /> : <Text style={[styles.buttonText, kind === 'danger' && { color: colors.rose }]}>{label}</Text>}
    </Pressable>
  );
}

export function Note({ children, tone = 'muted' }: { children: ReactNode; tone?: 'muted' | 'error' | 'warning' }) {
  return (
    <View style={[styles.note, tone === 'error' && { backgroundColor: colors.roseSoft }, tone === 'warning' && { backgroundColor: colors.amberSoft }]}>
      <Text style={[styles.noteText, tone === 'error' && { color: colors.rose }, tone === 'warning' && { color: colors.amber }]}>{children}</Text>
    </View>
  );
}

export const styles = StyleSheet.create({
  dotBox: { width: 8, height: 8, alignItems: 'center', justifyContent: 'center' },
  dot: { width: 6, height: 6, borderRadius: 3 },
  dotPulse: { position: 'absolute', width: 8, height: 8, borderRadius: 4 },
  statusLine: { flexDirection: 'row', alignItems: 'center', gap: 6, minWidth: 0 },
  statusText: { flexShrink: 1 },
  header: { flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: 6, paddingBottom: 8, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.lineStrong, backgroundColor: colors.overlay },
  back: { width: 40, height: 40, alignItems: 'center', justifyContent: 'center', borderRadius: 20 },
  backArrow: { color: colors.secondary, fontSize: 30, lineHeight: 32, marginTop: -2 },
  headerText: { flex: 1, minWidth: 0, gap: 2 },
  headerTitle: { color: colors.title, fontSize: 16, fontWeight: '600' },
  headerSub: { color: colors.subtle, fontSize: 11.5 },
  button: { minHeight: 40, paddingHorizontal: 16, borderRadius: 12, alignItems: 'center', justifyContent: 'center' },
  buttonPrimary: { backgroundColor: colors.brand500 },
  buttonSecondary: { backgroundColor: colors.raised, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong },
  buttonDanger: { backgroundColor: colors.roseSoft },
  buttonText: { color: colors.title, fontSize: 14, fontWeight: '600' },
  note: { borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, backgroundColor: colors.surface },
  noteText: { color: colors.muted, fontSize: 13, lineHeight: 18 },
});
