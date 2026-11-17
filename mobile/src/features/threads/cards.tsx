// The cards that wait on you in a chat (#323): a permission the chat's own
// AI tool asks for, one of the project's agents asking it in its lead's chat
// (chat.agentPermission), and a question the lead passed on to you. Each is
// answered within the phone's scopes: "chat" for its own chat's permission,
// "answer" for an agent's and for questions (lanWrites in
// internal/daemon/lan.go). After t3code's PendingApprovalCard.tsx and
// PendingUserInputCard.tsx.
import { useMutation } from '@tanstack/react-query';
import { useRouter } from 'expo-router';
import { Check, ChevronRight, MessageCircleQuestion, ShieldAlert, ShieldCheck, ShieldOff, ShieldX, X, type LucideIcon } from 'lucide-react-native';
import { useState, type ReactNode } from 'react';
import { Alert, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import type * as T from '../../shared/api.ts';
import { api, isLead } from '../../connection/api.ts';
import { can, useSession } from '../../connection/session.ts';
import { useT } from '../../lib/i18n.tsx';
import { Button } from '../../ui/parts.tsx';
import { colors, mono } from '../../ui/theme.ts';

const fail = (err: unknown) => Alert.alert(err instanceof Error ? err.message : String(err));

// The three choices a card offers, in the desktop's order: deny, always, once.
function choices(p: T.ChatPermission): T.ChatPermissionOption[] {
  return [
    ...p.options.filter((o) => o.kind.startsWith('reject')).slice(0, 1),
    ...p.options.filter((o) => o.kind === 'allow_always').slice(0, 1),
    ...p.options.filter((o) => o.kind === 'allow_once').slice(0, 1),
  ];
}

function Options({ permission, onAnswer, busy }: { permission: T.ChatPermission; onAnswer: (id: string) => void; busy: boolean }) {
  const t = useT();
  return (
    <View style={styles.options}>
      {choices(permission).map((o) => (
        <Button
          key={o.id}
          label={t(o.kind.startsWith('reject') ? 'chat.permission.deny' : o.kind === 'allow_always' ? 'chat.permission.alwaysAllow' : 'chat.permission.allow')}
          icon={o.kind.startsWith('reject') ? X : o.kind === 'allow_always' ? ShieldCheck : Check}
          kind={o.kind === 'allow_once' ? 'primary' : o.kind.startsWith('reject') ? 'danger' : 'secondary'}
          disabled={busy}
          onPress={() => onAnswer(o.id)}
          style={styles.option}
        />
      ))}
    </View>
  );
}

// Head is a card's first line: its icon, then what it is, in the colour of
// its state.
function Head({ icon: Icon, color, children }: { icon: LucideIcon; color: string; children: ReactNode }) {
  return (
    <View style={styles.head}>
      <Icon size={16} color={color} strokeWidth={2} />
      <Text style={[styles.cardHead, { color }]} numberOfLines={1}>
        {children}
      </Text>
    </View>
  );
}

function Detail({ tool, title }: { tool?: T.ChatTool; title: string }) {
  const detail = tool?.command || tool?.paths?.[0];
  if (!detail || title.includes(detail)) return null;
  return (
    <Text style={[mono, styles.detail]} numberOfLines={4}>
      {detail}
    </Text>
  );
}

// PermissionCard is the chat's own AI tool asking.
export function PermissionCard({ chatRef, item, tool }: { chatRef: string; item: T.ChatItem; tool?: T.ChatTool }) {
  const t = useT();
  const session = useSession();
  const answer = useMutation({ mutationFn: (option: string) => api.answerPermission(chatRef, item.id, option), onError: fail });
  const perm = item.permission!;
  // A lead's own permission is the chat's; an agent's, an answer too.
  const allowed = can(session, 'chat') || (!isLead(chatRef) && can(session, 'answer'));
  return (
    <View style={[styles.card, styles.waiting]}>
      <Head icon={ShieldAlert} color={colors.amber}>
        {t('chat.activity.permission')}
      </Head>
      <Text style={styles.cardBody}>{perm.title}</Text>
      <Detail tool={tool} title={perm.title} />
      {allowed ? <Options permission={perm} busy={answer.isPending} onAnswer={(id) => answer.mutate(id)} /> : <Text style={styles.cant}>{t('mobile.chat.cantAnswer')}</Text>}
    </View>
  );
}

// AgentPermissionCard is one of the project's agents asking, in its lead's
// chat: answered for the agent, or opened.
export function AgentPermissionCard({ item }: { item: T.ChatItem }) {
  const t = useT();
  const router = useRouter();
  const session = useSession();
  const ap = item.agentPermission!;
  const answer = useMutation({ mutationFn: (option: string) => api.answerPermission(ap.ref, ap.item, option), onError: fail });
  const p = ap.permission;
  const kind = p.outcome ? p.options.find((o) => o.id === p.outcome)?.kind : undefined;
  const state = !p.outcome ? 'waiting' : !kind ? 'gone' : kind.startsWith('reject') ? 'denied' : kind === 'allow_always' ? 'approvedAlways' : 'approved';
  const waiting = state === 'waiting';
  const [Icon, tone] = waiting ? [ShieldAlert, colors.amber] : state === 'denied' ? [ShieldX, colors.rose] : state === 'gone' ? [ShieldOff, colors.subtle] : [ShieldCheck, colors.emerald];
  return (
    <View style={[styles.card, waiting && styles.waiting]}>
      <View style={styles.cardTop}>
        <View style={styles.flex}>
          <Head icon={Icon} color={waiting ? colors.amber : tone}>
            <Text style={!waiting && { color: colors.secondary }}>{t('chat.agentPermission.asks', { agent: ap.title || ap.agent, action: ap.tool?.kind ?? '' })}</Text>
          </Head>
        </View>
        <Pressable onPress={() => router.push(`/chat/${ap.ref.split('/').map(encodeURIComponent).join('/')}`)} hitSlop={8} style={styles.open}>
          <Text style={styles.link}>{t('chat.agentPermission.open')}</Text>
          <ChevronRight size={13} color={colors.subtle} />
        </Pressable>
      </View>
      <Text style={[styles.cardBody, !waiting && { color: colors.muted }]}>{p.title}</Text>
      <Detail tool={ap.tool} title={p.title} />
      {waiting ? (
        can(session, 'answer') || can(session, 'chat') ? (
          <Options permission={p} busy={answer.isPending} onAnswer={(id) => answer.mutate(id)} />
        ) : (
          <Text style={styles.cant}>{t('mobile.chat.cantAnswer')}</Text>
        )
      ) : (
        <Text style={[styles.outcome, { color: state === 'denied' ? colors.rose : state === 'gone' ? colors.subtle : colors.emerald }]}>{t(`chat.agentPermission.${state}`)}</Text>
      )}
    </View>
  );
}

// QuestionCard is a question the lead couldn't answer and passed on to you.
export function QuestionCard({ question }: { question: T.Question }) {
  const t = useT();
  const session = useSession();
  const [text, setText] = useState('');
  const answer = useMutation({ mutationFn: () => api.answerQuestion(question.project, question.id, text.trim()), onError: fail, onSuccess: () => setText('') });
  return (
    <View style={[styles.card, styles.waiting]}>
      <Head icon={MessageCircleQuestion} color={colors.amber}>
        {t('mobile.question.asks', { agent: question.agent })}
      </Head>
      <Text style={styles.cardBody}>{question.question}</Text>
      {question.context ? <Text style={styles.context}>{question.context}</Text> : null}
      {can(session, 'answer') ? (
        <View style={styles.answer}>
          <TextInput value={text} onChangeText={setText} placeholder={t('mobile.question.placeholder')} placeholderTextColor={colors.faint} style={styles.input} multiline />
          <Button label={t('mobile.question.send')} busy={answer.isPending} disabled={!text.trim()} onPress={() => answer.mutate()} />
        </View>
      ) : (
        <Text style={styles.cant}>{t('mobile.chat.cantAnswer')}</Text>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  card: { borderRadius: 14, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.lineStrong, backgroundColor: colors.surface, padding: 12, gap: 6, marginVertical: 6 },
  waiting: { borderColor: 'rgba(252,211,77,0.3)', backgroundColor: 'rgba(251,191,36,0.07)' },
  cardTop: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  head: { flexDirection: 'row', alignItems: 'center', gap: 7 },
  flex: { flex: 1, minWidth: 0 },
  open: { flexDirection: 'row', alignItems: 'center', gap: 2 },
  cardHead: { flex: 1, color: colors.amber, fontSize: 13, fontWeight: '600' },
  cardBody: { color: colors.primary, fontSize: 14, lineHeight: 20 },
  context: { color: colors.muted, fontSize: 13, lineHeight: 18 },
  detail: { color: colors.muted, fontSize: 12 },
  link: { color: colors.subtle, fontSize: 12 },
  options: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, justifyContent: 'flex-end', marginTop: 4 },
  option: { minHeight: 36, paddingHorizontal: 12 },
  outcome: { fontSize: 12 },
  cant: { color: colors.subtle, fontSize: 12, lineHeight: 17 },
  answer: { gap: 8, marginTop: 4 },
  input: { color: colors.primary, backgroundColor: colors.surface, borderColor: colors.lineStrong, borderWidth: StyleSheet.hairlineWidth, borderRadius: 10, paddingHorizontal: 10, paddingVertical: 8, fontSize: 14, minHeight: 40 },
});
