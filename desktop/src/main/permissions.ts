import { session } from 'electron';

// denyMedia refuses the camera, the microphone and screen capture to the app's
// own page: it uses none. Every other permission is granted as Electron grants
// it with no handler at all.
export function denyMedia(): void {
  session.defaultSession.setPermissionRequestHandler((_contents, permission, callback) => callback(permission !== 'media'));
}
