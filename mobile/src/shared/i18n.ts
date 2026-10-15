// The app's text, from the desktop app's catalogs (desktop/src/shared/i18n):
// the phone's own messages are its mobile.* keys there, beside the web phone
// app's web.phone.* ones it reuses, so a language added to the desktop app is
// the phone's too. i18n.test.ts there fails when pt-BR lacks a key.
export * from '../../../desktop/src/shared/i18n/index.ts';
