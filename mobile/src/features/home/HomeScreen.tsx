// Projects first, each led by its chat, as UI v2 is: the lead's row with its
// live state, then its agents in the rail's order (what needs you first), and
// the project's review. Each row's line says what that chat is doing now
// (#303's states), from its latest turn only (peekThread). After t3code's
// features/home/HomeScreen.tsx, a list of threads under a header.
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { ChevronRight, CircleCheck, MessageCircleQuestion, Smartphone } from 'lucide-react-native';
import { useState, type ReactNode } from 'react';
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import * as T from '../../shared/api.ts';
import { api } from '../../connection/api.ts';
import { useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { bootState } from '../../connection/boot.ts';
import { ErrorBoundary } from '../../ui/ErrorBoundary.tsx';
import { AgentAvatar, LeadAvatar, PrChip, ProjectIcon } from '../../ui/icons.tsx';
import { Note, StatusLine, useTopInset } from '../../ui/parts.tsx';
import { colors } from '../../ui/theme.ts';
import { agentStatus, avatarMood, isWaiting, leadMood, leadStatus, rank, type ChatStatus } from '../threads/activity.ts';
import { chatKey } from '../threads/thread.ts';
import { peekThread } from '../threads/useThread.ts';

// The chats whose conversation can be read: a machine that is, or was, up.
const readable = (a: T.Agent) => a.state === 'running' || a.state === 'stopped' || a.state === 'paused';

export const chatPath = (ref: string) => `/chat/${ref.split('/').map(encodeURIComponent).join('/')}`;

const projectLabel = (p: T.Project) => p.displayName || p.name;

export function HomeScreen() {
  const t = useT();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const top = useTopInset();
  const session = useSession();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const list = (projects.data ?? []).filter((p) => p.name !== T.HomeProject);
  const leads = useQueries({ queries: list.map((p) => ({ queryKey: ['projectChat', p.name], queryFn: () => api.projectChat(p.name) })) });
  const questions = useQueries({ queries: list.map((p) => ({ queryKey: ['questions', p.name], queryFn: () => api.questions(p.name) })) });
  const reviews = useQueries({ queries: list.map((p) => ({ queryKey: ['review', p.name], queryFn: () => api.readyToReview(p.name) })) });
  // For the agents' pull request chips, as the desktop's rows have.
  const pulls = useQueries({ queries: list.map((p) => ({ queryKey: ['pulls', p.name], queryFn: () => api.pulls(p.name) })) });
  const refs = [...leads.flatMap((c) => (c.data?.started ? [c.data.ref] : [])), ...(agents.data ?? []).filter(readable).map((a) => a.ref)];
  const threads = useQueries({ queries: refs.map((ref) => ({ queryKey: chatKey(ref), queryFn: () => peekThread(queryClient, ref), staleTime: Infinity })) });
  const threadOf = (ref: string | undefined) => (ref ? threads[refs.indexOf(ref)]?.data : undefined);
  // A row that failed renders again when what it shows is read again.
  const updatedAt = Math.max(projects.dataUpdatedAt, agents.dataUpdatedAt);
  const recovered = bootState().mode === 'recovered';
  const [refreshing, setRefreshing] = useState(false);
  const refresh = async () => {
    setRefreshing(true);
    await queryClient.invalidateQueries().finally(() => setRefreshing(false));
  };

  return (
    <View style={styles.root}>
      <View style={[styles.top, { paddingTop: top + 10 }]}>
        <Text style={styles.heading}>{t('mobile.home.title')}</Text>
        <Pressable onPress={() => router.push('/settings')} hitSlop={8} accessibilityRole="button" accessibilityLabel={t('mobile.settings.title')} style={styles.phoneChip}>
          <Smartphone size={13} color={colors.muted} />
          <Text numberOfLines={1} style={styles.phoneChipText}>
            {session?.phone.name}
          </Text>
        </Pressable>
      </View>
      <ScrollView
        contentContainerStyle={[styles.content, { paddingBottom: insets.bottom + 24 }]}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh()} tintColor={colors.muted} />}
      >
        {projects.isPending && <ActivityIndicator color={colors.muted} style={{ marginTop: 24 }} />}
        {projects.error && <Text style={styles.error}>{projects.error.message}</Text>}
        {projects.data && list.length === 0 && <Text style={styles.empty}>{t('web.phone.noProjects')}</Text>}
        {recovered && <Note tone="warning">{t('mobile.crash.recovered')}</Note>}
        {list.map((p, i) => (
          // A project that can't be shown is a line saying so; the rest show.
          <ErrorBoundary key={p.name} label={`${t('mobile.error.row')} (${projectLabel(p)})`} resetKey={updatedAt}>
            <ProjectSection project={p} lead={leads[i]?.data} agents={agents.data ?? []} questions={questions[i]?.data ?? []} toReview={reviews[i]?.data?.total ?? 0} pulls={pulls[i]?.data?.pullRequests ?? []} threadOf={threadOf} />
          </ErrorBoundary>
        ))}
      </ScrollView>
    </View>
  );
}

function ProjectSection({
  project: p,
  lead,
  agents,
  questions,
  toReview,
  pulls,
  threadOf,
}: {
  project: T.Project;
  lead?: T.ProjectChat;
  agents: T.Agent[];
  questions: T.Question[];
  toReview: number;
  pulls: T.PullRequest[];
  threadOf: (ref: string | undefined) => T.ChatThread | undefined;
}) {
  const t = useT();
  const router = useRouter();
  const leadRef = lead?.started ? lead.ref : undefined;
  const own = agents
    .filter((a) => a.project === p.name)
    .map((a) => ({ a, status: safeStatus(a, threadOf(a.ref)) }))
    .sort((x, y) => safeRank(x.a) - safeRank(y.a) || x.a.name.localeCompare(y.a.name));
  const waiting = questions.filter(isWaiting).length;
  const working = own.filter((o) => o.status?.tone === 'live').length;
  const needYou = own.filter((o) => o.status?.tone === 'urgent').length + waiting;
  return (
    <View style={styles.section}>
      <View style={styles.sectionHead}>
        <ProjectIcon project={p} size={20} />
        <Text style={styles.sectionTitle} numberOfLines={1}>
          {projectLabel(p)}
        </Text>
        <Text style={styles.counts} numberOfLines={1}>
          {[t('mobile.home.agents', { count: own.length }), working > 0 ? t('mobile.home.working', { count: working }) : '', needYou > 0 ? t('mobile.home.needYou', { count: needYou }) : '']
            .filter(Boolean)
            .join(' · ')}
        </Text>
      </View>
      <Row
        icon={<LeadAvatar mood={leadMood(lead?.chat, waiting > 0)} />}
        title={t('web.phone.projectChat')}
        status={leadStatus(lead?.chat, threadOf(leadRef))}
        badge={waiting > 0 ? t('mobile.home.questions', { count: waiting }) : undefined}
        onPress={() => router.push(chatPath(p.name))}
      />
      {own.map(({ a, status }) => (
        <ErrorBoundary key={a.ref} label={`${t('mobile.error.row')} (${a.name})`} resetKey={status}>
          <Row
            icon={<AgentAvatar ai={a.ai} mood={avatarMood(a, questions.some((q) => q.ref === a.ref && isWaiting(q)))} />}
            title={a.title || a.name}
            subtitle={a.title ? a.name : undefined}
            status={status ?? { text: t('mobile.error.row'), tone: 'error', live: false }}
            extra={prOf(pulls, a.name) && <PrChip pr={prOf(pulls, a.name)!} />}
            onPress={() => router.push(chatPath(a.ref))}
          />
        </ErrorBoundary>
      ))}
      <Row
        icon={
          <View style={[styles.reviewTile, toReview > 0 && { backgroundColor: colors.amberSoft }]}>
            <CircleCheck size={20} color={toReview > 0 ? colors.amber : colors.subtle} />
          </View>
        }
        title={t('mobile.review.title')}
        status={{ text: toReview > 0 ? t('mobile.home.toReview', { count: toReview }) : t('mobile.review.nothing'), tone: toReview > 0 ? 'urgent' : 'muted', live: false }}
        plain
        onPress={() => router.push(`/review/${encodeURIComponent(p.name)}`)}
      />
    </View>
  );
}

// An agent's status, or undefined when its data can't be summed up: its row
// says so, and the list still sorts and counts the others.
function safeStatus(a: T.Agent, thread: T.ChatThread | undefined): ChatStatus | undefined {
  try {
    return agentStatus(a, thread);
  } catch (err) {
    console.error('[home] status of', a.ref, err);
    return undefined;
  }
}

function safeRank(a: T.Agent): number {
  try {
    return rank(a);
  } catch {
    return 1;
  }
}

// prOf is an agent's pull request, its open one before any it closed.
function prOf(pulls: T.PullRequest[], agent: string): T.PullRequest | undefined {
  const own = pulls.filter((pr) => pr.agent === agent);
  return own.find((pr) => pr.state === 'open') ?? own[0];
}

function Row({
  icon,
  title,
  subtitle,
  status,
  badge,
  extra,
  plain,
  onPress,
}: {
  icon: ReactNode;
  title: string;
  subtitle?: string;
  status: ChatStatus;
  badge?: string;
  extra?: ReactNode;
  plain?: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      style={({ pressed }) => [styles.row, status.tone === 'urgent' && !plain && { backgroundColor: colors.amberSoft }, pressed && { backgroundColor: colors.raised }]}
    >
      {icon}
      <View style={styles.rowText}>
        <Text style={styles.rowTitle} numberOfLines={1}>
          {title}
        </Text>
        {plain ? (
          <Text style={[styles.rowPlain, status.tone === 'urgent' && { color: colors.amber }]} numberOfLines={1}>
            {status.text}
          </Text>
        ) : (
          <StatusLine status={status} after={subtitle} />
        )}
      </View>
      {extra}
      {badge && (
        <View style={styles.badge}>
          <MessageCircleQuestion size={12} color={colors.amber} strokeWidth={2.2} />
          <Text style={styles.badgeText}>{badge}</Text>
        </View>
      )}
      <ChevronRight size={18} color={colors.faint} />
    </Pressable>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: colors.bg },
  top: { flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 18, paddingBottom: 10, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line },
  heading: { flex: 1, color: colors.title, fontSize: 24, fontWeight: '700', letterSpacing: -0.3 },
  phoneChip: { flexDirection: 'row', alignItems: 'center', gap: 5, maxWidth: 170, borderRadius: 999, paddingHorizontal: 10, paddingVertical: 5, backgroundColor: colors.raised },
  phoneChipText: { flexShrink: 1, color: colors.muted, fontSize: 12 },
  content: { paddingHorizontal: 10, paddingTop: 14, gap: 22 },
  section: { gap: 2 },
  sectionHead: { flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 8, paddingBottom: 6 },
  sectionTitle: { color: colors.faint, fontSize: 11, fontWeight: '700', textTransform: 'uppercase', letterSpacing: 1.3, flexShrink: 1 },
  counts: { color: colors.subtle, fontSize: 11, flexShrink: 1 },
  row: { flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 58, borderRadius: 16, paddingHorizontal: 10, paddingVertical: 8 },
  rowText: { flex: 1, minWidth: 0, gap: 3 },
  rowTitle: { color: colors.primary, fontSize: 15, fontWeight: '500' },
  rowPlain: { color: colors.subtle, fontSize: 12 },
  reviewTile: { width: 40, height: 40, borderRadius: 12, backgroundColor: colors.surface, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong, alignItems: 'center', justifyContent: 'center' },
  badge: { flexDirection: 'row', alignItems: 'center', gap: 4, borderRadius: 999, paddingHorizontal: 8, paddingVertical: 2, backgroundColor: colors.amberSoft },
  badgeText: { color: colors.amber, fontSize: 11, fontWeight: '600' },
  error: { color: colors.rose, paddingHorizontal: 8 },
  empty: { color: colors.subtle, paddingHorizontal: 8, fontSize: 14 },
});
