import { cn } from '../lib/utils';

// A sparkline of an agent's last ~20 CPU samples: motion tied to something
// real, so a list full of idle agents doesn't read as busier than it is.
export function Sparkline({ values, className }: { values: number[]; className?: string }) {
  const w = 36;
  const h = 12;
  if (values.length < 2) return <svg width={w} height={h} className={cn('shrink-0', className)} aria-hidden />;
  const max = Math.max(20, ...values);
  const points = values.map((v, i) => `${(i / (values.length - 1)) * w},${h - (Math.max(0, v) / max) * h}`).join(' ');
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className={cn('shrink-0 overflow-visible', className)} aria-hidden>
      <polyline points={points} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
