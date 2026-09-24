import * as TabsPrimitive from '@radix-ui/react-tabs';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/utils';

export const Tabs = TabsPrimitive.Root;

export function TabsList({ className, ...props }: ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List
      className={cn('inline-flex items-center gap-0.5 rounded-xl border border-line bg-surface-faint p-1', className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      className={cn(
        'relative inline-flex h-8 shrink-0 items-center gap-2 whitespace-nowrap rounded-lg px-3 text-[13px] font-medium text-muted transition-all hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 data-[state=active]:bg-surface-strong data-[state=active]:text-title data-[state=active]:shadow-[inset_0_1px_0_rgb(255_255_255/0.08),0_4px_12px_-6px_var(--ab-shadow-deep)] [&_svg]:size-[15px]',
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({ className, ...props }: ComponentProps<typeof TabsPrimitive.Content>) {
  return <TabsPrimitive.Content className={cn('min-h-0 flex-1 focus-visible:outline-none', className)} {...props} />;
}
