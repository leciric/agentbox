// The sessions of a project's chat, as a row of chips under its header: the
// one shown, the others to switch to, each with where it stands before its
// name (an amber count of what waits on you, a red mark when its chat stopped
// or stalled, a spinner while its lead works, a dot when it said something
// you haven't seen here or on the desktop), and
// + to open a new one at once, untitled until its first turn names it. The
// same sessions as the desktop's tabs (internal/state/leadsessions.go).
import { Plus, TriangleAlert } from 'lucide-react-native';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import type * as T from '../../shared/api.ts';
import { useT } from '../../lib/i18n.tsx';
import { colors } from '../../ui/theme.ts';

export function SessionBar({ sessions, current, canAdd, onChoose, onAdd }: { sessions: T.LeadSession[]; current?: string; canAdd: boolean; onChoose: (id: string) => void; onAdd: () => void }) {
  const t = useT();
  // An untitled session's topic is the daemon's placeholder, in English.
  const topicOf = (s: T.LeadSession) => (s.untitled ? t('sessions.untitled') : s.topic);
  if (sessions.length < 2 && !(sessions.length === 0 && canAdd)) {
    return canAdd && sessions.length === 1 ? (
      <View style={styles.bar}>
        <Text style={[styles.chipText, styles.only]} numberOfLines={1}>{topicOf(sessions[0])}</Text>
        <Pressable accessibilityLabel={t('sessions.new')} onPress={onAdd} style={styles.add}>
          <Plus size={16} color={colors.muted} />
        </Pressable>
      </View>
    ) : null;
  }
  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.scroll} contentContainerStyle={styles.bar} keyboardShouldPersistTaps="handled">
      {sessions.map((s) => {
        const on = s.id === current;
        return (
          <Pressable key={s.id} accessibilityRole="tab" accessibilityState={{ selected: on }} onPress={() => onChoose(s.id)} style={[styles.chip, on && styles.chipOn]}>
            <StatusMark session={s} unread={!on && !!s.unread} />
            <Text style={[styles.chipText, on && styles.chipTextOn]} numberOfLines={1}>{topicOf(s)}</Text>
          </Pressable>
        );
      })}
      {canAdd ? (
        <Pressable accessibilityLabel={t('sessions.new')} onPress={onAdd} style={styles.add}>
          <Plus size={16} color={colors.muted} />
          {sessions.length === 0 && <Text style={styles.chipText}>{t('sessions.new')}</Text>}
        </Pressable>
      ) : null}
    </ScrollView>
  );
}

// StatusMark is the desktop's SessionStatusMark (LeadSession.status), with an
// older daemon's session read from its chat's state.
function StatusMark({ session: s, unread }: { session: T.LeadSession; unread: boolean }) {
  const t = useT();
  const status = s.status || (s.chat === 'waiting' ? 'needsYou' : s.chat === 'error' ? 'failed' : s.chat === 'running' || s.chat === 'starting' ? 'working' : 'idle');
  if (status === 'needsYou') {
    const n = Math.max(1, s.needsYou ?? 0);
    return (
      <View accessibilityLabel={t('sessions.needsYou', { count: n })} style={styles.needs}>
        <Text style={styles.needsText}>{n}</Text>
      </View>
    );
  }
  if (status === 'failed') return <TriangleAlert accessibilityLabel={t('sessions.failed')} size={13} color={colors.rose} />;
  if (status === 'working') return <ActivityIndicator size="small" color={colors.brand300} style={styles.spin} />;
  if (unread) return <View accessibilityLabel={t('sessions.unread')} style={styles.dot} />;
  return null;
}

const styles = StyleSheet.create({
  scroll: { flexGrow: 0, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.lineStrong },
  bar: { flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: 12, paddingVertical: 8 },
  chip: { flexDirection: 'row', alignItems: 'center', gap: 6, maxWidth: 200, borderRadius: 14, paddingHorizontal: 12, paddingVertical: 6, backgroundColor: colors.surface },
  chipOn: { backgroundColor: colors.brandSoft },
  chipText: { color: colors.muted, fontSize: 13 },
  chipTextOn: { color: colors.title, fontWeight: '600' },
  only: { flex: 1 },
  spin: { transform: [{ scale: 0.7 }] },
  needs: { minWidth: 16, height: 16, borderRadius: 8, paddingHorizontal: 4, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.amber },
  needsText: { color: '#000', fontSize: 10, fontWeight: '700' },
  dot: { width: 6, height: 6, borderRadius: 3, backgroundColor: colors.brand400 },
  add: { flexDirection: 'row', alignItems: 'center', gap: 4, borderRadius: 14, paddingHorizontal: 10, paddingVertical: 6 },
});
