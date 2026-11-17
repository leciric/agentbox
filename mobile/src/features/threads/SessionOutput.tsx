// What a session of a project's chat made, under its chips: its agents, a
// spinner on one at work, each with its pull request's number, state and
// checks; tapping one opens the pull request. The desktop's session header
// (desktop/src/renderer/components/chat/SessionHeader.tsx), read-only here.
import { useQuery } from '@tanstack/react-query';
import { ActivityIndicator, Linking, Pressable, ScrollView, StyleSheet, Text } from 'react-native';
import type * as T from '../../shared/api.ts';
import { api } from '../../connection/api.ts';
import { colors, mono } from '../../ui/theme.ts';

const prTone: Record<string, string> = { merged: colors.brand300, closed: colors.rose, open: colors.emerald };
const checkMark: Record<string, string> = { passing: '✓', failing: '✕', pending: '•' };

export function SessionOutput({ project, session }: { project: string; session: string }) {
  const output = useQuery({ queryKey: ['sessionOutput', project, session], queryFn: () => api.sessionOutput(project, session), refetchInterval: 60_000 });
  const agents = output.data ?? [];
  if (agents.length === 0) return null;
  return (
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.scroll} contentContainerStyle={styles.bar} keyboardShouldPersistTaps="handled">
      {agents.map((a) => (
        <Agent key={a.ref} agent={a} />
      ))}
    </ScrollView>
  );
}

function Agent({ agent }: { agent: T.SessionAgent }) {
  const pr = agent.pr;
  return (
    <Pressable disabled={!pr?.url} onPress={() => pr?.url && void Linking.openURL(pr.url)} style={[styles.pill, agent.archived && styles.archived]} accessibilityRole={pr?.url ? 'link' : undefined}>
      {agent.working ? <ActivityIndicator size="small" color={colors.brand300} style={styles.spin} /> : null}
      <Text style={styles.name} numberOfLines={1}>
        {agent.title || agent.name}
      </Text>
      {pr ? (
        <Text style={[styles.pr, { color: prTone[pr.draft ? '' : pr.state] ?? colors.muted }]}>
          #{pr.number}
          {pr.checks ? ` ${checkMark[pr.checks] ?? ''}` : ''}
        </Text>
      ) : null}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  scroll: { flexGrow: 0, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.lineStrong },
  bar: { flexDirection: 'row', alignItems: 'center', gap: 6, paddingHorizontal: 12, paddingVertical: 6 },
  pill: { flexDirection: 'row', alignItems: 'center', gap: 6, maxWidth: 220, borderRadius: 12, paddingHorizontal: 10, paddingVertical: 4, backgroundColor: colors.surface },
  archived: { opacity: 0.6 },
  name: { color: colors.muted, fontSize: 12, flexShrink: 1 },
  pr: { ...mono, fontSize: 12 },
  spin: { transform: [{ scale: 0.6 }] },
});
