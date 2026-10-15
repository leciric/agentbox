// The composer: a message to the chat, or Stop while its turn runs. Sending
// needs the phone's "chat" scope; a phone that may only read is told where
// to allow it. After t3code's ThreadComposer.tsx, without its attachments,
// commands and native editor, which come later.
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { Alert, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { api } from '../../connection/api.ts';
import { can, useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { colors } from '../../ui/theme.ts';
import { chatKey } from './thread.ts';

export function Composer({ chatRef, name, running, start }: { chatRef: string; name: string; running: boolean; start?: () => Promise<unknown> }) {
  const t = useT();
  const insets = useSafeAreaInsets();
  const session = useSession();
  const queryClient = useQueryClient();
  const [text, setText] = useState('');
  const send = useMutation({
    mutationFn: async (message: string) => {
      // A lead that never ran starts with its first message.
      if (start) await start();
      return api.sendMessage(chatRef, message);
    },
    onMutate: () => setText(''),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: chatKey(chatRef) }),
    onError: (err, message) => {
      setText(message);
      Alert.alert(t('mobile.chat.couldntSend', { error: err instanceof Error ? err.message : String(err) }));
    },
  });
  const stop = useMutation({ mutationFn: () => api.cancelChat(chatRef), onError: (err) => Alert.alert(err instanceof Error ? err.message : String(err)) });

  if (!can(session, 'chat')) {
    return (
      <View style={[styles.bar, { paddingBottom: insets.bottom + 10 }]}>
        <Text style={styles.cant}>{t('mobile.chat.cantChat')}</Text>
      </View>
    );
  }
  const empty = !text.trim();
  return (
    <View style={[styles.bar, { paddingBottom: insets.bottom + 8 }]}>
      <TextInput
        value={text}
        onChangeText={setText}
        placeholder={t('mobile.chat.placeholder', { name })}
        placeholderTextColor={colors.faint}
        style={styles.input}
        multiline
        accessibilityLabel={t('mobile.chat.placeholder', { name })}
      />
      {running && empty ? (
        <Pressable accessibilityRole="button" accessibilityLabel={t('mobile.chat.stop')} onPress={() => stop.mutate()} disabled={stop.isPending} style={[styles.send, styles.stop]}>
          <View style={styles.stopSquare} />
        </Pressable>
      ) : (
        <Pressable accessibilityRole="button" accessibilityLabel={t('mobile.chat.send')} onPress={() => send.mutate(text.trim())} disabled={empty || send.isPending} style={[styles.send, (empty || send.isPending) && { opacity: 0.4 }]}>
          <Text style={styles.arrow}>↑</Text>
        </Pressable>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  bar: { flexDirection: 'row', alignItems: 'flex-end', gap: 8, paddingHorizontal: 10, paddingTop: 8, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.lineStrong, backgroundColor: colors.overlay },
  input: { flex: 1, maxHeight: 140, minHeight: 40, color: colors.primary, backgroundColor: colors.surface, borderRadius: 20, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong, paddingHorizontal: 14, paddingTop: 10, paddingBottom: 10, fontSize: 15 },
  send: { width: 40, height: 40, borderRadius: 20, backgroundColor: colors.brand500, alignItems: 'center', justifyContent: 'center' },
  arrow: { color: colors.title, fontSize: 20, fontWeight: '700', marginTop: -2 },
  stop: { backgroundColor: colors.raised, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong },
  stopSquare: { width: 12, height: 12, borderRadius: 2, backgroundColor: colors.primary },
  cant: { flex: 1, color: colors.subtle, fontSize: 12.5, lineHeight: 18, paddingVertical: 4 },
});
