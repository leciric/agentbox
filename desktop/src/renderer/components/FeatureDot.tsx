// The dots of lib/featureDots.ts on screen: useFeatureDot is what an anchor
// asks, FeatureDot draws the dot, and dotLabel adds the hints to the anchor's
// own tooltip.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Fragment, useSyncExternalStore, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { dotAt, pending, rememberSince, since, type Anchor, type Feature } from '../lib/featureDots';
import { useT } from '../lib/i18n';
import { cn } from '../lib/utils';
import { openSettingsSection } from './SettingsPage';

let settled = since();
const listeners = new Set<() => void>();

// settleDots is App's, as the app learns its version and before What's new
// marks it seen.
export function settleDots(version: string): void {
  settled = rememberSince(version);
  listeners.forEach((l) => l());
}

const subscribe = (l: () => void) => (listeners.add(l), () => listeners.delete(l));

// usePendingFeatures is every feature still marked.
export function usePendingFeatures(): Feature[] {
  const from = useSyncExternalStore(subscribe, () => settled);
  const info = useQuery({ queryKey: ['app-info'], queryFn: () => window.agentbox.info(), staleTime: Infinity });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const current = info.data?.version;
  const seen = settings.data?.seenFeatures;
  // Until the daemon says what was used, nothing is marked: a dot that shows
  // and vanishes a moment later is worse than none.
  if (!current || !seen) return [];
  return pending({ since: from, current, seen });
}

export type FeatureDotState = {
  anchor: Anchor;
  on: boolean;
  // features are the anchor's own, then those inside it.
  features: Feature[];
  // clear marks the anchor's own features used; those inside it stay.
  clear: () => void;
};

export function useFeatureDot(anchor: Anchor): FeatureDotState {
  return dotOf(anchor, usePendingFeatures(), useSeeFeature());
}

// dotOf is useFeatureDot for a list that marks many anchors: one
// usePendingFeatures and one useSeeFeature for all of them.
export function dotOf(anchor: Anchor, marked: Feature[], see: (id: string) => void): FeatureDotState {
  const { own, inside } = dotAt(anchor, marked);
  return { anchor, on: own.length + inside.length > 0, features: [...own, ...inside], clear: () => own.forEach((f) => see(f.id)) };
}

// useSeeFeature clears a feature's dot, at once on screen, then for good
// in the daemon.
export function useSeeFeature(): (id: string) => void {
  const queryClient = useQueryClient();
  const see = useMutation({
    mutationFn: (id: string) => api.updateSettings({ seeFeature: id }),
    onMutate: (id) =>
      queryClient.setQueryData<T.Settings>(['settings'], (s) => (s && !s.seenFeatures.includes(id) ? { ...s, seenFeatures: [...s.seenFeatures, id] } : s)),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    // Not kept: the dot comes back with the next read of Settings, and goes
    // on the next use.
    onError: () => void queryClient.invalidateQueries({ queryKey: ['settings'] }),
  });
  return see.mutate;
}

// FeatureDot is the dot itself, in the corner of a `relative` anchor unless
// className places it.
export function FeatureDot({ dot, className }: { dot: FeatureDotState; className?: string }) {
  const t = useT();
  if (!dot.on) return null;
  return (
    <span
      className={cn('pointer-events-none absolute right-0.5 top-0.5 size-2 rounded-full bg-brand-400', className)}
      aria-label={t('featureDots.label')}
      data-feature-dot={dot.anchor}
    />
  );
}

// DotHints is what the dot's features add under the anchor's tooltip, or
// nothing when it has none.
export function DotHints({ dot }: { dot: FeatureDotState }) {
  const t = useT();
  if (!dot.on) return null;
  return (
    <span className="mt-1 grid gap-0.5" data-feature-hints>
      {dot.features.map((f) => (
        <span key={f.id} className="flex items-baseline gap-1.5 text-tertiary">
          <span className="size-1.5 shrink-0 translate-y-[-1px] rounded-full bg-brand-400" />
          {t(f.hint)}
        </span>
      ))}
    </span>
  );
}

// dotLabel is an anchor's tooltip with its dot's hints under it.
export function dotLabel(label: ReactNode, dot: FeatureDotState): ReactNode {
  if (!dot.on) return label;
  return (
    <Fragment>
      <span className="block text-secondary">{label}</span>
      <DotHints dot={dot} />
    </Fragment>
  );
}

// revealFeature shows where a feature lives, from What's new: its Settings
// section opened when that's where it is, then a pulse on its dot, or on a
// parent's when the anchor itself isn't on screen (a closed panel).
export function revealFeature(feature: Feature): void {
  if (feature.anchor.startsWith('settings:')) openSettingsSection(feature.anchor.slice('settings:'.length));
  // Two frames: Settings renders its section, and the dialog lets go of the page.
  requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      const visible = (a: string) => [...document.querySelectorAll<HTMLElement>(`[data-feature-dot="${a}"]`)].find((el) => el.offsetParent !== null);
      const el = [feature.anchor, ...(feature.parents ?? [])].map(visible).find(Boolean);
      if (!el) return;
      el.scrollIntoView({ block: 'nearest' });
      el.classList.remove('feature-dot-reveal');
      void el.offsetWidth;
      el.classList.add('feature-dot-reveal');
      setTimeout(() => el.classList.remove('feature-dot-reveal'), 2700);
    }),
  );
}
