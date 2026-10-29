// The sessions of a project's chat, as a row of chips under its header: the
// one shown, the others to switch to, a spinner on one whose lead works, and
// + to open a new one on a topic typed in place. The same sessions as the
// desktop's tabs (internal/state/leadsessions.go).
import { Plus } from 'lucide-react-native';
import { useState } from 'react';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import type * as T from '../../shared/api.ts';
import { useT } from '../../lib/i18n.tsx';
import { colors } from '../../ui/theme.ts';

export function SessionBar({ sessions, current, canAdd, onChoose, onAdd }: { sessions: T.LeadSession[]; current?: string; canAdd: boolean; onChoose: (id: string) => void; onAdd: (topic: string) => void }) {
  const t = useT();
  const [adding, setAdding] = useState(false);
  const [topic, setTopic] = useState('');
  if (sessions.length < 2 && !adding && !(sessions.length === 0 && canAdd)) {
    return canAdd && sessions.length === 1 ? (
      <View style={styles.bar}>
        <Text style={[styles.chipText, styles.only]} numberOfLines={1}>{sessions[0].topic}</Text>
        <Pressable accessibilityLabel={t('sessions.new')} onPress={() => setAdding(true)} style={styles.add}>
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
            {s.chat === 'running' || s.chat === 'starting' ? <ActivityIndicator size="small" color={colors.brand300} style={styles.spin} /> : null}
            <Text style={[styles.chipText, on && styles.chipTextOn]} numberOfLines={1}>{s.topic}</Text>
          </Pressable>
        );
      })}
      {adding ? (
        <TextInput
          autoFocus
          value={topic}
          onChangeText={setTopic}
          placeholder={t('sessions.topicPlaceholder')}
          placeholderTextColor={colors.faint}
          maxLength={200}
          returnKeyType="done"
          onSubmitEditing={() => {
            if (topic.trim()) onAdd(topic.trim());
            setAdding(false);
            setTopic('');
          }}
          onBlur={() => setAdding(false)}
          style={styles.input}
        />
      ) : canAdd ? (
        <Pressable accessibilityLabel={t('sessions.new')} onPress={() => setAdding(true)} style={styles.add}>
          <Plus size={16} color={colors.muted} />
          {sessions.length === 0 && <Text style={styles.chipText}>{t('sessions.new')}</Text>}
        </Pressable>
      ) : null}
    </ScrollView>
  );
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
  add: { flexDirection: 'row', alignItems: 'center', gap: 4, borderRadius: 14, paddingHorizontal: 10, paddingVertical: 6 },
  input: { minWidth: 200, color: colors.primary, fontSize: 13, borderRadius: 14, borderWidth: 1, borderColor: colors.lineStrong, paddingHorizontal: 12, paddingVertical: 5 },
});
