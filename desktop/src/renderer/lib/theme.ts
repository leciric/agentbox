// AgentBox wearing the theme of the desktop it runs on, and being light or
// dark.
//
// The daemon is the half that can read the desktop's theme — it runs on the
// host, beside the user's Omarchy configuration — so it reports one small
// palette over /v1/theme, along with the appearance setting, and publishes an
// event when either changes. Everything this file does is turn that into the
// custom properties styles.css reaches every colour through: the accent's four
// shades and its washes, and one attribute on <html> that says which way round
// the window is. Nothing else in the app knows a theme exists.
import { useQuery } from '@tanstack/react-query';
import { useEffect, useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';
import { api } from './api.ts';

// The properties styles.css declares defaults for. Setting one here overrides
// that default for the whole window; clearing it puts AgentBox's own back.
const properties = ['--ab-brand-300', '--ab-brand-400', '--ab-brand-500', '--ab-brand-600', '--ab-ink', '--ab-wash-near', '--ab-wash-far', '--ab-selection', '--ab-brand-mid', '--ab-brand-deep', '--ab-brand-far'] as const;

type Rgb = [number, number, number];

function parse(hex: string): Rgb | null {
  const match = /^#([0-9a-f]{6})$/i.exec(hex.trim());
  if (!match) return null;
  const n = Number.parseInt(match[1], 16);
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
}

const hex = ([r, g, b]: Rgb) => `#${[r, g, b].map((c) => Math.round(Math.min(255, Math.max(0, c))).toString(16).padStart(2, '0')).join('')}`;

// mix moves a colour towards another by amount, 0 to 1. The app needs four
// shades of one accent and a theme supplies one, so the other three are mixed
// here rather than asked of the theme: a palette small enough to be honest
// about is worth more than four colours a theme may not have thought about.
const mix = (from: Rgb, to: Rgb, amount: number): Rgb => [0, 1, 2].map((i) => from[i] + (to[i] - from[i]) * amount) as Rgb;

const alpha = ([r, g, b]: Rgb, a: number) => `rgb(${r} ${g} ${b} / ${a})`;

const white: Rgb = [255, 255, 255];
const black: Rgb = [0, 0, 0];

// Mode is which way round the window is painted. It is not the same question
// as which theme the accent comes from: the app can be light while following a
// light Omarchy theme, or light because someone said so and the desktop is
// dark.
export type Mode = 'dark' | 'light';

// modeOf is the mode the app should be in. Following the host means taking the
// theme's, and pinning means what was pinned. A machine with no theme to
// follow, or a theme that says nothing about its mode, is dark: that is
// AgentBox's own look.
export function modeOf(theme: T.Theme | undefined): Mode {
  if (!theme) return 'dark';
  if (theme.appearance === 'light' || theme.appearance === 'dark') return theme.appearance;
  if (theme.available && theme.mode === 'light') return 'light';
  return 'dark';
}

// followed says the host's own palette is what the app should wear: the
// setting is "follow" and there is a theme on this machine to follow.
const followed = (theme: T.Theme | undefined): boolean => theme?.appearance === 'follow' && theme.available === true;

// palette is the properties a theme resolves to, or null when it resolves to
// nothing usable — an accent that isn't a colour, or a theme that isn't being
// followed. A null is AgentBox's own accent, which is what the stylesheet has,
// in whichever mode the window is in.
export function palette(theme: T.Theme | undefined): Record<string, string> | null {
  if (!followed(theme) || !theme) return null;
  const accent = parse(theme.accent);
  if (!accent) return null;

  // The window's own background, a shade further from the text than the theme
  // puts it so panels still lift off it — deeper under a dark theme, paler
  // under a light one.
  const light = theme.mode === 'light';
  const background = parse(theme.background);
  const ink = background ? hex(mix(background, light ? white : black, 0.35)) : null;

  return {
    '--ab-brand-300': hex(mix(accent, white, 0.35)),
    '--ab-brand-400': hex(mix(accent, white, 0.18)),
    '--ab-brand-500': hex(accent),
    '--ab-brand-600': hex(mix(accent, black, 0.18)),
    ...(ink ? { '--ab-ink': ink } : {}),
    '--ab-wash-near': alpha(accent, light ? 0.12 : 0.16),
    '--ab-wash-far': alpha(mix(accent, light ? black : white, 0.3), light ? 0.08 : 0.07),
    '--ab-selection': alpha(accent, light ? 0.28 : 0.35),
    // The companion hues AgentBox's gradients run through — its wordmark, its
    // primary button, the tiles. A theme names one accent, and a gradient
    // left running through two unrelated hues would be AgentBox's palette
    // wearing a theme's badge, so these are shades of the accent too.
    '--ab-brand-mid': hex(mix(accent, black, 0.28)),
    '--ab-brand-deep': hex(mix(accent, black, 0.42)),
    '--ab-brand-far': hex(mix(accent, white, 0.4)),
  };
}

// Where the mode is remembered between windows. The daemon owns the setting,
// but the app has to paint before it can ask: without this the window opens
// dark and turns over a moment later, and in a browser the sign-in page — which
// runs before there is an environment to ask at all — would always be dark.
const remembered = 'agentbox.appearance';

// restoreMode paints the window in the mode it was last in, before anything
// renders. Call it once, from the entry point, above createRoot.
export function restoreMode(): void {
  try {
    if (localStorage.getItem(remembered) === 'light') setMode('light');
  } catch {
    // A browser that refuses storage: the window opens dark, as it always did.
  }
}

// setMode is the one place the window turns over: the attribute styles.css
// keys its light block on, the colour the phone paints its chrome in, and the
// listeners below for the parts built in JavaScript rather than in CSS.
function setMode(next: Mode): void {
  const root = document.documentElement;
  if (next === 'light') root.setAttribute('data-appearance', 'light');
  else root.removeAttribute('data-appearance');
  // The web app is installable, and the browser paints its chrome from this.
  // index.html has no such tag; web.html does.
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', getComputedStyle(root).getPropertyValue('--ab-ink').trim());
  if (next === mode) return;
  mode = next;
  for (const listener of listeners) listener();
}

// The mode is not only a class of CSS: the terminal is a canvas and the chat's
// syntax highlighting is a set of inline colours, and both are built in
// JavaScript from a palette rather than from a utility. They subscribe here
// and rebuild themselves when the window turns over.
const listeners = new Set<() => void>();
let mode: Mode = 'dark';

// currentMode is which way round the window is right now, for the code that
// picks a colour rather than a class.
export const currentMode = (): Mode => mode;

// onMode runs fn whenever the window turns over, and returns the unsubscribe.
export function onMode(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// applyTheme puts a theme on the window, or takes the last one off, and says
// which way round the window is. The mode is an attribute rather than more
// properties because that is what styles.css keys its light block on: one
// attribute flips every role at once, and nothing here has to know what any of
// them are.
export function applyTheme(theme: T.Theme | undefined): void {
  const accent = palette(theme);
  const style = document.documentElement.style;
  for (const property of properties) {
    const value = accent?.[property];
    if (value) style.setProperty(property, value);
    else style.removeProperty(property);
  }

  const next = modeOf(theme);
  setMode(next);
  if (theme) {
    try {
      localStorage.setItem(remembered, next);
    } catch {
      // A browser that refuses storage: the next window opens dark.
    }
  }
}

// useMode is the mode, for a component that has to re-render when it changes
// rather than let CSS do the work.
export function useMode(): Mode {
  return useSyncExternalStore(onMode, currentMode);
}

// useHostTheme keeps the window in step with the desktop around it. The query
// is the daemon's answer; the event stream writes straight into it (events.ts)
// so a theme change on the host lands here without a poll, and the settings
// control writes into it too, for the same reason.
export function useHostTheme(): T.Theme | undefined {
  const theme = useQuery({ queryKey: ['theme'], queryFn: api.theme });
  useEffect(() => applyTheme(theme.data), [theme.data]);
  return theme.data;
}
