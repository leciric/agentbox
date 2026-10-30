// Push notifications, after t3code's apps/mobile/src/features/agent-awareness/
// (remoteRegistration.ts, notificationPermissions.ts,
// foregroundNotificationBehavior.ts, notificationResponseConsumer.ts).
//
// Once paired, the app asks for permission, gets the phone's Expo push token
// for the app's EAS project and registers it with the daemon (PUT /lan/push),
// beside which kinds this phone wants and its quiet hours, chosen in
// Settings. The daemon posts to Expo's push service itself
// (internal/daemon/phonepush.go): no hub, wherever the phone is. A tap opens
// what the notification is about (notificationTarget); one about the screen
// already open isn't shown over it.
//
// The EAS project is app.json's extra.eas.projectId, which `eas init` writes.
// Until it holds a real one the app can't get a token: Settings says so, keeps
// what is chosen, and its test notification is shown by the phone itself.
import { useEffect, useSyncExternalStore } from 'react';
import { Platform } from 'react-native';
import Constants from 'expo-constants';
import { getCalendars } from 'expo-localization';
import * as Notifications from 'expo-notifications';
import { router, usePathname } from 'expo-router';
import { api } from '../../connection/api.ts';
import { t } from '../../lib/i18n.tsx';
import { notificationTarget, sameScreen } from './target.ts';

export { notificationTarget } from './target.ts';

export type PushStatus = 'unavailable' | 'unconfigured' | 'denied' | 'pending' | 'registered' | 'failed';

let state: { status: PushStatus; error?: string } = { status: Platform.OS === 'web' ? 'unavailable' : 'pending' };
const listeners = new Set<() => void>();

function setState(status: PushStatus, error?: string) {
  state = { status, error };
  for (const fn of listeners) fn();
}

export function usePushStatus(): { status: PushStatus; error?: string } {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => state,
  );
}

// easProjectId is the app's EAS project, or undefined while app.json holds
// the placeholder.
export function easProjectId(): string | undefined {
  const extra = Constants.expoConfig?.extra as { eas?: { projectId?: unknown } } | undefined;
  const id = extra?.eas?.projectId ?? Constants.easConfig?.projectId;
  return typeof id === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id) ? id : undefined;
}

// timeZone is the phone's, which its quiet hours are read in.
export const timeZone = () => getCalendars()[0]?.timeZone ?? undefined;

// The screen open, which a notification about it isn't shown over.
let pathname = '';

if (Platform.OS !== 'web') {
  Notifications.setNotificationHandler({
    handleNotification: async (n) => {
      const show = !sameScreen(notificationTarget(n.request.content.data), pathname);
      return { shouldShowBanner: show, shouldShowList: show, shouldPlaySound: show, shouldSetBadge: false };
    },
  });
}

// registerPush asks for permission (when ask says it may), gets the push
// token and registers it with the daemon, keeping what this phone chose.
export async function registerPush(ask: boolean): Promise<void> {
  if (Platform.OS === 'web') return;
  try {
    if (Platform.OS === 'android') {
      await Notifications.setNotificationChannelAsync('default', { name: t('mobile.push.channel'), importance: Notifications.AndroidImportance.HIGH });
    }
    let perm = await Notifications.getPermissionsAsync();
    if (!perm.granted && perm.canAskAgain && ask) perm = await Notifications.requestPermissionsAsync();
    if (!perm.granted) return setState('denied');
    const projectId = easProjectId();
    if (!projectId) return setState('unconfigured');
    const { data: token } = await Notifications.getExpoPushTokenAsync({ projectId });
    const current = await api.push();
    await api.setPush({ ...current, token, timeZone: timeZone() ?? current.timeZone });
    setState('registered');
  } catch (err) {
    setState('failed', err instanceof Error ? err.message : String(err));
  }
}

// showHere shows a notification from the phone itself: the test one, when
// it couldn't be pushed. Expo's web build has the browser's.
export async function showHere(test: { title: string; body: string; data: Record<string, string> }): Promise<void> {
  if (Platform.OS === 'web') {
    if (typeof Notification === 'undefined') return;
    if (Notification.permission !== 'granted' && (await Notification.requestPermission()) !== 'granted') return;
    new Notification(test.title, { body: test.body });
    return;
  }
  await Notifications.scheduleNotificationAsync({ content: { title: test.title, body: test.body, data: test.data }, trigger: null });
}

function useNativePush(): PushStatus {
  const path = usePathname();
  useEffect(() => {
    pathname = path;
  }, [path]);
  useEffect(() => {
    void registerPush(true);
    // The token changes now and then (a restore, an update): register again.
    const sub = Notifications.addPushTokenListener(() => void registerPush(false));
    return () => sub.remove();
  }, []);
  // A tap, including the one that started the app.
  const response = Notifications.useLastNotificationResponse();
  useEffect(() => {
    if (!response || response.actionIdentifier !== Notifications.DEFAULT_ACTION_IDENTIFIER) return;
    const target = notificationTarget(response.notification.request.content.data);
    Notifications.clearLastNotificationResponse();
    if (target) router.push(target as never);
  }, [response]);
  return usePushStatus().status;
}

// usePushRegistration registers this phone for pushes once paired, and opens
// what a tapped notification is about. Expo's web build gets none.
export const usePushRegistration: () => PushStatus = Platform.OS === 'web' ? () => 'unavailable' : useNativePush;
