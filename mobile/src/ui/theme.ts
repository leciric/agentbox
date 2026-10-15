// The desktop app's dark palette (desktop/src/renderer/styles.css, --ab-*),
// which the phone shares. The phone is dark only for now.
import type { StatusTone } from '../features/threads/activity.ts';

export const colors = {
  bg: '#0b0b10',
  overlay: 'rgba(24,24,27,0.95)',
  surface: 'rgba(255,255,255,0.05)',
  raised: 'rgba(255,255,255,0.065)',
  line: 'rgba(255,255,255,0.065)',
  lineStrong: 'rgba(255,255,255,0.1)',
  title: '#fafafa',
  primary: '#f4f4f5',
  secondary: '#e4e4e7',
  muted: '#a1a1aa',
  subtle: '#71717a',
  faint: '#52525b',
  brand300: '#c4b5fd',
  brand400: '#a78bfa',
  brand500: '#8b5cf6',
  brandSoft: 'rgba(139,92,246,0.15)',
  amber: '#fcd34d',
  amberSoft: 'rgba(245,158,11,0.12)',
  rose: '#fda4af',
  roseSoft: 'rgba(244,63,94,0.12)',
  emerald: '#6ee7b7',
  emeraldSoft: 'rgba(16,185,129,0.15)',
};

export const toneText: Record<StatusTone, string> = {
  urgent: colors.amber,
  error: colors.rose,
  warning: 'rgba(252,211,77,0.8)',
  live: colors.emerald,
  muted: colors.subtle,
};

export const toneDot: Record<StatusTone, string> = {
  urgent: '#fbbf24',
  error: '#fb7185',
  warning: 'rgba(252,211,77,0.8)',
  live: '#34d399',
  muted: colors.faint,
};

export const mono = { fontFamily: 'Menlo, ui-monospace, monospace' };
