// Pairing, as the web phone app does it (desktop/src/renderer/web/PhonePair.tsx)
// but in the app: the QR code AgentBox shows on the computer (Settings, Phone,
// or `agentbox phone pair`) is scanned here with expo-camera, or its link
// pasted, and its one-time secret posted to the daemon for this phone's own
// token. After t3code's ConnectionsNewRouteScreen.tsx, which scans with
// CameraView and has a manual path beside it.
import { CameraView, useCameraPermissions } from 'expo-camera';
import { useRouter } from 'expo-router';
import { useRef, useState } from 'react';
import { KeyboardAvoidingView, Platform, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { isPlainHTTP, parsePairingLink } from '../../connection/pairing.ts';
import { pair } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { Button, Note } from '../../ui/parts.tsx';
import { colors, mono } from '../../ui/theme.ts';

export function PairScreen() {
  const t = useT();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const [permission, requestPermission] = useCameraPermissions();
  const [link, setLink] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const tried = useRef(new Set<string>());
  // The camera, on a phone; Expo's web build pastes.
  const camera = Platform.OS !== 'web';

  const tryPair = async (raw: string) => {
    if (busy) return;
    if (!parsePairingLink(raw)) {
      setError(t('mobile.pair.notALink'));
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await pair(raw);
      router.replace('/');
    } catch (err) {
      setError(t('web.pair.failed', { error: err instanceof Error ? err.message : String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const plain = parsePairingLink(link);
  const hl = (c: React.ReactNode) => <Text style={{ color: colors.primary }}>{c}</Text>;
  return (
    <KeyboardAvoidingView style={styles.root} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <ScrollView contentContainerStyle={[styles.content, { paddingTop: insets.top + 32, paddingBottom: insets.bottom + 24 }]} keyboardShouldPersistTaps="handled">
        <View style={styles.logo}>
          <Text style={styles.logoText}>A</Text>
        </View>
        <Text style={styles.title}>{t('mobile.pair.title')}</Text>
        <Text style={styles.body}>{t.rich('mobile.pair.steps', { hl })}</Text>
        <Text style={styles.body}>{t.rich('web.pair.terminal', { mono: (c) => <Text style={[mono, { color: colors.primary, fontSize: 13 }]}>{c}</Text> })}</Text>

        {camera &&
          (permission?.granted ? (
            <View style={styles.camera}>
              <CameraView
                style={StyleSheet.absoluteFill}
                facing="back"
                barcodeScannerSettings={{ barcodeTypes: ['qr'] }}
                onBarcodeScanned={({ data }) => {
                  // Each code once: a failed one isn't retried as long as it's in view.
                  if (tried.current.has(data)) return;
                  tried.current.add(data);
                  void tryPair(data);
                }}
              />
            </View>
          ) : (
            <View style={styles.gap}>
              {permission && !permission.canAskAgain && <Note tone="warning">{t('mobile.pair.cameraDenied')}</Note>}
              <Button label={t('mobile.pair.scan')} onPress={() => void requestPermission()} disabled={permission?.canAskAgain === false} />
            </View>
          ))}

        <View style={styles.gap}>
          <Text style={styles.label}>{t(camera ? 'mobile.pair.orPaste' : 'mobile.pair.paste')}</Text>
          <TextInput
            value={link}
            onChangeText={(text) => {
              setLink(text);
              setError(undefined);
            }}
            placeholder="http://192.168.1.20:7780/#pair=…"
            placeholderTextColor={colors.faint}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            style={styles.input}
            onSubmitEditing={() => void tryPair(link)}
            accessibilityLabel={t('mobile.pair.paste')}
          />
          <Button label={busy ? t('mobile.pair.pairing') : t('mobile.pair.connect')} busy={busy} disabled={!link.trim()} onPress={() => void tryPair(link)} />
        </View>
        {error && <Note tone="error">{error}</Note>}
        {plain && isPlainHTTP(plain.origin) && <Text style={styles.small}>{t('web.pair.plainHTTP')}</Text>}
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bg },
  content: { paddingHorizontal: 24, gap: 16, maxWidth: 480, width: '100%', alignSelf: 'center' },
  logo: { width: 48, height: 48, borderRadius: 16, backgroundColor: colors.raised, alignItems: 'center', justifyContent: 'center' },
  logoText: { color: colors.brand300, fontSize: 22, fontWeight: '700' },
  title: { color: colors.title, fontSize: 22, fontWeight: '600' },
  body: { color: colors.muted, fontSize: 14, lineHeight: 21 },
  camera: { height: 260, borderRadius: 18, overflow: 'hidden', backgroundColor: '#000' },
  gap: { gap: 10 },
  label: { color: colors.subtle, fontSize: 12, fontWeight: '600', textTransform: 'uppercase', letterSpacing: 1 },
  input: { color: colors.primary, backgroundColor: colors.surface, borderColor: colors.lineStrong, borderWidth: StyleSheet.hairlineWidth, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14 },
  small: { color: colors.subtle, fontSize: 12, lineHeight: 17 },
});
