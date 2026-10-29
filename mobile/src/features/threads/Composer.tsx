// The composer: a message to the chat, or Stop while its turn runs. Sending
// needs the phone's "chat" scope; a phone that may only read is told where
// to allow it. After t3code's ThreadComposer.tsx, without its attachments,
// commands and native editor, which come later.
//
// In a project's chat, a To: chip above it chooses who the message goes to:
// the lead, or one of the project's agents, the session's own first; and
// "@agent-31 " at the start of a message sends that one to the agent
// (desktop/src/shared/messageTarget.ts). Each session remembers its target
// while the app runs.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowUp, ChevronDown, Square } from 'lucide-react-native';
import { useState, useSyncExternalStore } from 'react';
import { Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { api } from '../../connection/api.ts';
import { can, useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { colors } from '../../ui/theme.ts';
import { atTarget, liveTarget, otherSession, targetsOf } from '../../shared/messageTarget.ts';
import type * as T from '../../shared/api.ts';
import { chatKey } from './thread.ts';

// The target each session's composer sends to, by its chat's ref.
const targets = new Map<string, string>();
const listeners = new Set<() => void>();
const chooseTarget = (ref: string, agent: string | undefined) => {
  if (agent) targets.set(ref, agent);
  else targets.delete(ref);
  for (const l of listeners) l();
};
const useTarget = (ref: string) =>
  useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => targets.get(ref),
  );

export function Composer({ chatRef, name, running, start, lead }: { chatRef: string; name: string; running: boolean; start?: () => Promise<unknown>; lead?: { project: string; session: string; sessions: T.LeadSession[] } }) {
  const t = useT();
  const insets = useSafeAreaInsets();
  const session = useSession();
  const queryClient = useQueryClient();
  const [text, setText] = useState('');
  const [picking, setPicking] = useState(false);
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled: !!lead });
  const choices = targetsOf(lead ? (agents.data ?? []) : [], lead?.project ?? '', lead?.session);
  const target = liveTarget(useTarget(chatRef), choices);
  const at = lead ? atTarget(text, [...choices.own, ...choices.others].map((a) => a.name)) : undefined;
  const sendTo = at?.to ?? target;
  const send = useMutation({
    mutationFn: async ({ message, to }: { message: string; to?: { to: string; anySession?: boolean }; once?: boolean }) => {
      if (to) return api.sendMessage(chatRef, message, to);
      // A lead that never ran starts with its first message.
      if (start) await start();
      return api.sendMessage(chatRef, message);
    },
    onMutate: () => setText(''),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: chatKey(chatRef) }),
    onError: (err, { message, to, once }) => {
      setText(to && once ? `@${to.to} ${message}` : message);
      Alert.alert(t('mobile.chat.couldntSend', { error: err instanceof Error ? err.message : String(err) }));
    },
  });
  const submit = () => {
    if (!sendTo) return send.mutate({ message: text.trim() });
    const message = at ? at.text : text.trim();
    if (!message) return;
    const other = otherSession(choices.others.find((a) => a.name === sendTo), lead?.session, lead?.sessions ?? []);
    if (!other) return send.mutate({ message, to: { to: sendTo }, once: !!at });
    Alert.alert(t('chat.target.otherSession', { name: sendTo, topic: other.topic }), undefined, [
      { text: t('common.cancel'), style: 'cancel' },
      { text: t('chat.target.sendAnyway'), onPress: () => send.mutate({ message, to: { to: sendTo, anySession: true }, once: !!at }) },
    ]);
  };
  const option = (agent: T.Agent | undefined) => {
    const on = (agent?.name ?? undefined) === sendTo;
    return (
      <Pressable
        key={agent?.name ?? ''}
        accessibilityRole="button"
        accessibilityState={{ selected: on }}
        onPress={() => {
          chooseTarget(chatRef, agent?.name);
          setPicking(false);
        }}
        style={[styles.option, on && styles.optionOn]}
      >
        <Text style={[styles.optionText, on && styles.optionTextOn]} numberOfLines={1}>
          {agent ? agent.name : t('chat.target.lead')}
        </Text>
      </Pressable>
    );
  };
  const stop = useMutation({ mutationFn: () => api.cancelChat(chatRef), onError: (err) => Alert.alert(err instanceof Error ? err.message : String(err)) });

  if (!can(session, 'chat')) {
    return (
      <View style={[styles.bar, { paddingBottom: insets.bottom + 10 }]}>
        <Text style={styles.cant}>{t('mobile.chat.cantChat')}</Text>
      </View>
    );
  }
  const empty = !text.trim();
  const bar = (
    <View style={[styles.bar, { paddingBottom: insets.bottom + 8 }]}>
      <TextInput
        value={text}
        onChangeText={setText}
        placeholder={t('mobile.chat.placeholder', { name: sendTo ?? name })}
        placeholderTextColor={colors.faint}
        style={styles.input}
        multiline
        accessibilityLabel={t('mobile.chat.placeholder', { name: sendTo ?? name })}
      />
      {running && empty ? (
        <Pressable accessibilityRole="button" accessibilityLabel={t('mobile.chat.stop')} onPress={() => stop.mutate()} disabled={stop.isPending} style={[styles.send, styles.stop]}>
          <Square size={14} color={colors.primary} fill={colors.primary} />
        </Pressable>
      ) : (
        <Pressable accessibilityRole="button" accessibilityLabel={t('mobile.chat.send')} onPress={submit} disabled={empty || send.isPending} style={[styles.send, (empty || send.isPending) && { opacity: 0.4 }]}>
          <ArrowUp size={20} color={colors.title} strokeWidth={2.4} />
        </Pressable>
      )}
    </View>
  );
  if (!lead) return bar;
  return (
    <View>
      {picking && (
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.options} contentContainerStyle={styles.optionsRow} keyboardShouldPersistTaps="handled">
          {option(undefined)}
          {choices.own.map(option)}
          {choices.others.length > 0 && choices.own.length > 0 && <View style={styles.divider} />}
          {choices.others.map(option)}
        </ScrollView>
      )}
      <View style={styles.toRow}>
        <Pressable accessibilityRole="button" accessibilityLabel={t('chat.target.label', { target: sendTo ?? t('chat.target.lead') })} onPress={() => setPicking((p) => !p)} style={[styles.chip, sendTo && styles.chipOn]}>
          <Text style={styles.chipTo}>{t('chat.target.to')}</Text>
          <Text style={[styles.chipText, sendTo && styles.chipTextOn]} numberOfLines={1}>
            {sendTo ?? t('chat.target.lead')}
          </Text>
          <ChevronDown size={12} color={colors.muted} />
        </Pressable>
      </View>
      {bar}
    </View>
  );
}

const styles = StyleSheet.create({
  bar: { flexDirection: 'row', alignItems: 'flex-end', gap: 8, paddingHorizontal: 10, paddingTop: 8, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.lineStrong, backgroundColor: colors.overlay },
  input: { flex: 1, maxHeight: 140, minHeight: 40, color: colors.primary, backgroundColor: colors.surface, borderRadius: 20, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong, paddingHorizontal: 14, paddingTop: 10, paddingBottom: 10, fontSize: 15 },
  send: { width: 40, height: 40, borderRadius: 20, backgroundColor: colors.brand500, alignItems: 'center', justifyContent: 'center' },
  stop: { backgroundColor: colors.raised, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong },
  toRow: { flexDirection: 'row', paddingHorizontal: 10, paddingTop: 6, backgroundColor: colors.overlay, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.lineStrong },
  chip: { flexDirection: 'row', alignItems: 'center', gap: 4, maxWidth: 220, borderRadius: 12, paddingHorizontal: 10, paddingVertical: 4, backgroundColor: colors.surface },
  chipOn: { backgroundColor: colors.brandSoft },
  chipTo: { color: colors.faint, fontSize: 12 },
  chipText: { color: colors.muted, fontSize: 12.5, fontWeight: '600', flexShrink: 1 },
  chipTextOn: { color: colors.title },
  options: { flexGrow: 0, backgroundColor: colors.overlay, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.lineStrong },
  optionsRow: { flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: 10, paddingVertical: 8 },
  option: { borderRadius: 14, paddingHorizontal: 12, paddingVertical: 6, backgroundColor: colors.surface },
  optionOn: { backgroundColor: colors.brandSoft },
  optionText: { color: colors.muted, fontSize: 13 },
  optionTextOn: { color: colors.title, fontWeight: '600' },
  divider: { width: StyleSheet.hairlineWidth, height: 20, backgroundColor: colors.lineStrong, marginHorizontal: 4 },
  cant: { flex: 1, color: colors.subtle, fontSize: 12.5, lineHeight: 18, paddingVertical: 4 },
});
