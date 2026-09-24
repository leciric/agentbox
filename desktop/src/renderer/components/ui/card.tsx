import { CircleAlert, Info, TriangleAlert } from 'lucide-react';
import type { ComponentType, HTMLAttributes, ReactNode } from 'react';
import { cn } from '../../lib/utils';

type Icon = ComponentType<{ className?: string }>;

export function Panel({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('panel rounded-2xl', className)} {...props} />;
}

export function Card({
  title,
  description,
  icon: Icon,
  action,
  className,
  bodyClassName,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  icon?: Icon;
  action?: ReactNode;
  className?: string;
  bodyClassName?: string;
  children: ReactNode;
}) {
  return (
    <section className={cn('panel rounded-2xl', className)}>
      <header className="flex items-center gap-3 px-5 pb-1 pt-4">
        {Icon && (
          <span className="flex size-7 items-center justify-center rounded-lg bg-surface text-muted ring-1 ring-inset ring-line">
            <Icon className="size-4" />
          </span>
        )}
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-primary">{title}</h3>
          {description && <p className="text-xs text-subtle">{description}</p>}
        </div>
        {action && <div className="ml-auto flex items-center gap-1">{action}</div>}
      </header>
      <div className={cn('px-5 pb-5 pt-3', bodyClassName)}>{children}</div>
    </section>
  );
}

export function Row({ label, mono, children }: { label: string; mono?: boolean; children: ReactNode }) {
  return (
    <div className="flex min-h-9 items-center gap-4 border-t border-line-faint text-sm first:border-t-0">
      <span className="w-28 shrink-0 text-[13px] text-subtle">{label}</span>
      <span className={cn('flex min-w-0 flex-1 items-center gap-1.5 text-secondary', mono && 'font-mono text-[12.5px]')}>{children}</span>
    </div>
  );
}

const tones = {
  danger: { className: 'border-rose-500/25 bg-rose-500/[0.08] text-rose-100', icon: CircleAlert, iconClass: 'text-rose-300' },
  warning: { className: 'border-amber-400/25 bg-amber-400/[0.07] text-amber-50', icon: TriangleAlert, iconClass: 'text-amber-300' },
  info: { className: 'border-sky-400/20 bg-sky-400/[0.06] text-sky-50', icon: Info, iconClass: 'text-sky-300' },
};

export function Notice({ tone = 'danger', children, className }: { tone?: keyof typeof tones; children: ReactNode; className?: string }) {
  const { className: toneClass, icon: Icon, iconClass } = tones[tone];
  return (
    <div role={tone === 'danger' ? 'alert' : 'status'} className={cn('flex items-start gap-2.5 rounded-xl border px-3.5 py-2.5 text-[13px]', toneClass, className)}>
      <Icon className={cn('mt-px size-4 shrink-0', iconClass)} />
      <div className="min-w-0 break-words leading-relaxed">{children}</div>
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded-md border border-line-strong bg-surface-raised px-1.5 py-px font-mono text-[10.5px] text-tertiary shadow-[inset_0_-1px_0_rgb(255_255_255/0.06)]">
      {children}
    </kbd>
  );
}

export function Code({ children, className }: { children: ReactNode; className?: string }) {
  return <code className={cn('rounded-md bg-surface-raised px-1.5 py-px font-mono text-[12px] text-secondary', className)}>{children}</code>;
}

export function EmptyState({ icon: Icon, title, children, action }: { icon: Icon; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="grid max-w-md animate-slide-up justify-items-center gap-3 text-center">
        <div className="relative mb-2">
          <div className="brand-gradient absolute inset-0 rounded-2xl opacity-30 blur-xl" />
          <div className="relative flex size-14 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
            <Icon className="size-6 text-brand-300" />
          </div>
        </div>
        <h2 className="text-lg font-semibold text-title">{title}</h2>
        {children && <div className="text-sm leading-relaxed text-muted">{children}</div>}
        {action && <div className="mt-2 flex flex-wrap justify-center gap-2">{action}</div>}
      </div>
    </div>
  );
}
