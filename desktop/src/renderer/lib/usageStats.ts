// The app's share of the anonymous usage stats: uses only the app can see,
// like which tab was opened, counted by the daemon with the rest
// (internal/daemon/usagestats.go). A count carries its key and nothing else,
// and the daemon drops it when the stats are off.
import * as T from '../../shared/api';
import { api } from './api';

export type AppFeature =
  | typeof T.FeatureDesktopOpen
  | typeof T.FeatureTerminalOpen
  | typeof T.FeatureAndroidOpen
  | typeof T.FeatureAgentMediaView
  | typeof T.FeatureProjectMediaView
  | typeof T.FeaturePullList
  | typeof T.FeatureMemoryView
  | typeof T.FeatureTokensView
  | typeof T.FeatureSettingsEnvironment
  | typeof T.FeatureSettingsAccounts
  | typeof T.FeatureSettingsLead
  | typeof T.FeatureSettingsAgents
  | typeof T.FeatureSettingsGeneral
  | typeof T.FeatureSettingsModels
  | typeof T.FeatureSettingsResources
  | typeof T.FeatureSettingsProject
  | typeof T.FeatureSettingsSearch
  | typeof T.FeatureChatFind
  | typeof T.FeatureSearchOpen
  | typeof T.FeatureMenuOpenChat
  | typeof T.FeatureMenuOpenTerminal
  | typeof T.FeatureMenuInfo
  | typeof T.FeatureMenuLifecycle
  | typeof T.FeatureMenuRetire
  | typeof T.FeatureMenuCopyBranch
  | typeof T.FeatureMenuOpenPullRequest
  | typeof T.FeatureMenuDestroy;

// countFeature counts one use. A count that doesn't arrive is nobody's
// business, so it never shows an error.
export function countFeature(feature: AppFeature | undefined) {
  if (feature) void api.countFeature(feature).catch(() => {});
}

// The tabs whose opening is counted, of an agent and of a project.
export const agentTabFeatures: Record<string, AppFeature> = {
  browser: T.FeatureDesktopOpen,
  terminal: T.FeatureTerminalOpen,
  android: T.FeatureAndroidOpen,
  media: T.FeatureAgentMediaView,
};

export const projectTabFeatures: Record<string, AppFeature> = {
  pulls: T.FeaturePullList,
  media: T.FeatureProjectMediaView,
  memory: T.FeatureMemoryView,
  tokens: T.FeatureTokensView,
};

// The sections of Settings whose opening is counted. Setup keeps the key the
// Environment tab had, since it is the same checks; the lead's defaults moved
// into Models, so settings.view.lead is only counted by older apps now. Every
// project's settings count as one key: a project's name is never sent.
export const settingsSectionFeatures: Record<string, AppFeature> = {
  general: T.FeatureSettingsGeneral,
  models: T.FeatureSettingsModels,
  agents: T.FeatureSettingsAgents,
  resources: T.FeatureSettingsResources,
  accounts: T.FeatureSettingsAccounts,
  setup: T.FeatureSettingsEnvironment,
  project: T.FeatureSettingsProject,
};
