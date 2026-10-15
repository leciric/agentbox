// Push notifications: not in this version, only the places they'll hook in.
//
// What they will be, after t3code's apps/mobile/src/features/agent-awareness/
// (remoteRegistration.ts, notificationPermissions.ts,
// foregroundNotificationBehavior.ts, notificationResponseConsumer.ts):
// expo-notifications asks for permission, the phone's push token is
// registered with the daemon under the phone's pairing, and the daemon sends
// what the desktop already notifies (an agent finished, a question, a
// permission request) while the app is in the background; tapping one opens
// its chat (notificationTarget). Sending needs a relay that holds the push
// credentials, which is the hub's to decide, hence not yet.

export type PushStatus = 'unavailable' | 'unknown' | 'pending' | 'registered' | 'failed';

// usePushRegistration will register the phone for pushes once paired. For
// now it reports that they aren't available.
export function usePushRegistration(): PushStatus {
  return 'unavailable';
}

// notificationTarget is the screen a notification opens: its chat, from
// the ref it carries ("project" or "project/agent").
export function notificationTarget(data: { ref?: unknown } | undefined): string | undefined {
  const ref = typeof data?.ref === 'string' ? data.ref : undefined;
  return ref ? `/chat/${ref.split('/').map(encodeURIComponent).join('/')}` : undefined;
}
