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
import { useEffect, useMemo } from 'react';
import { Platform, StyleSheet, View } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { api } from '../connection/api.ts';
import { connect } from '../connection/events.ts';
import { loadSession, refreshPhone, useSession } from '../connection/session.ts';
import { usePushRegistration } from '../features/notifications/push.ts';
import { setLanguage, speakPhoneLanguage } from '../lib/i18n.tsx';
import { ConnectionBar } from '../ui/ConnectionBar.tsx';
import { colors } from '../ui/theme.ts';

speakPhoneLanguage();
// Expo's web build: the page around the phone-wide column is dark too.
if (Platform.OS === 'web' && typeof document !== 'undefined') document.body.style.backgroundColor = colors.bg;
void loadSession();

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
  // One cache per daemon: nothing read from another, or before unpairing, is kept.
  const origin = session?.origin;
  const queryClient = useMemo(
    () => new QueryClient({ defaultOptions: { queries: { retry: 1, staleTime: 5_000, refetchOnWindowFocus: false } } }),
    // oxlint-disable-next-line react/exhaustive-deps -- a new cache for each daemon
    [origin],
  );
  if (session === undefined) return <View style={styles.root} />;
  return (
    <SafeAreaProvider>
      <QueryClientProvider client={queryClient}>
        <StatusBar style="light" />
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
      </QueryClientProvider>
    </SafeAreaProvider>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bg },
  // Expo's web build, for development, in a phone's width.
  webColumn: { width: '100%', maxWidth: 430, alignSelf: 'center', borderLeftWidth: 1, borderRightWidth: 1, borderColor: colors.line },
});
