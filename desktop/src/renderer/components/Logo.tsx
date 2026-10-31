import { Box } from 'lucide-react';
import { cn } from '../lib/utils';

// Logo is AgentBox's mark: the sidebar's, and the web app's sign-in page's.
export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn('brand-gradient relative flex size-8 items-center justify-center rounded-[10px] shadow-[0_8px_24px_-8px_rgb(139_92_246/0.8)]', className)}>
      <span className="absolute inset-px rounded-[9px] bg-gradient-to-b from-gloss to-transparent" />
      <Box className="relative size-[18px] text-white" strokeWidth={2.2} />
    </span>
  );
}
