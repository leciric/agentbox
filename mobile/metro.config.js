// Metro, as Expo sets it up, plus the desktop app's shared folder: the API's
// types generated from Go (desktop/src/shared/api.ts) and the catalogs of the
// app's text (desktop/src/shared/i18n), which this app imports rather than
// copies (src/shared/).
const path = require('node:path');
const { getDefaultConfig } = require('expo/metro-config');

const config = getDefaultConfig(__dirname);
const shared = path.resolve(__dirname, '../desktop/src/shared');
config.watchFolders = [...(config.watchFolders ?? []), shared];
// What the shared files import resolves from this app's node_modules (they
// import nothing outside themselves today).
config.resolver.nodeModulesPaths = [path.resolve(__dirname, 'node_modules')];
module.exports = config;
