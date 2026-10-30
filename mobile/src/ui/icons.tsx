// The marks the phone shares with the desktop app, which draws the same ones
// with lucide-react and its own SVG: an agent's AI tool as a little character
// acting out its mood on a tinted tile (renderer/components/state.tsx's
// AgentAvatar, AgentCharacter.tsx), the lead's crown, a project's icon
// (ProjectIcon.tsx) and a pull request's chip (PrChip.tsx). Same icon, same
// meaning, so the two apps read alike. The characters hold their mood's pose
// without the desktop's loops: a list of them never stops animating otherwise.
import { useQuery } from '@tanstack/react-query';
import { Image } from 'expo-image';
import { Bot, Crown, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, MousePointer2, Sparkles, SquareTerminal, Terminal, type LucideIcon } from 'lucide-react-native';
import { useId, type ReactNode } from 'react';
import { Linking, Pressable, StyleSheet, Text, View } from 'react-native';
import Svg, { Defs, G, LinearGradient, Path, Rect, Stop } from 'react-native-svg';
import type * as T from '../shared/api.ts';
import { api } from '../connection/api.ts';
import type { Mood } from '../features/threads/activity.ts';
import { colors, mono } from './theme.ts';

// Softened a shade from the brands' own, as on the desktop.
const CLAWD = '#DE8A6E';
const CLAWD_INK = '#3D2B25';
const OC_FRAME = '#e4dede';
const OC_INNER = '#5a5454';
const CODEX_CLOUD =
  'M9.064 3.344a4.578 4.578 0 012.285-.312c1 .115 1.891.54 2.673 1.275.01.01.024.017.037.021a.09.09 0 00.043 0 4.55 4.55 0 013.046.275l.047.022.116.057a4.581 4.581 0 012.188 2.399c.209.51.313 1.041.315 1.595a4.24 4.24 0 01-.134 1.223.123.123 0 00.03.115c.594.607.988 1.33 1.183 2.17.289 1.425-.007 2.71-.887 3.854l-.136.166a4.548 4.548 0 01-2.201 1.388.123.123 0 00-.081.076c-.191.551-.383 1.023-.74 1.494-.9 1.187-2.222 1.846-3.711 1.838-1.187-.006-2.239-.44-3.157-1.302a.107.107 0 00-.105-.024c-.388.125-.78.143-1.204.138a4.441 4.441 0 01-1.945-.466 4.544 4.544 0 01-1.61-1.335c-.152-.202-.303-.392-.414-.617a5.81 5.81 0 01-.37-.961 4.582 4.582 0 01-.014-2.298.124.124 0 00.006-.056.085.085 0 00-.027-.048 4.467 4.467 0 01-1.034-1.651 3.896 3.896 0 01-.251-1.192 5.189 5.189 0 01.141-1.6c.337-1.112.982-1.985 1.933-2.618.212-.141.413-.251.601-.33.215-.089.43-.164.646-.227a.098.098 0 00.065-.066 4.51 4.51 0 01.829-1.615 4.535 4.535 0 011.837-1.388z';

export const hasCharacter = (ai: string) => ai === 'claude' || ai === 'codex' || ai === 'opencode';

// Where each character's eyes and arm turn from, in its 24-unit box, and how
// far a raised arm lifts (styles.css's --ab-eye-origin, --ab-arm-origin and
// --ab-arm-lift).
const poses: Record<string, { eye: [number, number]; arm: [number, number]; lift: number }> = {
  claude: { eye: [12, 9.55], arm: [21, 12.5], lift: -6 },
  codex: { eye: [8.71, 12.05], arm: [19.51, 12.46], lift: -4.08 },
  opencode: { eye: [12, 13.34], arm: [19.99, 13.51], lift: -6 },
};

// grey is a colour as the desktop's grayscale filter shows it: an agent asleep.
function grey(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  const l = Math.round(0.2126 * ((n >> 16) & 255) + 0.7152 * ((n >> 8) & 255) + 0.0722 * (n & 255));
  const h = l.toString(16).padStart(2, '0');
  return `#${h}${h}${h}`;
}

// AgentCharacter is an AI tool's character in its mood's pose: eyes shut and
// arm down asleep, crossed out on an error, an arm up asking.
export function AgentCharacter({ ai, mood, size }: { ai: string; mood: Mood; size: number }) {
  const gradient = `codex${useId().replace(/[^a-zA-Z0-9]/g, '')}`;
  const pose = poses[ai];
  if (!pose) return null;
  const asleep = mood === 'sleeping';
  const c = (hex: string) => (asleep ? grey(hex) : hex);
  const ey = pose.eye[1];
  const [ax, ay] = pose.arm;
  const eyesAt = asleep ? `translate(0 ${(ey * 0.85).toFixed(2)}) scale(1 0.15)` : undefined;
  const armAt = mood === 'asking' ? `translate(0 ${pose.lift}) rotate(-40 ${ax} ${ay})` : asleep ? `rotate(12 ${ax} ${ay})` : undefined;
  const crossed = mood === 'error';
  let arm: ReactNode, body: ReactNode, eyes: ReactNode, xeyes: ReactNode, cursor: ReactNode;
  if (ai === 'claude') {
    const x = (cx: number) => <Path key={cx} d={`M${cx - 0.8} 8.75l1.6 1.6M${cx + 0.8} 8.75l-1.6 1.6`} stroke={c(CLAWD_INK)} strokeWidth={0.7} strokeLinecap="round" />;
    arm = <Rect x={18.5} y={10.95} width={5.5} height={3.1} fill={c(CLAWD)} />;
    body = <Path fill={c(CLAWD)} d="M3 5h18v12.08h-1.49V20H18v-2.92h-1.49V20H15v-2.92H9V20H7.49v-2.92H6V20H4.49v-2.92H3v-3.03H0v-3.1h3z" />;
    eyes = (
      <G fill={c(CLAWD_INK)}>
        <Rect x={6.15} y={8.5} width={1.2} height={2.1} rx={0.3} />
        <Rect x={16.66} y={8.5} width={1.2} height={2.1} rx={0.3} />
      </G>
    );
    xeyes = [x(6.75), x(17.26)];
  } else if (ai === 'codex') {
    const face = { stroke: '#fff', strokeWidth: 1, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, fill: 'none' };
    arm = <Rect x={17} y={11.2} width={6.6} height={2.5} rx={1.25} fill={c('#97B2FF')} />;
    body = (
      <>
        <Defs>
          <LinearGradient id={gradient} gradientUnits="userSpaceOnUse" x1={12} x2={12} y1={3} y2={21}>
            <Stop stopColor={c('#C9C2FF')} />
            <Stop offset={0.5} stopColor={c('#97B2FF')} />
            <Stop offset={1} stopColor={c('#6670FF')} />
          </LinearGradient>
        </Defs>
        <Path d={CODEX_CLOUD} fill={`url(#${gradient})`} />
      </>
    );
    eyes = <Path d="M8.2 10.15l1 1.9-1 1.85" {...face} />;
    xeyes = <Path d="M7.9 11.25l1.6 1.6M9.5 11.25l-1.6 1.6" {...face} />;
    cursor = <Path d="M12.9 14.2h2.9" {...face} />;
  } else {
    const x = (cx: number) => <Path key={cx} d={`M${cx - 0.75} 12.6l1.5 1.5M${cx + 0.75} 12.6l-1.5 1.5`} stroke={OC_FRAME} strokeWidth={0.7} strokeLinecap="round" />;
    arm = <Rect x={17.5} y={12} width={5.8} height={3} fill={OC_FRAME} />;
    body = (
      <>
        <Rect x={8} y={10} width={8} height={8} fill={OC_INNER} />
        <Path d="M16 6H8v12h8V6zm4 16H4V2h16v20z" fillRule="evenodd" fill={OC_FRAME} />
      </>
    );
    eyes = (
      <G fill={OC_FRAME}>
        <Rect x={9.8} y={12.4} width={1.2} height={1.9} rx={0.3} />
        <Rect x={13} y={12.4} width={1.2} height={1.9} rx={0.3} />
      </G>
    );
    xeyes = [x(10.4), x(13.6)];
  }
  return (
    <Svg width={size} height={size} viewBox="0 0 24 24" style={asleep ? { opacity: 0.45 } : undefined}>
      {ai === 'codex' && <G transform={armAt}>{arm}</G>}
      {body}
      {ai !== 'codex' && <G transform={armAt}>{arm}</G>}
      {crossed ? xeyes : <G transform={eyesAt}>{eyes}</G>}
      {cursor}
    </Svg>
  );
}

// ToolIcon is a tool with no character: Cursor's cube, else the desktop's
// AIIcon (a shell-only agent's terminal).
export function ToolIcon({ ai, size, color }: { ai: string; size: number; color: string }) {
  if (ai === 'cursor') {
    return (
      <Svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={1.6} strokeLinejoin="round">
        <Path d="M12 2.5l8.2 4.75v9.5L12 21.5l-8.2-4.75v-9.5z" />
        <Path d="M3.8 7.25L12 12l8.2-4.75M12 12v9.5" />
      </Svg>
    );
  }
  const Icon: LucideIcon = ai === 'claude' ? Sparkles : ai === 'codex' ? Bot : ai === 'opencode' ? Terminal : ai === 'cursor' ? MousePointer2 : SquareTerminal;
  return <Icon size={size} color={color} />;
}

// The tile's tint for each mood, its gradient's stops: the brand's while at
// work or idle, amber asking, rose broken, plain asleep.
const tints: Record<Mood, [string, number][]> = {
  working: [['#8b5cf6', 0.2], ['#6366f1', 0.07], ['#6366f1', 0]],
  idle: [['#8b5cf6', 0.2], ['#6366f1', 0.07], ['#6366f1', 0]],
  asking: [['#fbbf24', 0.2], ['#fbbf24', 0.05], ['#fbbf24', 0]],
  error: [['#f43f5e', 0.18], ['#f43f5e', 0], ['#f43f5e', 0]],
  sleeping: [['#ffffff', 0.065], ['#ffffff', 0], ['#ffffff', 0]],
};

// AgentAvatar is an agent's AI tool on a tile tinted by its mood, with "z"s
// while its machine is off; badge sits on its corner (the lead's crown).
export function AgentAvatar({ ai, mood, size = 40, badge }: { ai: string; mood: Mood; size?: number; badge?: ReactNode }) {
  const tint = `tint${useId().replace(/[^a-zA-Z0-9]/g, '')}`;
  const dim = mood === 'sleeping' || mood === 'error';
  return (
    <View style={{ width: size, height: size }}>
      <View
        style={[
          styles.tile,
          { borderRadius: size * 0.3 },
          mood === 'asking' && { borderColor: 'rgba(251,191,36,0.45)' },
          mood === 'error' && { borderColor: 'rgba(251,113,133,0.4)' },
        ]}
      >
        <Svg style={StyleSheet.absoluteFill}>
          <Defs>
            <LinearGradient id={tint} x1={0} y1={0} x2={1} y2={1}>
              {tints[mood].map(([color, opacity], i) => (
                <Stop key={i} offset={i / 2} stopColor={color} stopOpacity={opacity} />
              ))}
            </LinearGradient>
          </Defs>
          <Rect width="100%" height="100%" fill={`url(#${tint})`} />
        </Svg>
        {hasCharacter(ai) ? <AgentCharacter ai={ai} mood={mood} size={size * 0.58} /> : <ToolIcon ai={ai} size={size * 0.42} color={dim ? colors.muted : '#ddd6fe'} />}
        {mood === 'sleeping' && (
          <>
            <Text style={[styles.z, { right: size * 0.16, top: size * 0.26, fontSize: size * 0.14 }]}>z</Text>
            <Text style={[styles.z, { right: size * 0.09, top: size * 0.13, fontSize: size * 0.16 }]}>z</Text>
            <Text style={[styles.z, { right: size * 0.02, top: 0, fontSize: size * 0.19 }]}>z</Text>
          </>
        )}
      </View>
      {badge}
    </View>
  );
}

// LeadAvatar is a project's lead: Claude Code, as the desktop's rail draws
// it, crowned (its repository row's crown), so it reads apart from the agents.
export function LeadAvatar({ mood, size = 40 }: { mood: Mood; size?: number }) {
  const crown = Math.round(size * 0.42);
  return (
    <AgentAvatar
      ai="claude"
      mood={mood}
      size={size}
      badge={
        <View style={[styles.crown, { width: crown, height: crown, borderRadius: crown / 2, top: -crown * 0.3, right: -crown * 0.3 }]}>
          <Crown size={crown * 0.62} color="#fbbf24" strokeWidth={2.4} />
        </View>
      }
    />
  );
}

// ProjectIcon is a project's icon: the image or emoji chosen for it or found
// in its repository, else its first letter, in a rounded square.
export function ProjectIcon({ project, size = 20 }: { project: Pick<T.Project, 'name' | 'displayName' | 'iconVersion'>; size?: number }) {
  const icon = useQuery({ queryKey: ['projectIcon', project.name, project.iconVersion ?? 0], queryFn: () => api.projectIcon(project.name), staleTime: Infinity, retry: false }).data;
  const box = [styles.project, { width: size, height: size, borderRadius: size * 0.25 }];
  if (icon?.image) return <Image source={{ uri: icon.image }} style={box} contentFit="contain" accessibilityIgnoresInvertColors />;
  return (
    <View style={box}>
      <Text style={icon?.emoji ? { fontSize: size * 0.72 } : [styles.letter, { fontSize: size * 0.5 }]}>{icon?.emoji || (project.displayName || project.name).slice(0, 1).toUpperCase()}</Text>
    </View>
  );
}

// The chip's colours by what the pull request is: open, draft, merged or
// closed, as the desktop's. Failing checks turn an open one red.
function prTone(pr: Pick<T.PullRequest, 'state' | 'draft' | 'checks'>): { Icon: LucideIcon; color: string; bg: string } {
  if (pr.state === 'merged') return { Icon: GitMerge, color: colors.brand300, bg: colors.brandSoft };
  if (pr.state === 'closed') return { Icon: GitPullRequestClosed, color: colors.rose, bg: colors.roseSoft };
  if (pr.draft) return { Icon: GitPullRequestDraft, color: colors.muted, bg: colors.raised };
  if (pr.checks === 'failing') return { Icon: GitPullRequest, color: colors.rose, bg: colors.roseSoft };
  return { Icon: GitPullRequest, color: colors.emerald, bg: colors.emeraldSoft };
}

const checkMark: Record<string, [string, string]> = { passing: ['✓', colors.emerald], failing: ['✕', colors.rose], pending: ['•', colors.amber] };

// PrChip is a pull request as a pill: its icon, number and checks, in its
// state's colour. It opens the pull request on GitHub.
export function PrChip({ pr }: { pr: Pick<T.PullRequest, 'number' | 'url' | 'state' | 'draft' | 'checks' | 'conflict'> }) {
  const { Icon, color, bg } = prTone(pr);
  const check = pr.checks ? checkMark[pr.checks] : undefined;
  return (
    <Pressable accessibilityRole="link" disabled={!pr.url} onPress={() => void Linking.openURL(pr.url)} hitSlop={6} style={[styles.chip, { backgroundColor: bg }]}>
      <Icon size={12} color={color} strokeWidth={2.2} />
      <Text style={[mono, styles.chipText, { color }]}>
        #{pr.number}
        {check ? <Text style={{ color: check[1] }}> {check[0]}</Text> : null}
        {pr.conflict ? <Text style={{ color: colors.rose }}> ⚠</Text> : null}
      </Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  tile: { flex: 1, overflow: 'hidden', alignItems: 'center', justifyContent: 'center', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong },
  z: { position: 'absolute', color: colors.subtle, fontWeight: '700' },
  crown: { position: 'absolute', alignItems: 'center', justifyContent: 'center', backgroundColor: colors.bg, borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(251,191,36,0.45)' },
  project: { alignItems: 'center', justifyContent: 'center', overflow: 'hidden', backgroundColor: colors.raised },
  letter: { color: colors.secondary, fontWeight: '600' },
  chip: { flexDirection: 'row', alignItems: 'center', gap: 4, alignSelf: 'flex-start', borderRadius: 999, paddingHorizontal: 7, paddingVertical: 2 },
  chipText: { fontSize: 11 },
});
