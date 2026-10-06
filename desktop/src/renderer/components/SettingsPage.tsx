import { ChevronRight, FolderGit2, Search, SearchX, X, type LucideIcon } from 'lucide-react';
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import * as T from '../../shared/api';
import { countFeature, settingsSectionFeatures } from '../lib/usageStats';
import { countEntries, search, splitAdvanced, type SettingEntry, type SettingGroup, type SettingSection } from '../lib/settingsSearch';
import { cn } from '../lib/utils';
import { Kbd, Notice, Panel } from './ui/card';
import { SettingsGroup } from './ui/settings';
import { useT } from '../lib/i18n';

// The Settings page once setup is done: a sidebar of sections, one open at a
// time, with a search across all of them above it. The installation's own
// sections come first — what applies to every project on this machine — then
// each project's, the way Linear puts a workspace's settings above its teams'
// and Zed keeps user settings apart from a project's. Which section holds what
// is SettingsView's to say (InstalledSettings); this is how it's drawn and
// searched.
//
// The sidebar is a column beside the section while the page is wide enough,
// and a row of chips above it when it isn't: a container query, since the page
// sits beside the app's own sidebar and a wide window can still leave it
// narrow.

export type SectionIcons = Record<string, LucideIcon>;

// The section open last, so coming back to Settings lands where you were.
const sectionKey = 'agentbox.settings.section';

// rememberSection makes id the section Settings opens at next, for a link
// from elsewhere in the app to a section of it.
export function rememberSection(id: string): void {
  localStorage.setItem(sectionKey, id);
}

export function SettingsPage({ sections, icons, error }: { sections: SettingSection[]; icons: SectionIcons; error?: string }) {
  const t = useT();
  const [stored, setStored] = useState(() => localStorage.getItem(sectionKey) ?? '');
  const [query, setQuery] = useState('');
  const main = useRef<HTMLDivElement>(null);
  const searchBox = useRef<HTMLInputElement>(null);
  // A section that's gone (a removed project, a section an older version
  // had) opens the first one instead.
  const open = sections.find((s) => s.id === stored) ?? sections[0];
  const select = (id: string) => {
    localStorage.setItem(sectionKey, id);
    setStored(id);
    setQuery('');
    main.current?.scrollTo({ top: 0 });
  };
  const found = useMemo(() => search(sections, query), [sections, query]);
  const searching = query.trim() !== '';

  const openId = open?.id;
  useEffect(() => {
    if (openId) countFeature(settingsSectionFeatures[openId.startsWith('project:') ? 'project' : openId]);
  }, [openId]);
  // A search is counted once per visit, when it's first typed.
  const counted = useRef(false);
  useEffect(() => {
    if (!searching || counted.current) return;
    counted.current = true;
    countFeature(T.FeatureSettingsSearch);
  }, [searching]);

  // "/" or Ctrl+F (⌘F) goes to the search from anywhere on the page, as they
  // do in a browser's and in Linear's settings.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const typing = target?.closest('input, textarea, select, [contenteditable="true"]');
      if ((event.key === 'f' && (event.ctrlKey || event.metaKey)) || (event.key === '/' && !typing)) {
        event.preventDefault();
        searchBox.current?.focus();
        searchBox.current?.select();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const changed = sections.reduce((n, s) => n + s.groups.reduce((m, g) => m + g.entries.filter((e) => e.modified).length, 0), 0);
  const foundIn = new Map(found.map((s) => [s.id, countEntries(s)]));
  const installation = sections.filter((s) => s.scope === 'installation');
  const projects = sections.filter((s) => s.scope === 'project');

  const navItem = (section: SettingSection) => {
    const Icon = icons[section.id] ?? FolderGit2;
    const hits = foundIn.get(section.id) ?? 0;
    const active = !searching && section.id === open?.id;
    return (
      <button
        key={section.id}
        type="button"
        data-settings-nav={section.id}
        aria-current={active ? 'page' : undefined}
        disabled={searching && hits === 0}
        onClick={() => {
          if (!searching) return select(section.id);
          document.getElementById(`settings-found-${section.id}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' });
        }}
        className={cn(
          'flex min-w-0 max-w-56 shrink-0 items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 @3xl:max-w-none @3xl:py-[7px]',
          active ? 'bg-surface-raised text-title' : 'text-muted hover:bg-surface hover:text-primary',
          searching && hits === 0 && 'opacity-40 hover:bg-transparent hover:text-muted',
        )}
      >
        <Icon className={cn('size-4 shrink-0', active ? 'text-brand-300' : 'text-subtle')} />
        <span className="min-w-0 truncate">{section.title}</span>
        {searching ? (
          hits > 0 && <span className="ml-auto rounded-full bg-surface-raised px-1.5 text-[10.5px] tabular-nums text-tertiary">{hits}</span>
        ) : (
          (section.attention ?? 0) > 0 && (
            <span
              className="ml-auto flex items-center gap-1 rounded-full bg-amber-400/15 px-1.5 text-[10.5px] font-medium tabular-nums text-amber-300"
              title={t('settings.page.toLookAt', { n: section.attention ?? 0 })}
            >
              {section.attention}
            </span>
          )
        )}
      </button>
    );
  };

  return (
    <div className="@container h-full">
      <div className="flex h-full min-h-0 flex-col @3xl:flex-row">
        <aside className="flex shrink-0 flex-col gap-3 border-b border-line px-3 pb-3 pt-4 @3xl:w-60 @3xl:overflow-y-auto @3xl:border-b-0 @3xl:border-r @3xl:pb-6 @3xl:pt-7">
          <h1 className="px-2 text-xl font-semibold tracking-tight text-title">{t('common.settings')}</h1>
          <div className="grid gap-1.5">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-subtle" />
              <input
                ref={searchBox}
                type="search"
                data-settings-search
                aria-label={t('settings.page.search')}
                placeholder={t('settings.page.search')}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Escape' && query) {
                    event.stopPropagation();
                    setQuery('');
                  }
                }}
                className="h-8 w-full rounded-lg border border-line-strong bg-sunken pl-8 pr-8 text-[13px] text-primary transition-colors placeholder:text-faint hover:border-line-vivid focus-visible:border-brand-400/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/25 [&::-webkit-search-cancel-button]:hidden"
              />
              {query ? (
                <button
                  type="button"
                  aria-label={t('settings.page.clearSearch')}
                  onClick={() => {
                    setQuery('');
                    searchBox.current?.focus();
                  }}
                  className="absolute right-1.5 top-1/2 flex size-5 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-primary"
                >
                  <X className="size-3.5" />
                </button>
              ) : (
                <span className="pointer-events-none absolute right-2 top-1/2 hidden -translate-y-1/2 @3xl:block">
                  <Kbd>/</Kbd>
                </span>
              )}
            </div>
            {changed > 0 && (
              <button
                type="button"
                data-settings-changed
                onClick={() => setQuery((q) => (/@changed/i.test(q) ? q.replace(/@changed/gi, '').trim() : `@changed ${q}`.trim()))}
                className={cn(
                  'flex w-fit items-center gap-1.5 rounded-md px-2 text-[11.5px] transition hover:text-primary',
                  /@changed/i.test(query) ? 'text-brand-300' : 'text-subtle',
                )}
              >
                <span className="h-2.5 w-0.5 rounded-full bg-brand-400" aria-hidden />
                {t('settings.page.changedFromDefault', { n: changed })}
              </button>
            )}
          </div>
          {/* Narrow, the sections are a row that scrolls sideways, faded at
              its right edge so the ones past it read as more to come. */}
          <nav
            aria-label={t('settings.page.sections')}
            className="-mx-3 flex gap-1 overflow-x-auto px-3 pb-0.5 [mask-image:linear-gradient(to_right,black_calc(100%-3rem),transparent)] [scrollbar-width:none] @3xl:mx-0 @3xl:grid @3xl:gap-0.5 @3xl:overflow-visible @3xl:px-0 @3xl:[mask-image:none]"
          >
            <NavLabel>{t('settings.page.installation')}</NavLabel>
            {installation.map(navItem)}
            {projects.length > 0 && (
              <>
                <span className="mx-1 w-px shrink-0 self-stretch bg-line @3xl:hidden" aria-hidden />
                <NavLabel className="@3xl:mt-4">{t('settings.page.projects')}</NavLabel>
                {projects.map(navItem)}
              </>
            )}
          </nav>
        </aside>

        <main ref={main} className="min-h-0 min-w-0 flex-1 overflow-y-auto" data-settings-main>
          <div className="mx-auto max-w-3xl px-4 py-6 @3xl:px-8 @3xl:py-8">
            {error && <Notice className="mb-5">{error}</Notice>}
            {searching ? (
              <SearchResults found={found} query={query} icons={icons} onOpen={select} />
            ) : (
              open && <SectionView key={open.id} section={open} icon={icons[open.id] ?? FolderGit2} />
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

function NavLabel({ className, children }: { className?: string; children: ReactNode }) {
  return <span className={cn('hidden px-2.5 pb-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-subtle @3xl:block', className)}>{children}</span>;
}

function SectionView({ section, icon: Icon }: { section: SettingSection; icon: LucideIcon }) {
  return (
    <div className="grid gap-8" data-settings-section={section.id}>
      <header className="grid gap-3">
        <div className="flex items-start gap-3">
          <span className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl border border-line-strong bg-overlay">
            <Icon className="size-[18px] text-brand-300" />
          </span>
          <div className="min-w-0">
            <h2 className="break-words text-lg font-semibold tracking-tight text-title">{section.title}</h2>
            <p className="mt-0.5 text-[13px] leading-relaxed text-muted">{section.description}</p>
          </div>
        </div>
        {section.header}
      </header>
      <SettingGroups groups={section.groups} />
      {section.footer}
    </div>
  );
}

// SettingGroups draws a section's groups, with its advanced settings folded
// into one group at the end. A project's Overview draws its settings with
// this too, so a setting looks the same on either page.
export function SettingGroups({ groups }: { groups: SettingGroup[] }) {
  const { groups: shown, advanced } = splitAdvanced(groups);
  return (
    <>
      {shown.map((group) => (
        <Group key={group.id} group={group} />
      ))}
      {advanced.length > 0 && <Advanced entries={advanced} />}
    </>
  );
}

function Group({ group }: { group: SettingGroup }) {
  if (group.cards) {
    return (
      <section className="grid gap-2.5" data-settings-group={group.id}>
        <GroupHeader title={group.title} description={group.description} />
        <div className="grid grid-cols-1 gap-3">
          {group.entries.map((entry) => (
            <Fragment key={entry.id}>{entry.render()}</Fragment>
          ))}
        </div>
      </section>
    );
  }
  return (
    <SettingsGroup title={group.title} description={group.description}>
      {group.entries.map((entry) => (
        <Entry key={entry.id} entry={entry} />
      ))}
    </SettingsGroup>
  );
}

function GroupHeader({ title, description }: { title: ReactNode; description?: ReactNode }) {
  return (
    <header className="px-1">
      <h2 className="text-[13px] font-semibold text-primary">{title}</h2>
      {description && <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">{description}</p>}
    </header>
  );
}

// Entry is one setting's row, marked at its edge when it's been changed from
// its default, the way VS Code marks a modified setting.
function Entry({ entry }: { entry: SettingEntry }) {
  const t = useT();
  return (
    <div className="relative" data-setting-id={entry.id} data-modified={entry.modified ? '' : undefined}>
      {entry.modified && (
        <span className="absolute inset-y-4 left-0 w-0.5 rounded-r-full bg-brand-400" title={t('settings.page.changed')}>
          <span className="sr-only">{t('settings.page.changed')}</span>
        </span>
      )}
      {entry.render()}
    </div>
  );
}

// Advanced is the settings most people never change, folded until asked for,
// as Raycast and VS Code keep theirs. It says how many it holds and how many
// of them are changed, so a change never hides behind the fold.
function Advanced({ entries }: { entries: SettingEntry[] }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const changed = entries.filter((e) => e.modified).length;
  return (
    <section className="grid gap-2.5" data-settings-advanced>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-fit items-center gap-1.5 rounded-md px-1 text-[13px] font-semibold text-primary transition hover:text-title focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
      >
        <ChevronRight className={cn('size-4 text-subtle transition-transform', open && 'rotate-90')} />
        {t('settings.page.advanced')}
        <span className="font-normal text-subtle">· {t(changed > 0 ? 'settings.page.advancedCountChanged' : 'settings.page.advancedCount', { count: entries.length, changed })}</span>
      </button>
      {open && (
        <Panel className="divide-y divide-line-faint">
          {entries.map((entry) => (
            <Entry key={entry.id} entry={entry} />
          ))}
        </Panel>
      )}
    </section>
  );
}

function SearchResults({ found, query, icons, onOpen }: { found: SettingSection[]; query: string; icons: SectionIcons; onOpen: (id: string) => void }) {
  const t = useT();
  const total = found.reduce((n, s) => n + countEntries(s), 0);
  if (total === 0) {
    return (
      <div className="grid justify-items-center gap-2 py-16 text-center" data-settings-results="0">
        <SearchX className="size-6 text-subtle" />
        <p className="text-sm text-secondary">{t('settings.page.noMatch', { query: query.trim() })}</p>
        <p className="max-w-sm text-[12.5px] leading-relaxed text-subtle">
          {t.rich('settings.page.noMatchHint', { code: (c) => <span className="font-mono text-tertiary">{c}</span> })}
        </p>
      </div>
    );
  }
  return (
    <div className="grid gap-10" data-settings-results={total}>
      <p className="px-1 text-[12.5px] text-subtle">
        {t('settings.page.found', { count: total })}
      </p>
      {found.map((section) => {
        const Icon = icons[section.id] ?? FolderGit2;
        return (
          <div key={section.id} id={`settings-found-${section.id}`} className="grid scroll-mt-6 gap-5">
            <button
              type="button"
              onClick={() => onOpen(section.id)}
              className="group flex min-w-0 max-w-full items-center gap-2 rounded-md text-left text-[15px] font-semibold text-title focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
            >
              <Icon className="size-4 shrink-0 text-brand-300" />
              {section.scope === 'project' && <span className="shrink-0 font-normal text-subtle">{t('settings.page.project')}</span>}
              <span className="min-w-0 truncate">{section.title}</span>
              <ChevronRight className="size-4 shrink-0 text-subtle transition group-hover:translate-x-0.5" />
            </button>
            {section.groups.map((group) => (
              <Group key={group.id} group={group} />
            ))}
          </div>
        );
      })}
    </div>
  );
}
