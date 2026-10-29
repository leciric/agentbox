// A project's review, within the phone's scopes: the agents ready to review
// (#322), each with what it said when it finished, what it changed, its
// screenshots and recordings (each before/after pair first, one half at a
// time, a swipe flipping to the other), and its pull request, which a phone with the
// "merge" scope merges after confirming it here (the daemon refuses a
// phone's merge that doesn't say so). The method is a choice the phone
// remembers, as the desktop's split Merge button does (#329). One agent shows
// at a time, with Back and Next between those waiting, "Done reviewing" (which
// asks whether to merge an open pull request first) and "Request changes",
// both of the "answer" scope.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocalSearchParams, useRouter } from 'expo-router';
import { Image } from 'expo-image';
import { ArrowUp, Check, ChevronDown, ChevronLeft, ChevronRight, CircleCheck, GitMerge, MessageSquare, Play } from 'lucide-react-native';
import { useRef, useState } from 'react';
import { ActivityIndicator, Alert, KeyboardAvoidingView, Modal, PanResponder, Pressable, RefreshControl, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import type * as T from '../../shared/api.ts';
import { pairUp, type Pair, type Side } from '../../shared/pairs.ts';
import { api, mediaFilePath, mediaSource } from '../../connection/api.ts';
import { can, useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { ErrorBoundary } from '../../ui/ErrorBoundary.tsx';
import { AgentAvatar, PrChip, ProjectIcon } from '../../ui/icons.tsx';
import { Button, Note, ScreenHeader } from '../../ui/parts.tsx';
import { colors } from '../../ui/theme.ts';
import { chatPath } from '../home/HomeScreen.tsx';
import { Markdown } from '../threads/Markdown.tsx';
import { mergeBlock, pickMethod, rememberMethod, useMergeMethod } from './merge.ts';
import { type Walk, advance, goBack, nextPending, position, startWalk } from './walk.ts';

export function ReviewScreen() {
  const t = useT();
  // agent is where the walk starts, from a notification's tap (push.ts).
  const { project, agent } = useLocalSearchParams<{ project: string; agent?: string }>();
  const queryClient = useQueryClient();
  const session = useSession();
  const review = useQuery({ queryKey: ['review', project], queryFn: () => api.readyToReview(project) });
  const pulls = useQuery({ queryKey: ['pulls', project], queryFn: () => api.pulls(project) });
  const p = useQuery({ queryKey: ['projects'], queryFn: api.projects }).data?.find((x) => x.name === project);
  const [refreshing, setRefreshing] = useState(false);
  const items = review.data?.items ?? [];
  const reviewed = new Set(items.flatMap((i) => (i.pr ? [i.pr.number] : [])));
  const others = (pulls.data?.pullRequests ?? []).filter((pr) => pr.state === 'open' && !reviewed.has(pr.number));
  const mayMerge = can(session, 'merge');
  const mayAnswer = can(session, 'answer');
  // The walk starts on the first agent waiting. finished is it run out: all
  // done until a refresh finds more.
  const [started, setWalk] = useState<Walk | null>(null);
  const [finished, setFinished] = useState(false);
  const walk = finished ? null : (started ?? startWalk(items, agent));
  const live = items;
  // The agent as the list has it now (its pull request merged, say), or as it
  // was when the walk left it there (reviewed, so gone from the list).
  const current = walk && (items.find((i) => i.ref === walk.item.ref) ?? walk.item);
  const next = walk ? nextPending(walk, live) : undefined;
  const go = (w: Walk | null) => (w ? setWalk(w) : setFinished(true));
  return (
    <View style={styles.root}>
      <ScreenHeader title={t('mobile.review.title')} subtitle={p?.displayName || project} icon={p && <ProjectIcon project={p} size={28} />} />
      <ScrollView
        contentContainerStyle={styles.content}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            tintColor={colors.muted}
            onRefresh={() => {
              setRefreshing(true);
              setWalk(null);
              setFinished(false);
              void Promise.all([queryClient.invalidateQueries({ queryKey: ['review', project] }), api.pulls(project, true).then((p) => queryClient.setQueryData(['pulls', project], p))]).finally(() => setRefreshing(false));
            }}
          />
        }
      >
        {!mayMerge && <Note>{t('mobile.review.noMergeScope')}</Note>}
        {!mayAnswer && <Note>{t('mobile.review.noAnswerScope')}</Note>}
        {review.isPending && <ActivityIndicator color={colors.muted} />}
        {review.error && <Note tone="error">{review.error.message}</Note>}
        {review.data && !current && (
          <View style={styles.emptyBox}>
            <CircleCheck size={28} color={colors.faint} />
            <Text style={styles.empty}>{finished ? t('review.allDone') : t('mobile.review.nothing')}</Text>
          </View>
        )}
        {walk && current && (
          <>
            <Text style={styles.cardSub}>{t('review.agentPosition', position(walk, live))}</Text>
            <ErrorBoundary key={current.ref} label={t('mobile.error.row')} resetKey={current}>
              <ReviewCard item={current} pulls={pulls.data} mayMerge={mayMerge} />
            </ErrorBoundary>
            <ReviewActions
              key={`${current.ref}:actions`}
              item={current}
              pulls={pulls.data}
              mayMerge={mayMerge}
              mayAnswer={mayAnswer}
              canBack={walk.trail.length > 0}
              hasNext={!!next}
              onBack={() => setWalk(goBack(walk))}
              onNext={() => go(advance(walk, live))}
              onDone={() => go(advance(walk, live))}
            />
          </>
        )}
        {others.length > 0 && <Text style={styles.section}>{t('mobile.review.openPulls')}</Text>}
        {others.map((pr) => (
          <View key={pr.number} style={styles.card}>
            <ErrorBoundary label={t('mobile.error.row')} resetKey={pr}>
              <PullRow project={project} pr={pr} pulls={pulls.data} mayMerge={mayMerge} />
            </ErrorBoundary>
          </View>
        ))}
      </ScrollView>
    </View>
  );
}

// ReviewActions are the footer's: Back, Request changes, Done reviewing and
// Next. Done reviewing marks the agent reviewed and goes on; while its pull
// request is open (and the phone may merge) it asks first whether to merge.
function ReviewActions({
  item,
  pulls,
  mayMerge,
  mayAnswer,
  canBack,
  hasNext,
  onBack,
  onNext,
  onDone,
}: {
  item: T.ReviewItem;
  pulls?: T.ProjectPullRequests;
  mayMerge: boolean;
  mayAnswer: boolean;
  canBack: boolean;
  hasNext: boolean;
  onBack: () => void;
  onNext: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const remembered = useMergeMethod();
  const [asking, setAsking] = useState(false);
  const [text, setText] = useState<string | null>(null);
  const name = item.title || item.agent;
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['review', item.project] });
    void queryClient.invalidateQueries({ queryKey: ['pulls', item.project] });
  };
  const fail = (err: unknown) => Alert.alert(t('review.failed', { error: err instanceof Error ? err.message : String(err) }));
  const mark = useMutation({
    mutationFn: () => api.markReviewed(item.ref, item.fingerprint),
    onSuccess: () => {
      Alert.alert(t('review.marked', { agent: name }));
      refresh();
      onDone();
    },
    onError: fail,
  });
  const pr = item.pr && (pulls?.pullRequests.find((p) => p.number === item.pr?.number) ?? item.pr);
  const methods = pulls?.mergeMethods?.length ? pulls.mergeMethods : ['squash', 'merge', 'rebase'];
  const method = pickMethod(methods, remembered);
  const merge = useMutation({
    mutationFn: (n: number) => api.merge(item.project, n, method),
    onSuccess: () => {
      void rememberMethod(method);
      setAsking(false);
      mark.mutate();
    },
    onError: fail,
  });
  const block = pr ? mergeBlock(pr, pulls) : undefined;
  const ask = !!pr && pr.state === 'open' && mayMerge;
  const changes = useMutation({
    mutationFn: (message: string) => api.requestChanges(item.ref, message, item.fingerprint),
    onSuccess: () => {
      setText(null);
      Alert.alert(t('mobile.review.changesSent', { agent: name }));
      refresh();
      onDone();
    },
    onError: fail,
  });
  return (
    <View style={styles.actions}>
      <View style={styles.row}>
        <Button kind="secondary" label={t('review.back')} icon={ChevronLeft} disabled={!canBack} onPress={onBack} style={styles.flex} />
        {hasNext && <Button kind="secondary" label={t('review.next')} icon={ChevronRight} onPress={onNext} style={styles.flex} />}
      </View>
      {mayAnswer && (
        <View style={styles.row}>
          <Button kind="secondary" label={t('review.requestChanges')} icon={MessageSquare} onPress={() => setText('')} style={styles.flex} />
          <Button label={t('review.doneReviewing')} icon={Check} busy={mark.isPending} onPress={() => (ask ? setAsking(true) : mark.mutate())} style={styles.flex} />
        </View>
      )}
      <Modal visible={asking} transparent animationType="fade" onRequestClose={() => setAsking(false)}>
        <View style={styles.backdrop}>
          <View style={styles.sheet}>
            <Text style={styles.sheetTitle}>{t('review.mergeAsk', { number: pr ? pr.number : 0 })}</Text>
            <Text style={styles.cardSub}>{pr ? `${pr.title}\n${t(`memory.pulls.method.${method as 'merge' | 'squash' | 'rebase'}`)} → ${pr.baseBranch ?? ''}` : ''}</Text>
            {block && <Text style={styles.block}>{t(`review.mergeBlocked.${block}`, { base: pr?.baseBranch ?? '' })}</Text>}
            <Button label={t('web.phone.merge')} icon={GitMerge} busy={merge.isPending} disabled={!!block || mark.isPending} onPress={() => pr && merge.mutate(pr.number)} />
            <Button kind="secondary" label={t('review.justMark')} icon={Check} busy={mark.isPending} disabled={merge.isPending} onPress={() => { setAsking(false); mark.mutate(); }} />
            <Button kind="secondary" label={t('common.cancel')} disabled={merge.isPending || mark.isPending} onPress={() => setAsking(false)} />
          </View>
        </View>
      </Modal>
      <Modal visible={text !== null} transparent animationType="fade" onRequestClose={() => setText(null)}>
        {/* The sheet rises above the keyboard, which would cover it (ChatScreen). */}
        <KeyboardAvoidingView behavior="padding" style={styles.backdrop}>
          <View style={styles.sheet}>
            <Text style={styles.sheetTitle}>{t('mobile.review.changesTitle', { agent: name })}</Text>
            <TextInput
              value={text ?? ''}
              onChangeText={setText}
              placeholder={t('mobile.review.changesPlaceholder')}
              placeholderTextColor={colors.faint}
              style={styles.input}
              multiline
              autoFocus
              accessibilityLabel={t('mobile.review.changesTitle', { agent: name })}
            />
            <Button label={t('mobile.review.send')} icon={ArrowUp} busy={changes.isPending} disabled={!(text ?? '').trim()} onPress={() => changes.mutate((text ?? '').trim())} />
            <Button kind="secondary" label={t('common.cancel')} disabled={changes.isPending} onPress={() => setText(null)} />
          </View>
        </KeyboardAvoidingView>
      </Modal>
    </View>
  );
}

function ReviewCard({ item, pulls, mayMerge }: { item: T.ReviewItem; pulls?: T.ProjectPullRequests; mayMerge: boolean }) {
  const t = useT();
  const router = useRouter();
  const media = useQuery({ queryKey: ['media', item.ref], queryFn: () => api.agentMedia(item.ref) });
  const { pairs, rest: shots } = pairUp((media.data ?? []).filter((m) => !m.removed && (m.kind === 'screenshot' || m.kind === 'recording')));
  return (
    <View style={styles.card}>
      <Pressable onPress={() => router.push(chatPath(item.ref))} style={styles.cardHead}>
        <AgentAvatar ai={item.ai ?? ''} mood="idle" size={36} />
        <View style={styles.flex}>
          <Text style={styles.cardTitle}>{item.title || item.agent}</Text>
          <Text style={styles.cardSub} numberOfLines={1}>
            {[item.agent, item.branch].filter(Boolean).join(' · ')}
          </Text>
        </View>
      </Pressable>
      {item.summary ? <Markdown text={item.summary} /> : <Text style={styles.cardSub}>{t('review.noSummary')}</Text>}
      {item.changes && item.changes.files > 0 && (
        <Text style={styles.changes}>
          {t('review.changes', { files: item.changes.files })} <Text style={{ color: colors.emerald }}>+{item.changes.insertions}</Text> <Text style={{ color: colors.rose }}>−{item.changes.deletions}</Text>
        </Text>
      )}
      {pairs.map((p) => (
        <PairView key={p.key} pair={p} />
      ))}
      {shots.length > 0 && (
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.media}>
          {shots.map((m) => (
            <View key={m.id} style={styles.shot}>
              {m.kind === 'screenshot' ? (
                <Image source={mediaSource(mediaFilePath(m.id))} style={styles.shotImage} contentFit="cover" accessibilityLabel={m.name} />
              ) : (
                <View style={[styles.shotImage, styles.recording]}>
                  <Play size={22} color={colors.secondary} />
                </View>
              )}
              <Text style={styles.shotName} numberOfLines={1}>
                {m.name}
              </Text>
            </View>
          ))}
        </ScrollView>
      )}
      {item.pr && <PullRow project={item.project} pr={item.pr} pulls={pulls} mayMerge={mayMerge} />}
    </View>
  );
}

// PairView is a before/after pair, one half at a time: a swipe across it,
// or a tap, flips to the other.
function PairView({ pair }: { pair: Pair }) {
  const t = useT();
  const [side, setSide] = useState<Side>('before');
  const flip = () => setSide((s) => (s === 'before' ? 'after' : 'before'));
  const flipRef = useRef(flip);
  flipRef.current = flip;
  const swipe = useRef(
    PanResponder.create({
      onMoveShouldSetPanResponder: (_, g) => Math.abs(g.dx) > 12 && Math.abs(g.dx) > Math.abs(g.dy),
      onPanResponderRelease: (_, g) => {
        if (Math.abs(g.dx) > 40) flipRef.current();
      },
    }),
  ).current;
  const m = pair[side];
  const aspect = m.meta.width && m.meta.height ? m.meta.width / m.meta.height : 16 / 10;
  return (
    <View style={styles.pair} {...swipe.panHandlers}>
      <Pressable onPress={flip} accessibilityRole="button" accessibilityLabel={`${pair.name}: ${t(side === 'before' ? 'review.compare.before' : 'review.compare.after')}. ${t('mobile.review.swipeToFlip')}`}>
        {m.kind === 'screenshot' ? (
          <Image source={mediaSource(mediaFilePath(m.id))} style={[styles.pairImage, { aspectRatio: aspect }]} contentFit="contain" />
        ) : (
          <View style={[styles.pairImage, styles.recording, { aspectRatio: aspect }]}>
            <Play size={22} color={colors.secondary} />
          </View>
        )}
        <View style={styles.pairSide}>
          <Text style={styles.pairSideText}>{t(side === 'before' ? 'review.compare.before' : 'review.compare.after')}</Text>
        </View>
      </Pressable>
      <View style={styles.pairFoot}>
        <Text style={styles.shotName} numberOfLines={1}>
          {pair.name}
        </Text>
        <View style={styles.dots}>
          {(['before', 'after'] as const).map((s) => (
            <View key={s} style={[styles.dot, s === side && styles.dotOn]} />
          ))}
        </View>
        <Text style={styles.shotName}>{t('mobile.review.swipeToFlip')}</Text>
      </View>
    </View>
  );
}

function PullRow({ project, pr, pulls, mayMerge }: { project: string; pr: T.PullRequest; pulls?: T.ProjectPullRequests; mayMerge: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const remembered = useMergeMethod();
  const methods = pulls?.mergeMethods?.length ? pulls.mergeMethods : ['squash', 'merge', 'rebase'];
  const method = pickMethod(methods, remembered);
  const merge = useMutation({
    mutationFn: (m: string) => api.merge(project, pr.number, m),
    onSuccess: (_, m) => {
      void rememberMethod(m);
      Alert.alert(t('web.phone.merged', { number: pr.number }));
      void queryClient.invalidateQueries({ queryKey: ['review', project] });
      void queryClient.invalidateQueries({ queryKey: ['pulls', project] });
    },
    onError: (err) => Alert.alert(err instanceof Error ? err.message : String(err)),
  });
  const block = mergeBlock(pr, pulls);
  const confirm = (m: string) =>
    Alert.alert(t('review.mergeAsk', { number: pr.number }), `${pr.title}\n${t(`memory.pulls.method.${m as 'merge' | 'squash' | 'rebase'}`)} → ${pr.baseBranch ?? ''}`, [
      { text: t('common.cancel'), style: 'cancel' },
      { text: t('web.phone.merge'), onPress: () => merge.mutate(m) },
    ]);
  const chooseMethod = () =>
    Alert.alert(
      t('mobile.review.method'),
      undefined,
      [...methods.map((m) => ({ text: t(`memory.pulls.method.${m as 'merge' | 'squash' | 'rebase'}`), onPress: () => confirm(m) })), { text: t('common.cancel'), style: 'cancel' as const }],
    );
  return (
    <View style={styles.pr}>
      <View style={styles.prHead}>
        <PrChip pr={pr} />
        <Text style={styles.prTitle} numberOfLines={2}>
          {pr.title}
        </Text>
      </View>
      <Text style={styles.cardSub}>
        {[pr.draft ? t('agent.pr.draft') : t('agent.pr.open'), pr.checks ? t(`agent.pr.${pr.checks as 'passing' | 'failing' | 'pending'}`) : '', pr.additions !== undefined ? `+${pr.additions} −${pr.deletions ?? 0}` : ''].filter(Boolean).join(' · ')}
      </Text>
      {block && <Text style={styles.block}>{t(`review.mergeBlocked.${block}`, { base: pr.baseBranch ?? '' })}</Text>}
      {mayMerge && pr.state === 'open' && (
        <View style={styles.split}>
          <Button label={`${t('web.phone.merge')} · ${t(`memory.pulls.method.${method as 'merge' | 'squash' | 'rebase'}`)}`} icon={GitMerge} busy={merge.isPending} disabled={!!block} onPress={() => confirm(method)} style={styles.splitMain} />
          {methods.length > 1 && (
            <Pressable accessibilityRole="button" accessibilityLabel={t('mobile.review.method')} onPress={chooseMethod} disabled={!!block || merge.isPending} style={[styles.splitMore, !!block && { opacity: 0.5 }]}>
              <ChevronDown size={16} color={colors.title} />
            </Pressable>
          )}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  actions: { gap: 10 },
  row: { flexDirection: 'row', gap: 10 },
  flex: { flex: 1 },
  backdrop: { flex: 1, backgroundColor: 'rgba(0,0,0,0.6)', justifyContent: 'flex-end' },
  sheet: { backgroundColor: colors.overlay, borderTopLeftRadius: 20, borderTopRightRadius: 20, padding: 16, gap: 10, paddingBottom: 32 },
  sheetTitle: { color: colors.title, fontSize: 16, fontWeight: '600' },
  input: { minHeight: 110, maxHeight: 220, borderRadius: 12, backgroundColor: colors.raised, color: colors.primary, padding: 12, fontSize: 15, textAlignVertical: 'top' },
  root: { flex: 1, backgroundColor: colors.bg },
  content: { padding: 12, gap: 12, paddingBottom: 40 },
  emptyBox: { alignItems: 'center', gap: 8, padding: 24 },
  empty: { color: colors.subtle, fontSize: 14, textAlign: 'center' },
  cardHead: { flexDirection: 'row', alignItems: 'center', gap: 10 },
  section: { color: colors.faint, fontSize: 11, fontWeight: '700', textTransform: 'uppercase', letterSpacing: 1.3, marginTop: 8, paddingHorizontal: 4 },
  card: { borderRadius: 16, backgroundColor: colors.surface, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line, padding: 14, gap: 8 },
  cardTitle: { color: colors.title, fontSize: 16, fontWeight: '600' },
  cardSub: { color: colors.subtle, fontSize: 12 },
  changes: { color: colors.muted, fontSize: 12.5 },
  media: { gap: 10 },
  shot: { width: 150, gap: 4 },
  shotImage: { width: 150, height: 94, borderRadius: 10, backgroundColor: colors.raised },
  recording: { alignItems: 'center', justifyContent: 'center' },
  shotName: { color: colors.subtle, fontSize: 11 },
  pair: { gap: 6 },
  pairImage: { width: '100%', borderRadius: 10, backgroundColor: colors.raised },
  pairSide: { position: 'absolute', top: 8, left: 8, borderRadius: 6, backgroundColor: 'rgba(0,0,0,0.7)', paddingHorizontal: 6, paddingVertical: 2 },
  pairSideText: { color: '#fff', fontSize: 11, fontWeight: '600' },
  pairFoot: { flexDirection: 'row', alignItems: 'center', gap: 8, justifyContent: 'space-between' },
  dots: { flexDirection: 'row', gap: 4 },
  dot: { width: 6, height: 6, borderRadius: 3, backgroundColor: colors.faint },
  dotOn: { backgroundColor: colors.secondary },
  pr: { gap: 6, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line, paddingTop: 10 },
  prHead: { flexDirection: 'row', gap: 8, alignItems: 'center' },
  prTitle: { flex: 1, color: colors.primary, fontSize: 14, fontWeight: '500' },
  block: { color: colors.amber, fontSize: 12 },
  split: { flexDirection: 'row', gap: 1, marginTop: 4 },
  splitMain: { flex: 1, borderTopRightRadius: 0, borderBottomRightRadius: 0 },
  splitMore: { width: 44, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.brand500, borderTopRightRadius: 12, borderBottomRightRadius: 12 },
});
