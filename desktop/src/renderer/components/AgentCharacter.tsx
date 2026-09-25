import { useId, type CSSProperties, type ReactNode } from 'react';
import type { Mood } from '../lib/agentStatus';
import { cn } from '../lib/utils';

// AgentCharacter is an AI tool's mark as a little character acting out what
// its agent is doing (Mood): Claude Code's Clawd, Codex's cloud with its
// prompt for a face, OpenCode's framed "o". The shapes are the brands' own
// (Clawd's pixel grid, Codex's cloud and gradient, OpenCode's square mark and
// its two greys), with eyes and an arm cut out to move. The arm starts inside
// the body, in its colour, so it shows only its end at rest and a whole limb
// raised.
//
// Each moving part is an <svg> of its own over the same 24×24 box, not a
// group inside one: an animated transform on an HTML-level box runs on the
// compositor, where one on an SVG child restyles the page every frame, and a
// rail can hold dozens of these at once. The poses and animations are in
// styles.css (.ab-char), keyed on data-ai and data-mood.

type Parts = { arm: ReactNode; body: ReactNode; eyes: ReactNode; xeyes: ReactNode; cursor?: ReactNode };

// Softened a shade from the brands' own, to sit quietly in a row.
const CLAWD = '#DE8A6E';
const CLAWD_INK = '#3D2B25';

function clawd(): Parts {
  const x = (cx: number) => (
    <path d={`M${cx - 0.8} 8.75l1.6 1.6M${cx + 0.8} 8.75l-1.6 1.6`} stroke={CLAWD_INK} strokeWidth="0.7" strokeLinecap="round" />
  );
  return {
    arm: <rect x="18.5" y="10.95" width="5.5" height="3.1" fill={CLAWD} />,
    body: (
      <path
        fill={CLAWD}
        d="M3 5h18v12.08h-1.49V20H18v-2.92h-1.49V20H15v-2.92H9V20H7.49v-2.92H6V20H4.49v-2.92H3v-3.03H0v-3.1h3z"
      />
    ),
    eyes: (
      <g fill={CLAWD_INK}>
        <rect x="6.15" y="8.5" width="1.2" height="2.1" rx="0.3" />
        <rect x="16.66" y="8.5" width="1.2" height="2.1" rx="0.3" />
      </g>
    ),
    xeyes: (
      <>
        {x(6.75)}
        {x(17.26)}
      </>
    ),
  };
}

const CODEX_CLOUD =
  'M9.064 3.344a4.578 4.578 0 012.285-.312c1 .115 1.891.54 2.673 1.275.01.01.024.017.037.021a.09.09 0 00.043 0 4.55 4.55 0 013.046.275l.047.022.116.057a4.581 4.581 0 012.188 2.399c.209.51.313 1.041.315 1.595a4.24 4.24 0 01-.134 1.223.123.123 0 00.03.115c.594.607.988 1.33 1.183 2.17.289 1.425-.007 2.71-.887 3.854l-.136.166a4.548 4.548 0 01-2.201 1.388.123.123 0 00-.081.076c-.191.551-.383 1.023-.74 1.494-.9 1.187-2.222 1.846-3.711 1.838-1.187-.006-2.239-.44-3.157-1.302a.107.107 0 00-.105-.024c-.388.125-.78.143-1.204.138a4.441 4.441 0 01-1.945-.466 4.544 4.544 0 01-1.61-1.335c-.152-.202-.303-.392-.414-.617a5.81 5.81 0 01-.37-.961 4.582 4.582 0 01-.014-2.298.124.124 0 00.006-.056.085.085 0 00-.027-.048 4.467 4.467 0 01-1.034-1.651 3.896 3.896 0 01-.251-1.192 5.189 5.189 0 01.141-1.6c.337-1.112.982-1.985 1.933-2.618.212-.141.413-.251.601-.33.215-.089.43-.164.646-.227a.098.098 0 00.065-.066 4.51 4.51 0 01.829-1.615 4.535 4.535 0 011.837-1.388z';

function codex(gradient: string): Parts {
  const face = { stroke: '#fff', strokeWidth: 1, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, fill: 'none' };
  return {
    arm: <rect x="17" y="11.2" width="6.6" height="2.5" rx="1.25" fill="#97B2FF" />,
    body: (
      <>
        <defs>
          <linearGradient id={gradient} gradientUnits="userSpaceOnUse" x1="12" x2="12" y1="3" y2="21">
            <stop stopColor="#C9C2FF" />
            <stop offset=".5" stopColor="#97B2FF" />
            <stop offset="1" stopColor="#6670FF" />
          </linearGradient>
        </defs>
        <path d={CODEX_CLOUD} fill={`url(#${gradient})`} />
      </>
    ),
    eyes: <path d="M8.2 10.15l1 1.9-1 1.85" {...face} />,
    xeyes: <path d="M7.9 11.25l1.6 1.6M9.5 11.25l-1.6 1.6" {...face} />,
    cursor: <path d="M12.9 14.2h2.9" {...face} />,
  };
}

// OpenCode's colours swap with the appearance, the way its light and dark
// logos do: --oc-frame and --oc-inner, set in styles.css.
function opencode(): Parts {
  const x = (cx: number) => <path d={`M${cx - 0.75} 12.6l1.5 1.5M${cx + 0.75} 12.6l-1.5 1.5`} stroke="var(--oc-frame)" strokeWidth="0.7" strokeLinecap="round" />;
  return {
    arm: <rect x="17.5" y="12" width="5.8" height="3" fill="var(--oc-frame)" />,
    body: (
      <>
        <rect x="8" y="10" width="8" height="8" fill="var(--oc-inner)" />
        <path d="M16 6H8v12h8V6zm4 16H4V2h16v20z" fillRule="evenodd" fill="var(--oc-frame)" />
      </>
    ),
    eyes: (
      <g fill="var(--oc-frame)">
        <rect x="9.8" y="12.4" width="1.2" height="1.9" rx="0.3" />
        <rect x="13" y="12.4" width="1.2" height="1.9" rx="0.3" />
      </g>
    ),
    xeyes: (
      <>
        {x(10.4)}
        {x(13.6)}
      </>
    ),
  };
}

export const hasCharacter = (ai: string) => ai === 'claude' || ai === 'codex' || ai === 'opencode';

// phase spreads the avatars' loops out, so a column of them doesn't blink in
// step: a fixed offset per seed (an agent's ref), not a random one, so a row
// keeps its rhythm across renders.
function phase(seed: string): string {
  let h = 0;
  for (const c of seed) h = (h * 31 + c.charCodeAt(0)) | 0;
  return `${-(Math.abs(h) % 7000)}ms`;
}

export function AgentCharacter({ ai, mood, seed = '', className }: { ai: string; mood: Mood; seed?: string; className?: string }) {
  const gradient = `ab-codex-${useId().replace(/[^a-zA-Z0-9]/g, '')}`;
  if (!hasCharacter(ai)) return null;
  const parts = ai === 'claude' ? clawd() : ai === 'codex' ? codex(gradient) : opencode();
  const svg = (content: ReactNode, name?: string) => (
    <svg className={name} viewBox="0 0 24 24" aria-hidden>
      {content}
    </svg>
  );
  // A part that loops (eyes blink, the arm waves) is a pose box holding the
  // looping <svg>: the pose eases from mood to mood as a transition, which a
  // loop on the same element would cut short, since removing an animation
  // snaps rather than transitions.
  const layer = (name: string, content: ReactNode) =>
    name === 'ab-eyes' || name === 'ab-arm' ? <span className={name}>{svg(content)}</span> : svg(content, name);
  return (
    <span className={cn('ab-char', className)} data-ai={ai} data-mood={mood} style={{ '--ab-phase': phase(seed) } as CSSProperties}>
      {ai === 'codex' && layer('ab-arm', parts.arm)}
      {layer('ab-body', parts.body)}
      {ai !== 'codex' && layer('ab-arm', parts.arm)}
      {layer('ab-eyes', parts.eyes)}
      {layer('ab-xeyes', parts.xeyes)}
      {parts.cursor && layer('ab-cursor', parts.cursor)}
    </span>
  );
}
