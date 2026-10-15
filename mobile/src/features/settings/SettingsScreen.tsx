// This phone: the AgentBox it's paired with, what its scopes let it do (set
// on the computer, under Settings, Phone), notifications to come, and
// unpairing, which revokes it in AgentBox too.
import { Alert, ScrollView, StyleSheet, Text, View } from 'react-native';
import { isPlainHTTP } from '../../connection/pairing.ts';
import { unpair, useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { Button, Note, ScreenHeader } from '../../ui/parts.tsx';
import { colors, mono } from '../../ui/theme.ts';

const scopes = ['read', 'chat', 'answer', 'merge'] as const;

export function SettingsScreen() {
  const t = useT();
  const session = useSession();
  if (!session) return null;
  return (
    <View style={styles.root}>
      <ScreenHeader title={t('mobile.settings.title')} />
      <ScrollView contentContainerStyle={styles.content}>
        <Text style={styles.body}>{t.rich('web.phone.pairedAs', { name: <Text style={{ color: colors.secondary }}>{session.phone.name}</Text> })}</Text>
        <Text style={[mono, styles.origin]}>{session.origin}</Text>
        {isPlainHTTP(session.origin) && <Note>{t('web.pair.plainHTTP')}</Note>}
        <Text style={styles.label}>{t('mobile.settings.scopes')}</Text>
        {scopes.map((scope) => {
          const on = !!session.phone.scopes?.includes(scope);
          return (
            <View key={scope} style={styles.scope}>
              <Text style={[styles.scopeMark, { color: on ? colors.emerald : colors.faint }]}>{on ? '✓' : '–'}</Text>
              <Text style={[styles.scopeText, !on && { color: colors.subtle }]}>{t(`mobile.scope.${scope}`)}</Text>
            </View>
          );
        })}
        <Text style={styles.small}>{t('mobile.settings.scopesHint')}</Text>
        <Text style={styles.label}>{t('mobile.settings.notifications')}</Text>
        <Text style={styles.small}>{t('mobile.settings.notificationsLater')}</Text>
        <Button
          kind="danger"
          label={t('web.phone.unpair')}
          style={{ marginTop: 16 }}
          onPress={() =>
            Alert.alert(t('web.phone.unpair'), t('mobile.settings.unpairBody'), [
              { text: t('common.cancel'), style: 'cancel' },
              { text: t('web.phone.unpair'), style: 'destructive', onPress: () => void unpair() },
            ])
          }
        />
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bg },
  content: { padding: 18, gap: 10 },
  body: { color: colors.muted, fontSize: 14 },
  origin: { color: colors.subtle, fontSize: 12 },
  label: { color: colors.faint, fontSize: 11, fontWeight: '700', textTransform: 'uppercase', letterSpacing: 1.3, marginTop: 14 },
  scope: { flexDirection: 'row', gap: 10, alignItems: 'center' },
  scopeMark: { width: 16, fontSize: 14, fontWeight: '700' },
  scopeText: { color: colors.primary, fontSize: 14 },
  small: { color: colors.subtle, fontSize: 12.5, lineHeight: 18 },
});
