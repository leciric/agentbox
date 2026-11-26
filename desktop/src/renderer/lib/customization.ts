// The user's customization of the app's look and layout, worn live.
//
// The daemon keeps it (internal/customize), in a file of the user's, and
// publishes every change; a chat's appearance tool is what usually changes it,
// when the user asks for "a green accent" or "hide Jobs". Everything here turns
// it into two stylesheets put after the app's own — one of properties, one the
// user's custom CSS — and into the sidebar's order (arrangeItems). Nothing in
// the app's code is changed by it: taking the two stylesheets away (Reset to
// default) is the app as it was.
//
// The properties are the ones styles.css reaches every colour, size and space
// through, set with !important so they win over both the stylesheet's modes
// and the inline accent lib/theme.ts puts on <html> for a followed theme.
import { useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import * as T from '../../shared/api.ts';
import { api } from './api.ts';
import { palette, repaint } from './theme.ts';

// What each colour role paints: a role is several of styles.css's properties
// at once, so asking for a text colour can't leave the titles in the old one.
// Surfaces and lines are washes of their colour, as AgentBox's own are of
// white or black, at the strengths tokens.css gives them.
export const colorRoles: Record<keyof T.CustomColors, (c: string) => Record<string, string>> = {
  background: (c) => ({ '--ab-ink': c, '--ab-chat': c }),
  sidebar: (c) => ({ '--ab-rail': c }),
  composer: (c) => ({ '--ab-composer': c }),
  popover: (c) => ({ '--ab-overlay': c, '--ab-modal': c }),
  text: (c) => ({ '--ab-title': c, '--ab-primary': c, '--ab-secondary': c, '--ab-tertiary': c }),
  mutedText: (c) => ({ '--ab-muted': c, '--ab-subtle': c, '--ab-faint': c }),
  surface: (c) => washes(c, { faint: 2.5, '': 4, raised: 6, strong: 8.5, vivid: 13 }, '--ab-surface'),
  line: (c) => washes(c, { faint: 4.5, '': 7, strong: 10, vivid: 15, heavy: 25 }, '--ab-line'),
  success: (c) => shades(c, '--ab-emerald'),
  warning: (c) => shades(c, '--ab-amber'),
  danger: (c) => shades(c, '--ab-rose'),
  info: (c) => shades(c, '--ab-sky'),
};

function washes(c: string, strengths: Record<string, number>, prefix: string): Record<string, string> {
  return Object.fromEntries(Object.entries(strengths).map(([name, pct]) => [name ? `${prefix}-${name}` : prefix, `color-mix(in srgb, ${c} ${pct}%, transparent)`]));
}

const shades = (c: string, prefix: string) => ({ [`${prefix}-200`]: c, [`${prefix}-300`]: c, [`${prefix}-400`]: c });

// AgentBox's own faces (styles.css's @theme), which a customized font goes in
// front of, so a font this computer lacks falls back to them.
export const ownFonts = {
  ui: '-apple-system, BlinkMacSystemFont, "Inter Variable", "Inter", "Noto Sans", system-ui, sans-serif',
  mono: '"JetBrains Mono Variable", "JetBrainsMono Nerd Font", "JetBrains Mono", ui-monospace, monospace',
};

// The text sizes, AgentBox's (tokens.css) and Tailwind's, in rem: a font size
// scales them all, and nothing else, so a bigger font isn't a zoom.
const textSizes: Record<string, number> = {
  '--text-micro': 0.6875,
  '--text-caption': 0.75,
  '--text-ui': 0.8125,
  '--text-body': 0.875,
  '--text-heading': 1,
  '--text-display': 1.25,
  '--text-xs': 0.75,
  '--text-sm': 0.875,
  '--text-base': 1,
  '--text-lg': 1.125,
  '--text-xl': 1.25,
  '--text-2xl': 1.5,
};

// Tailwind's spacing unit, which every padding, gap and margin is a multiple
// of: density is that unit, 0.25rem being AgentBox's own.
const spacing: Record<string, string> = { [T.DensityCompact]: '0.2rem', [T.DensitySpacious]: '0.3rem' };

const radii: Record<string, number> = { '--radius-sm': 0.25, '--radius-md': 0.375, '--radius-lg': 0.5, '--radius-xl': 0.625, '--radius-2xl': 0.75, '--radius-3xl': 1 };
const radiusScale: Record<string, number> = { [T.RadiusSharp]: 0, [T.RadiusSubtle]: 0.5, [T.RadiusRound]: 1.75 };

// The daemon only stores fonts that are names, but this is the last place
// before a stylesheet: nothing that could end a declaration gets through.
const fontList = (value: string) => value.replace(/[;{}<>\\]/g, '');

const block = (selector: string, props: Record<string, string>) => {
  const lines = Object.entries(props).map(([name, value]) => `  ${name}: ${value} !important;`);
  return lines.length ? `${selector} {\n${lines.join('\n')}\n}\n` : '';
};

// The window wears the dark colours unless it is light, Dracula included; a
// part of a dark window drawn light (data-appearance-scope) wears the light
// ones too.
const darkSelector = ':root:not([data-appearance="light"])';
const lightSelector = ':root[data-appearance="light"], [data-appearance-scope="light"]';

// customizationCSS is the stylesheet of properties a customization comes to.
// Empty for AgentBox's own look.
export function customizationCSS(c: T.Customization | undefined): string {
  if (!c) return '';
  const root: Record<string, string> = {};
  if (c.fonts.ui) root['--font-sans'] = `${fontList(c.fonts.ui)}, ${ownFonts.ui}`;
  if (c.fonts.mono) root['--font-mono'] = `${fontList(c.fonts.mono)}, ${ownFonts.mono}`;
  const size = c.fonts.size || Number(T.FontSizeDefault);
  if (size !== Number(T.FontSizeDefault)) {
    const scale = size / Number(T.FontSizeDefault);
    for (const [name, rem] of Object.entries(textSizes)) root[name] = `${+(rem * scale).toFixed(4)}rem`;
  }
  if (c.density && spacing[c.density]) root['--spacing'] = spacing[c.density];
  if (c.theme.radius && c.theme.radius in radiusScale) {
    for (const [name, rem] of Object.entries(radii)) root[name] = `${+(rem * radiusScale[c.theme.radius]).toFixed(4)}rem`;
  }

  const mode = (which: 'dark' | 'light', colors: T.CustomColors) => {
    const props: Record<string, string> = {};
    // The accent's shades are mixed for each mode, readable on its window,
    // as a followed theme's are (lib/theme.ts palette); its window is the
    // background set here, not the one palette would make of it.
    if (c.theme.accent) {
      const shades = palette({ appearance: 'follow', available: true, name: '', mode: which, background: colors.background ?? '', surface: '', foreground: '', muted: '', accent: c.theme.accent });
      for (const [name, value] of Object.entries(shades ?? {})) if (name !== '--ab-ink') props[name] = value;
    }
    for (const role of Object.keys(colorRoles) as (keyof T.CustomColors)[]) {
      const value = colors[role];
      if (value) Object.assign(props, colorRoles[role](value));
    }
    return props;
  };

  return [block(':root', root), block(darkSelector, mode('dark', c.theme.dark)), block(lightSelector, mode('light', c.theme.light))].join('');
}

// isCustomized says the customization changes anything at all.
export function isCustomized(c: T.Customization | undefined): boolean {
  if (!c) return false;
  return customizationCSS(c) !== '' || !!c.css?.trim() || !!c.sidebar.top?.length || !!c.sidebar.foot?.length || !!c.sidebar.hidden?.length;
}

// arrangeItems is a place's items (the sidebar's top or foot) in the user's
// order — those the order names first, as it names them, then the rest in
// AgentBox's — without the hidden ones.
export function arrangeItems<K extends string>(own: readonly K[], order: readonly string[] | undefined, hidden: readonly string[] | undefined): K[] {
  const named = (order ?? []).filter((id): id is K => (own as readonly string[]).includes(id));
  const rest = own.filter((id) => !named.includes(id));
  return [...new Set([...named, ...rest])].filter((id) => !hidden?.includes(id));
}

export const sidebarTop = T.SidebarTopItems.split(' ');
export const sidebarFoot = T.SidebarFootItems.split(' ');

// The two stylesheets, made on first use and kept last in <head>, after the
// app's own and anything a lazy chunk adds.
function sheet(id: string): HTMLStyleElement {
  let el = document.getElementById(id) as HTMLStyleElement | null;
  if (!el) {
    el = document.createElement('style');
    el.id = id;
  }
  document.head.appendChild(el);
  return el;
}

// applyCustomization puts a customization on the window, or takes it off.
export function applyCustomization(c: T.Customization | undefined): void {
  const props = customizationCSS(c);
  const css = c?.css ?? '';
  const propsSheet = sheet('ab-customization');
  const cssSheet = sheet('ab-custom-css');
  if (propsSheet.textContent === props && cssSheet.textContent === css) return;
  propsSheet.textContent = props;
  cssSheet.textContent = css;
  // The parts that copy the window's colours elsewhere (a chat's framed
  // pages) read them again.
  repaint();
}

// useCustomizationQuery is the customization, kept current by the event
// stream (events.ts), for what reads it — the sidebar, Settings.
export function useCustomizationQuery() {
  return useQuery({ queryKey: ['customization'], queryFn: api.customization, staleTime: Infinity });
}

// useCustomization wears the customization, from the app's root.
export function useCustomization(): T.CustomizationStatus | undefined {
  const status = useCustomizationQuery();
  useEffect(() => applyCustomization(status.data?.customization), [status.data]);
  return status.data;
}
