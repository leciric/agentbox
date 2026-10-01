import type { LucideIcon } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../lib/utils';

export type SettingsSection<Id extends string> = { id: Id; title: string; icon: LucideIcon };

// The Settings tab of a project or an agent: its sections in a column beside
// the open one, drawn like the app's own Settings page, and a row of chips
// above it once the page is too narrow for a column. Each section is the panel
// that used to be a tab of its own, scrolling on its own, and only the open one
// is mounted, so a section's queries run while it's on screen, as they did.
export function SettingsSections<Id extends string>({
  label,
  sections,
  value,
  onValueChange,
  children,
}: {
  label: string;
  sections: SettingsSection<Id>[];
  value: Id;
  onValueChange: (id: Id) => void;
  children: ReactNode;
}) {
  return (
    <div className="@container flex h-full min-h-0 flex-col">
      <div className="flex min-h-0 flex-1 flex-col @3xl:flex-row">
        <nav
          aria-label={label}
          className="flex shrink-0 gap-1 overflow-x-auto px-3 py-2 [scrollbar-width:none] @3xl:w-48 @3xl:flex-col @3xl:gap-0.5 @3xl:overflow-visible @3xl:border-r @3xl:border-line-faint @3xl:py-4"
        >
          {sections.map(({ id, title, icon: Icon }) => {
            const active = id === value;
            return (
              <button
                key={id}
                type="button"
                data-settings-tab-section={id}
                aria-current={active ? 'page' : undefined}
                onClick={() => onValueChange(id)}
                className={cn(
                  'flex shrink-0 items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 @3xl:py-[7px]',
                  active ? 'bg-surface-raised text-title' : 'text-muted hover:bg-surface hover:text-primary',
                )}
              >
                <Icon className={cn('size-4 shrink-0', active ? 'text-brand-300' : 'text-subtle')} />
                <span className="min-w-0 truncate">{title}</span>
              </button>
            );
          })}
        </nav>
        <div key={value} className="min-h-0 min-w-0 flex-1 overflow-y-auto" data-settings-tab-open={value}>
          {children}
        </div>
      </div>
    </div>
  );
}
