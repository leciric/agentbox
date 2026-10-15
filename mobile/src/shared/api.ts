// The daemon's API types, generated from Go into the desktop app's
// shared/api.ts (UPDATE_TS=1 go test ./internal/api). The phone app imports
// that same file rather than a copy: Metro watches the folder
// (metro.config.js), so there's one source of truth and nothing to regenerate.
export * from '../../../desktop/src/shared/api.ts';
