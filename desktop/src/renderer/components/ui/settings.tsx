import type { HTMLAttributes, ReactNode } from 'react';
import { cn } from '../../lib/utils';
import { Panel } from './card';

// The one shape every setting in the app takes, on Settings and on a project's
// Overview alike: a group is a heading over one panel, and each setting in it
// is a row, divided from the next by a hairline rather than boxed in a panel of
// its own.

// SettingsGroup is a titled section: its heading and what the group has in
// common sit above the panel, so the rows inside only say what is their own.
export function SettingsGroup({
  title,
  description,
  className,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={cn('grid gap-2.5', className)}>
      <header className="px-1">
        <h2 className="text-[13px] font-semibold text-primary">{title}</h2>
        {description && <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">{description}</p>}
      </header>
      <Panel className="divide-y divide-line-faint">{children}</Panel>
    </section>
  );
}

// SettingRow is one setting: its name and what it means on the left, and its
// control in a column of the same width in every row, so the controls of a
// group line up and the sentences beside them wrap at the same place. A control
// too wide for that column — a text box with buttons, a long choice, a grid of
// fields — goes in children instead, full width under the description, where
// anything the row has to say after the fact (an error, a job) goes too.
export function SettingRow({
  label,
  htmlFor,
  description,
  control,
  children,
  className,
  ...props
}: {
  label: ReactNode;
  htmlFor?: string;
  description?: ReactNode;
  control?: ReactNode;
  children?: ReactNode;
} & Omit<HTMLAttributes<HTMLDivElement>, 'children'>) {
  const Label = htmlFor ? 'label' : 'div';
  return (
    <div className={cn('grid gap-3 px-5 py-4', className)} {...props}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:gap-6">
        <div className="min-w-0 flex-1">
          <Label htmlFor={htmlFor} className="block text-[13px] font-medium text-primary">
            {label}
          </Label>
          {description && <div className="mt-1 text-[12px] leading-relaxed text-subtle">{description}</div>}
        </div>
        {control && <div className="flex min-w-0 shrink-0 items-center sm:w-64 sm:justify-end">{control}</div>}
      </div>
      {children}
    </div>
  );
}

// SettingNote is a line a row adds under its control: how the choice stands
// now, a warning, or why saving it failed.
export function SettingNote({ tone = 'muted', className, children }: { tone?: 'muted' | 'warning' | 'error'; className?: string; children: ReactNode }) {
  return (
    <p
      role={tone === 'error' ? 'alert' : undefined}
      className={cn(
        'min-w-0 break-words text-[12px] leading-relaxed',
        { muted: 'text-subtle', warning: 'text-amber-300', error: 'text-rose-300' }[tone],
        className,
      )}
    >
      {children}
    </p>
  );
}
