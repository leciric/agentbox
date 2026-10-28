// This phone's notifications, in Settings: whether it can get them, which
// kinds it wants, its quiet hours, and a test. Each change is saved with the
// daemon at once (PUT /lan/push), which is what decides what to push.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { Linking, Pressable, StyleSheet, Switch, Text, View } from 'react-native';
import * as T from '../../shared/api.ts';
import { api } from '../../connection/api.ts';
import { type MessageKey, useT } from '../../lib/i18n.tsx';
import { Button, Note } from '../../ui/parts.tsx';
import { colors, mono } from '../../ui/theme.ts';
import { showHere, timeZone, usePushStatus } from '../notifications/push.ts';
import { hhmm, shiftQuiet } from '../notifications/target.ts';

const kinds = [T.PushNeedsYou, T.PushPRReady, T.PushCIFailed, T.PushFinished];
// Quiet hours that are turned on start as a night: 22:00 to 07:00.
const night = { quietFrom: 22 * 60, quietTo: 7 * 60 };
const step = 30;

export function PushSettings() {
  const t = useT();
  const { status, error } = usePushStatus();
  const queryClient = useQueryClient();
  const push = useQuery({ queryKey: ['push'], queryFn: api.push });
  const save = useMutation({
    mutationFn: (next: T.PhonePush) => api.setPush({ ...next, token: undefined, timeZone: timeZone() ?? next.timeZone }),
    onMutate: (next) => queryClient.setQueryData(['push'], next),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['push'] }),
  });
  const [test, setTest] = useState<{ sent: boolean; error?: string } | null>(null);
  const sendTest = useMutation({
    mutationFn: api.testPush,
    onSuccess: async (res) => {
      setTest(res);
      if (!res.sent) await showHere(res);
    },
    onError: (err) => setTest({ sent: false, error: err.message }),
  });
  const p = push.data;
  const quiet = !!p && p.quietFrom !== p.quietTo;
  const set = (change: Partial<T.PhonePush>) => p && save.mutate({ ...p, ...change });
  return (
    <View style={styles.root}>
      <StatusNote status={status} error={error} />
      {push.error && <Note tone="error">{push.error.message}</Note>}
      {p && (
        <>
          <Text style={styles.sub}>{t('mobile.push.kinds')}</Text>
          {kinds.map((kind) => {
            const on = p.kinds.includes(kind);
            return (
              <View key={kind} style={styles.row}>
                <Text style={styles.rowText}>{t(`mobile.push.kind.${kind}` as MessageKey)}</Text>
                <Switch
                  accessibilityLabel={t(`mobile.push.kind.${kind}` as MessageKey)}
                  value={on}
                  onValueChange={(v) => set({ kinds: v ? kinds.filter((k) => k === kind || p.kinds.includes(k)) : p.kinds.filter((k) => k !== kind) })}
                  trackColor={{ true: colors.brand500, false: colors.lineStrong }}
                  thumbColor={colors.title}
                />
              </View>
            );
          })}
          <View style={[styles.row, { marginTop: 6 }]}>
            <Text style={styles.sub}>{t('mobile.push.quiet')}</Text>
            <Switch
              accessibilityLabel={t('mobile.push.quiet')}
              value={quiet}
              onValueChange={(v) => set(v ? night : { quietFrom: 0, quietTo: 0 })}
              trackColor={{ true: colors.brand500, false: colors.lineStrong }}
              thumbColor={colors.title}
            />
          </View>
          {quiet ? (
            <>
              <Bound label={t('mobile.push.from')} minutes={p.quietFrom} onChange={(m) => set({ quietFrom: m })} />
              <Bound label={t('mobile.push.to')} minutes={p.quietTo} onChange={(m) => set({ quietTo: m })} />
              <Text style={styles.small}>{t('mobile.push.quietOn', { from: hhmm(p.quietFrom), to: hhmm(p.quietTo) })}</Text>
            </>
          ) : (
            <Text style={styles.small}>{t('mobile.push.quietOff')}</Text>
          )}
          {save.error && <Note tone="error">{save.error.message}</Note>}
          <Button kind="secondary" label={t('mobile.push.test')} busy={sendTest.isPending} onPress={() => sendTest.mutate()} style={{ marginTop: 6 }} />
          {test && <Text style={styles.small}>{test.sent ? t('mobile.push.testSent') : t('mobile.push.testLocal', { error: test.error ?? '' })}</Text>}
        </>
      )}
    </View>
  );
}

function StatusNote({ status, error }: { status: string; error?: string }) {
  const t = useT();
  switch (status) {
    case 'registered':
      return <Text style={[styles.small, { color: colors.emerald }]}>{t('mobile.push.on')}</Text>;
    case 'pending':
      return <Text style={styles.small}>{t('mobile.push.pending')}</Text>;
    case 'denied':
      return (
        <>
          <Note tone="warning">{t('mobile.push.denied')}</Note>
          <Button kind="secondary" label={t('mobile.push.openSettings')} onPress={() => void Linking.openSettings()} />
        </>
      );
    case 'unconfigured':
      return <Note tone="warning">{t('mobile.push.unconfigured')}</Note>;
    case 'failed':
      return <Note tone="error">{t('mobile.push.failed', { error: error ?? '' })}</Note>;
    default:
      return <Text style={styles.small}>{t('mobile.push.unavailable')}</Text>;
  }
}

// Bound is one end of the quiet hours, moved half an hour at a time.
function Bound({ label, minutes, onChange }: { label: string; minutes: number; onChange: (m: number) => void }) {
  const t = useT();
  return (
    <View style={styles.row}>
      <Text style={styles.rowText}>{label}</Text>
      <View style={styles.stepper}>
        <Pressable accessibilityRole="button" accessibilityLabel={`${label}: ${t('mobile.push.earlier')}`} onPress={() => onChange(shiftQuiet(minutes, -step))} style={styles.stepButton}>
          <Text style={styles.stepText}>−</Text>
        </Pressable>
        <Text style={[mono, styles.time]}>{hhmm(minutes)}</Text>
        <Pressable accessibilityRole="button" accessibilityLabel={`${label}: ${t('mobile.push.later')}`} onPress={() => onChange(shiftQuiet(minutes, step))} style={styles.stepButton}>
          <Text style={styles.stepText}>+</Text>
        </Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { gap: 10 },
  sub: { color: colors.muted, fontSize: 13, fontWeight: '600', marginTop: 4 },
  row: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 12, minHeight: 36 },
  rowText: { color: colors.primary, fontSize: 14, flex: 1 },
  small: { color: colors.subtle, fontSize: 12.5, lineHeight: 18 },
  stepper: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  stepButton: { width: 34, height: 34, borderRadius: 8, backgroundColor: colors.surface, borderWidth: 1, borderColor: colors.lineStrong, alignItems: 'center', justifyContent: 'center' },
  stepText: { color: colors.title, fontSize: 18, lineHeight: 20 },
  time: { color: colors.title, fontSize: 15, minWidth: 52, textAlign: 'center' },
});
