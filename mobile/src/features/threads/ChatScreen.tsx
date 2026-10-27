// A chat, a lead's or an agent's: the conversation live, what it's doing in
// the header (#303's states), the cards that wait on you, and the composer.
// The list is inverted, so it opens on the latest and older pages load as
// you scroll up to them. After t3code's features/threads/ThreadDetailScreen.tsx
// and ThreadFeed.tsx (which uses LegendList; FlatList is enough here).
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocalSearchParams } from 'expo-router';
import { Image } from 'expo-image';
import { useMemo, useState } from 'react';
import { ActivityIndicator, Alert, FlatList, KeyboardAvoidingView, Platform, Pressable, StyleSheet, Text, View } from 'react-native';
import * as T from '../../shared/api.ts';
import { api, chatImagePath, isLead, mediaSource } from '../../connection/api.ts';
import { can, useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { ErrorBoundary } from '../../ui/ErrorBoundary.tsx';
import { Button, Dot, ScreenHeader } from '../../ui/parts.tsx';
import { colors, mono } from '../../ui/theme.ts';
import { agentStatus, isWaiting, leadStatus, liveStatus, workLabel } from './activity.ts';
import { AgentPermissionCard, PermissionCard, QuestionCard } from './cards.tsx';
import { Composer } from './Composer.tsx';
import { Markdown } from './Markdown.tsx';
import { timelineRows, type Row } from './timeline.ts';
import { loadOlder, useThread } from './useThread.ts';

export function ChatScreen() {
  const t = useT();
  const params = useLocalSearchParams<{ ref: string[] }>();
  const parts = (Array.isArray(params.ref) ? params.ref : [params.ref]).filter(Boolean).map(decodeURIComponent);
  const project = parts[0];
  const lead = isLead(parts.join('/'));
  const agentRef = lead ? undefined : `${parts[0]}/${parts[1]}`;
  const session = useSession();
  const queryClient = useQueryClient();

  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const info = useQuery({ queryKey: ['projectChat', project], queryFn: () => api.projectChat(project), enabled: lead });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled: !lead });
  const agent = agents.data?.find((a) => a.ref === agentRef);
  const ref = lead ? (info.data?.started ? info.data.ref : undefined) : agent && agent.state !== 'initializing' ? agent.ref : undefined;
  const thread = useThread(ref);
  const questions = useQuery({ queryKey: ['questions', project], queryFn: () => api.questions(project) });
  const waiting = (questions.data ?? []).filter((q) => isWaiting(q) && (lead || q.ref === agentRef));

  const p = projects.data?.find((x) => x.name === project);
  const title = lead ? t('web.phone.projectChat') : agent?.title || parts[1];
  const subtitle = lead ? p?.displayName || project : agent?.title ? agent.name : undefined;
  const status = lead ? (info.data ? leadStatus(info.data.chat, thread.data) : undefined) : agent ? agentStatus(agent, thread.data) : undefined;
  const live = thread.data ? liveStatus(thread.data) : undefined;
  const asleep = agent && (agent.state === 'stopped' || agent.state === 'paused') ? agent.state : undefined;
  const wake = useMutation({ mutationFn: () => api.startAgent(agentRef!, asleep === 'paused'), onError: (err) => Alert.alert(t('web.phone.couldntStart', { agent: agentRef ?? '' }), err instanceof Error ? err.message : String(err)) });

  const rows = useMemo(() => (thread.data ? timelineRows(thread.data).reverse() : []), [thread.data]);
  const tools = useMemo(() => new Map((thread.data?.items ?? []).flatMap((it) => (it.tool ? [[it.tool.callId, it.tool] as const] : []))), [thread.data]);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const older = async () => {
    if (!ref || !thread.data?.older || loadingOlder) return;
    setLoadingOlder(true);
    await loadOlder(queryClient, ref).finally(() => setLoadingOlder(false));
  };
  const chatRef = lead ? `${project}/${T.LeadName}` : agentRef!;

  const missing = lead ? projects.data && !p : agents.data && !agent;
  return (
    <KeyboardAvoidingView style={styles.root} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <ScreenHeader title={title} subtitle={subtitle} status={status} />
      {missing ? (
        <Text style={styles.empty}>{lead ? t('web.phone.missingProject', { name: project }) : t('web.phone.missingAgent', { name: agentRef ?? '' })}</Text>
      ) : (
        <FlatList
          inverted
          data={rows}
          keyExtractor={(row) => row.id}
          renderItem={({ item: row }) => (
            <ErrorBoundary label={t('mobile.error.row')} resetKey={row}>
              <TimelineRow row={row} chatRef={chatRef} tools={tools} />
            </ErrorBoundary>
          )}
          contentContainerStyle={styles.list}
          onEndReached={() => void older()}
          onEndReachedThreshold={0.3}
          keyboardDismissMode="interactive"
          keyboardShouldPersistTaps="handled"
          ListHeaderComponent={
            <View>
              {live && live.tone === 'live' && (
                <View style={styles.liveRow}>
                  <Dot status={live} />
                  <Text style={styles.liveText} numberOfLines={1}>
                    {live.text}
                    {live.command ? <Text style={[mono, { color: colors.secondary }]}> {live.command}</Text> : null}
                  </Text>
                </View>
              )}
              {waiting.map((q) => (
                <ErrorBoundary key={q.id} label={t('mobile.error.row')} resetKey={q}>
                  <QuestionCard question={q} />
                </ErrorBoundary>
              ))}
            </View>
          }
          ListFooterComponent={loadingOlder || thread.isPending ? <ActivityIndicator color={colors.muted} style={{ marginVertical: 16 }} /> : null}
          ListEmptyComponent={!thread.isPending ? <Text style={[styles.empty, styles.flip]}>{lead && !info.data?.started ? t('mobile.chat.notStarted') : t('mobile.chat.empty')}</Text> : null}
        />
      )}
      {asleep && (
        <View style={styles.asleep}>
          <Text style={styles.asleepText}>{t('mobile.chat.asleep', { name: agent?.name ?? '', state: asleep })}</Text>
          {can(session, 'chat') && <Button label={t('mobile.chat.wake')} kind="secondary" busy={wake.isPending} onPress={() => wake.mutate()} />}
        </View>
      )}
      {!missing && !asleep && (
        <Composer
          chatRef={chatRef}
          name={lead ? t('web.phone.projectChat') : agent?.name ?? ''}
          running={!!live}
          start={lead && info.data && !info.data.started ? () => api.startChat(chatRef).then(() => queryClient.invalidateQueries({ queryKey: ['projectChat', project] })) : undefined}
        />
      )}
    </KeyboardAvoidingView>
  );
}

function TimelineRow({ row, chatRef, tools }: { row: Row; chatRef: string; tools: Map<string, T.ChatTool> }) {
  switch (row.kind) {
    case 'user':
      return (
        <View style={styles.userRow}>
          <View style={styles.userBubble}>
            {row.item.images?.map((img) => (
              <Image key={img.id} source={mediaSource(chatImagePath(chatRef, img.id))} style={styles.image} contentFit="cover" />
            ))}
            {row.item.text ? (
              <Text selectable style={styles.userText}>
                {row.item.text}
              </Text>
            ) : null}
          </View>
        </View>
      );
    case 'assistant':
      return (
        <View style={styles.assistant}>
          <Markdown text={row.item.text ?? ''} />
        </View>
      );
    case 'work':
      return <WorkRow items={row.items} live={row.live} />;
    case 'permission':
      return row.item.permission?.outcome ? null : <PermissionCard chatRef={chatRef} item={row.item} tool={tools.get(row.item.permission?.callId ?? '')} />;
    case 'agentPermission':
      return <AgentPermissionCard item={row.item} />;
    case 'error':
      return (
        <View style={styles.error} accessibilityRole="alert">
          <Text selectable style={styles.errorText}>
            {row.item.text}
          </Text>
        </View>
      );
    case 'note':
      return <Text style={styles.note}>{row.item.text}</Text>;
  }
}

// WorkRow is a run of work folded into one line, what it did last; tapped,
// every step.
function WorkRow({ items, live }: { items: T.ChatItem[]; live: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const last = items.at(-1)!;
  return (
    <Pressable onPress={() => setOpen((o) => !o)} style={styles.work} accessibilityRole="button" accessibilityState={{ expanded: open }}>
      <View style={styles.workHead}>
        <Dot status={{ tone: live ? 'live' : 'muted', live }} />
        <Text style={[styles.workText, live && { color: colors.secondary }]} numberOfLines={1}>
          {workLabel(last)}
        </Text>
        {items.length > 1 && <Text style={styles.workCount}>{t('mobile.chat.steps', { count: items.length })}</Text>}
      </View>
      {open &&
        items.map((it) => (
          <Text key={it.id} style={[styles.workStep, it.kind === 'tool' && it.tool?.kind === 'execute' && mono]} numberOfLines={2}>
            {workLabel(it)}
          </Text>
        ))}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bg },
  list: { paddingHorizontal: 14, paddingVertical: 12 },
  flip: { transform: [{ scaleY: -1 }] },
  empty: { color: colors.subtle, fontSize: 14, textAlign: 'center', padding: 24 },
  userRow: { alignItems: 'flex-end', marginVertical: 8 },
  userBubble: { maxWidth: '85%', backgroundColor: colors.brandSoft, borderRadius: 18, borderBottomRightRadius: 6, paddingHorizontal: 14, paddingVertical: 9, gap: 6 },
  userText: { color: colors.title, fontSize: 15, lineHeight: 21 },
  image: { width: 180, height: 120, borderRadius: 10 },
  assistant: { marginVertical: 6 },
  work: { marginVertical: 3, paddingVertical: 4, gap: 4 },
  workHead: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  workText: { flex: 1, color: colors.subtle, fontSize: 13 },
  workCount: { color: colors.faint, fontSize: 12 },
  workStep: { color: colors.subtle, fontSize: 12, paddingLeft: 16 },
  error: { borderRadius: 12, backgroundColor: colors.roseSoft, padding: 10, marginVertical: 6 },
  errorText: { color: colors.rose, fontSize: 13, lineHeight: 18 },
  note: { color: colors.subtle, fontSize: 12.5, textAlign: 'center', marginVertical: 6 },
  liveRow: { flexDirection: 'row', alignItems: 'center', gap: 8, paddingVertical: 8 },
  liveText: { flex: 1, color: colors.emerald, fontSize: 13 },
  asleep: { flexDirection: 'row', alignItems: 'center', gap: 10, padding: 12, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.lineStrong, backgroundColor: colors.overlay },
  asleepText: { flex: 1, color: colors.muted, fontSize: 13 },
});
