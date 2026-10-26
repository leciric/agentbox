// The app's root: the paired daemon, its events, and the screens. A phone
// that isn't paired sees only pairing; a paired one, the rest
// (Stack.Protected). t3code's equivalent is apps/mobile/src/App.tsx with
// src/Stack.tsx; this app uses Expo Router's file routes instead of its
// hand-built native stack.
//
//   /                      projects and their leads and agents (features/home)
//   /pair                  pairing by QR code or pasted link (features/pairing)
//   /chat/<project>        a project's chat, its lead's (features/threads)
//   /chat/<project>/<name> an agent's chat
//   /review/<project>      ready to review, with media, and Merge (features/review)
//   /settings              this phone: its scopes, unpairing (features/settings)
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { Stack } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useMemo, useState } from 'react';
import { Platform, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { api } from '../connection/api.ts';
import { bootState, recordCrash, startBoot } from '../connection/boot.ts';
import { connect } from '../connection/events.ts';
import { loadSession, refreshPhone, unpair, useSession } from '../connection/session.ts';
import { usePushRegistration } from '../features/notifications/push.ts';
import { setLanguage, speakPhoneLanguage, useT } from '../lib/i18n.tsx';
import { ConnectionBar } from '../ui/ConnectionBar.tsx';
import { ErrorBoundary } from '../ui/ErrorBoundary.tsx';
import { Button, Note } from '../ui/parts.tsx';
import { colors, mono } from '../ui/theme.ts';

speakPhoneLanguage();
// Expo's web build: the page around the phone-wide column is dark too.
if (Platform.OS === 'web' && typeof document !== 'undefined') document.body.style.backgroundColor = colors.bg;
// How the last start ended comes first: after a crash on start, what was
// kept of the computer's data is cleared before anything is read (boot.ts).
void startBoot().then(loadSession);

// Paired: the events, the computer's language, the phone's scopes.
function Paired({ queryClient }: { queryClient: QueryClient }) {
  useEffect(() => connect(queryClient), [queryClient]);
  useEffect(() => {
    void refreshPhone();
  }, []);
  const language = useQuery({ queryKey: ['settings'], queryFn: api.settings }).data?.language;
  useEffect(() => {
    if (language) setLanguage(language);
  }, [language]);
  usePushRegistration();
  return <ConnectionBar />;
}

export default function RootLayout() {
  const session = useSession();
  // The session is read once the boot is known, so this render sees it.
  const boot = bootState();
  const [retried, setRetried] = useState(false);
  const safe = boot.mode === 'safe' && !retried;
  // One cache per daemon: nothing read from another, or before unpairing, is kept.
  const origin = session?.origin;
  const queryClient = useMemo(
    () => new QueryClient({ defaultOptions: { queries: { retry: 1, staleTime: 5_000, refetchOnWindowFocus: false } } }),
    // oxlint-disable-next-line react/exhaustive-deps -- a new cache for each daemon
    [origin],
  );
  if (session === undefined) return <View style={styles.root} />;
  if (safe && session) {
    return (
      <SafeAreaProvider>
        <StatusBar style="light" />
        <Crashed crashes={boot.crashes} error={boot.error} onRetry={() => setRetried(true)} />
      </SafeAreaProvider>
    );
  }
  return (
    <SafeAreaProvider>
      <QueryClientProvider client={queryClient}>
        <StatusBar style="light" />
        <ErrorBoundary label="app" resetKey={origin} onError={recordCrash} fallback={(error, retry) => <Crashed error={`${error.name}: ${error.message}`} onRetry={retry} />}>
          <View style={[styles.root, Platform.OS === 'web' && styles.webColumn]}>
            {session && <Paired key={session.origin} queryClient={queryClient} />}
            <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: colors.bg }, animation: 'slide_from_right' }}>
              <Stack.Protected guard={!!session}>
                <Stack.Screen name="index" />
                <Stack.Screen name="chat/[...ref]" />
                <Stack.Screen name="review/[project]" />
                <Stack.Screen name="settings" />
              </Stack.Protected>
              <Stack.Protected guard={!session}>
                <Stack.Screen name="pair" />
              </Stack.Protected>
            </Stack>
          </View>
        </ErrorBoundary>
      </QueryClientProvider>
    </SafeAreaProvider>
  );
}

// Crashed is what the app shows instead of crashing again: the error, and a
// way on that needs nothing from the computer. Retry starts as usual;
// unpairing is the way out when the computer's data keeps crashing it.
function Crashed({ crashes, error, onRetry }: { crashes?: number; error?: string; onRetry: () => void }) {
  const t = useT();
  return (
    <ScrollView style={styles.root} contentContainerStyle={styles.crashed}>
      <Text style={styles.crashedTitle}>{t('mobile.crash.title')}</Text>
      <Text style={styles.crashedBody}>{crashes ? t('mobile.crash.onStart', { count: crashes }) : t('mobile.crash.body')}</Text>
      {error ? (
        <Note tone="error">
          <Text style={mono}>{error}</Text>
        </Note>
      ) : null}
      <Button label={t('mobile.crash.retry')} onPress={onRetry} />
      <Button label={t('web.phone.unpair')} kind="danger" onPress={() => void unpair().then(onRetry)} />
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  crashed: { flexGrow: 1, justifyContent: 'center', gap: 14, padding: 24 },
  crashedTitle: { color: colors.title, fontSize: 20, fontWeight: '700' },
  crashedBody: { color: colors.muted, fontSize: 14, lineHeight: 20 },
  root: { flex: 1, backgroundColor: colors.bg },
  // Expo's web build, for development, in a phone's width.
  webColumn: { width: '100%', maxWidth: 430, alignSelf: 'center', borderLeftWidth: 1, borderRightWidth: 1, borderColor: colors.line },
});
