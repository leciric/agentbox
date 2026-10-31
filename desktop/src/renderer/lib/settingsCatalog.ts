// Every installation setting, by where it lives on the Settings page, for the
// command palette to find without drawing the page: Settings builds its
// sections inside InstalledSettings (components/SettingsView.tsx), from hooks,
// so this is the same list as data. settingsCatalog.test.ts reads
// SettingsView.tsx and fails when a setting there is missing here.
//
// A phone's and a project's settings come from phoneGroups() and
// projectSettingGroups(), which are plain functions, so they aren't repeated.
import type { MessageKey } from '../../shared/i18n/en-US.ts';

export type CatalogSetting = {
  // section and id are the page's: Settings opens at the section and brings
  // forward the row marked data-setting-id.
  section: string;
  id: string;
  group: MessageKey;
  label: MessageKey;
  keywords?: MessageKey;
  // advanced is folded at the end of its section until opened; the palette
  // opens the fold.
  advanced?: boolean;
  // v2 settings are only on the page under UI v2's layout.
  v2?: boolean;
};

export const sectionTitles: Record<string, MessageKey> = {
  general: 'settings.section.general.title',
  notifications: 'settings.section.notifications.title',
  phone: 'settings.section.phone.title',
  accounts: 'settings.section.accounts.title',
  models: 'settings.section.models.title',
  agents: 'settings.section.agents.title',
  skills: 'settings.section.skills.title',
  connectors: 'settings.section.connectors.title',
  memory: 'settings.section.memory.title',
  resources: 'settings.section.resources.title',
  setup: 'settings.section.setup.title',
  diagnostics: 'settings.section.diagnostics.title',
};

export const catalog: CatalogSetting[] = [
  { section: 'general', group: 'settings.group.appearance.title', id: 'appearance', label: 'settings.entry.appearance.label', keywords: 'settings.entry.appearance.keywords' },
  { section: 'general', group: 'settings.group.appearance.title', id: 'language', label: 'common.language', keywords: 'settings.languageKeywords' },
  { section: 'general', group: 'settings.group.appearance.title', id: 'layout', label: 'settings.entry.layout.label', keywords: 'settings.entry.layout.keywords' },
  { section: 'general', group: 'settings.group.appearance.title', id: 'group-idle', label: 'settings.entry.group-idle.label', keywords: 'settings.entry.group-idle.keywords', v2: true },
  { section: 'general', group: 'settings.group.appearance.title', id: 'environment', label: 'settings.entry.environment.label', keywords: 'settings.entry.environment.keywords', v2: true },
  { section: 'general', group: 'settings.group.working.title', id: 'reviewing', label: 'settings.entry.reviewing.label', keywords: 'settings.entry.reviewing.keywords' },
  { section: 'general', group: 'settings.group.working.title', id: 'topbar-detailed', label: 'settings.entry.topbar-detailed.label', keywords: 'settings.entry.topbar-detailed.keywords', advanced: true },
  { section: 'general', group: 'settings.group.working.title', id: 'pages-stack', label: 'settings.entry.pages-stack.label', keywords: 'settings.entry.pages-stack.keywords', advanced: true },
  { section: 'general', group: 'settings.group.commits.title', id: 'credit-ai-tool', label: 'settings.entry.credit-ai-tool.label', keywords: 'settings.entry.credit-ai-tool.keywords' },
  { section: 'general', group: 'settings.group.updates.title', id: 'update-channel', label: 'settings.entry.update-channel.label', keywords: 'settings.entry.update-channel.keywords' },
  { section: 'general', group: 'settings.group.updates.title', id: 'whats-new', label: 'settings.entry.whats-new.label', keywords: 'settings.entry.whats-new.keywords' },
  { section: 'general', group: 'settings.group.updates.title', id: 'report-problem', label: 'settings.entry.report-problem.label', keywords: 'settings.entry.report-problem.keywords' },
  { section: 'general', group: 'settings.group.updates.title', id: 'update-check', label: 'settings.entry.update-check.label', keywords: 'settings.entry.update-check.keywords', advanced: true },
  { section: 'general', group: 'settings.group.privacy.title', id: 'usage-stats', label: 'settings.entry.usage-stats.label', keywords: 'settings.entry.usage-stats.keywords' },
  { section: 'general', group: 'settings.group.privacy.title', id: 'error-reports', label: 'settings.entry.error-reports.label', keywords: 'settings.entry.error-reports.keywords' },
  { section: 'notifications', group: 'settings.group.notifications.title', id: 'notify-dnd', label: 'notify.dnd.title', keywords: 'notify.dnd.keywords' },
  { section: 'notifications', group: 'settings.group.notifications.title', id: 'notify-kinds', label: 'settings.entry.notify-kinds.label', keywords: 'settings.entry.notify-kinds.keywords' },
  { section: 'notifications', group: 'settings.group.notifications.title', id: 'notify-volume', label: 'settings.reviewSound.volume', keywords: 'settings.entry.review-sound.keywords' },
  // Accounts' rows are Setup's checks, one per AI tool and GitHub.
  { section: 'accounts', group: 'settings.group.ai.title', id: 'claude', label: 'settings.setup.title.claude', keywords: 'settings.setup.desc.claude' },
  { section: 'accounts', group: 'settings.group.ai.title', id: 'codex', label: 'settings.setup.title.codex', keywords: 'settings.setup.desc.codex' },
  { section: 'accounts', group: 'settings.group.ai.title', id: 'opencode', label: 'settings.setup.title.opencode', keywords: 'settings.setup.desc.opencode' },
  { section: 'accounts', group: 'settings.group.ai.title', id: 'cursor', label: 'settings.setup.title.cursor', keywords: 'settings.setup.desc.cursor' },
  { section: 'accounts', group: 'settings.group.github.title', id: 'github', label: 'settings.setup.title.github', keywords: 'settings.setup.desc.github' },
  { section: 'models', group: 'settings.group.new-agents.title', id: 'agent-model', label: 'settings.entry.agent-model.label', keywords: 'settings.entry.agent-model.keywords' },
  { section: 'models', group: 'settings.group.new-agents.title', id: 'agent-window', label: 'settings.entry.agent-window.label', keywords: 'settings.entry.agent-window.keywords' },
  { section: 'models', group: 'settings.group.new-agents.title', id: 'agent-effort', label: 'settings.entry.agent-effort.label', keywords: 'settings.entry.agent-effort.keywords' },
  { section: 'models', group: 'settings.group.new-agents.title', id: 'cursor-model', label: 'settings.entry.cursor-model.label', keywords: 'settings.entry.cursor-model.keywords', advanced: true },
  { section: 'models', group: 'settings.group.new-agents.title', id: 'cursor-effort', label: 'settings.entry.cursor-effort.label', keywords: 'settings.entry.cursor-effort.keywords', advanced: true },
  { section: 'models', group: 'settings.group.new-agents.title', id: 'enforce', label: 'settings.entry.enforce.label', keywords: 'settings.entry.enforce.keywords', advanced: true },
  { section: 'models', group: 'settings.group.lead.title', id: 'lead-model', label: 'settings.entry.lead-model.label', keywords: 'settings.entry.lead-model.keywords' },
  { section: 'models', group: 'settings.group.lead.title', id: 'lead-window', label: 'settings.entry.lead-window.label', keywords: 'settings.entry.lead-window.keywords' },
  { section: 'models', group: 'settings.group.tool-versions.title', id: 'claude-code-update', label: 'settings.entry.claude-code-update.label', keywords: 'settings.entry.claude-code-update.keywords' },
  { section: 'models', group: 'settings.group.chats.title', id: 'compact', label: 'settings.entry.compact.label', keywords: 'settings.entry.compact.keywords', advanced: true },
  { section: 'models', group: 'settings.group.chats.title', id: 'resume', label: 'settings.entry.resume.label', keywords: 'settings.entry.resume.keywords' },
  { section: 'models', group: 'settings.group.chats.title', id: 'continue-after-restart', label: 'settings.entry.continueAfterRestart.label', keywords: 'settings.entry.continueAfterRestart.keywords' },
  { section: 'agents', group: 'settings.group.pulls.title', id: 'pr-watch', label: 'settings.entry.pr-watch.label', keywords: 'settings.entry.pr-watch.keywords' },
  { section: 'agents', group: 'settings.group.recheck.title', id: 'lead-recheck', label: 'settings.entry.lead-recheck.label', keywords: 'settings.entry.lead-recheck.keywords' },
  { section: 'agents', group: 'settings.group.lifecycle.title', id: 'auto-stop', label: 'settings.entry.auto-stop.label', keywords: 'settings.entry.auto-stop.keywords' },
  { section: 'agents', group: 'settings.group.machines.title', id: 'agent-gpu', label: 'settings.entry.agent-gpu.label', keywords: 'settings.entry.agent-gpu.keywords' },
  { section: 'skills', group: 'settings.group.skills.title', id: 'skills', label: 'settings.section.skills.title', keywords: 'settings.entry.skills.keywords' },
  { section: 'connectors', group: 'settings.group.connectors.title', id: 'connectors', label: 'settings.section.connectors.title', keywords: 'settings.entry.connectors.keywords' },
  { section: 'memory', group: 'settings.group.memory.title', id: 'global-memory', label: 'settings.section.memory.title', keywords: 'settings.entry.memory.keywords' },
  { section: 'resources', group: 'settings.group.disk.title', id: 'disk-floor', label: 'settings.entry.disk-floor.label', keywords: 'settings.entry.disk-floor.keywords' },
  { section: 'resources', group: 'settings.group.disk.title', id: 'media-retention', label: 'settings.entry.media-retention.label', keywords: 'settings.entry.media-retention.keywords' },
  { section: 'resources', group: 'settings.group.disk.title', id: 'docker-prune', label: 'settings.entry.docker-prune.label', keywords: 'settings.entry.docker-prune.keywords', advanced: true },
  { section: 'resources', group: 'settings.group.disk.title', id: 'docker-image-cache', label: 'settings.entry.docker-image-cache.label', keywords: 'settings.entry.docker-image-cache.keywords', advanced: true },
  { section: 'resources', group: 'settings.group.disk.title', id: 'package-caches', label: 'settings.entry.package-caches.label', keywords: 'settings.entry.package-caches.keywords', advanced: true },
  { section: 'resources', group: 'settings.group.vm.title', id: 'vm-size', label: 'settings.entry.vm-size.label', keywords: 'settings.entry.vm-size.keywords' },
  { section: 'resources', group: 'settings.group.vm.title', id: 'vm-swap', label: 'settings.entry.vm-swap.label', keywords: 'settings.entry.vm-swap.keywords' },
  { section: 'setup', group: 'settings.group.where.title', id: 'move-to-vm', label: 'settings.entry.move-to-vm.label', keywords: 'settings.setup.moveKeywords' },
  { section: 'setup', group: 'settings.checklist.title', id: 'checklist', label: 'settings.checklist.title' },
  { section: 'setup', group: 'settings.group.optional.title', id: 'android', label: 'settings.setup.title.android', keywords: 'settings.setup.desc.android' },
  { section: 'setup', group: 'settings.group.optional.title', id: 'preview', label: 'settings.setup.title.preview', keywords: 'settings.setup.desc.preview' },
  { section: 'setup', group: 'settings.group.image.title', id: 'opencode-image', label: 'settings.entry.opencode-image.label', keywords: 'settings.entry.opencode-image.keywords', advanced: true },
  { section: 'diagnostics', group: 'settings.group.diagnostics.title', id: 'diagnostics', label: 'settings.section.diagnostics.title', keywords: 'settings.entry.diagnostics.keywords' },
];
