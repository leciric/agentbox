// The people of a shared project on the phone: who wrote a message, who else
// is in the project or on an agent, where each one is, and who is typing.
// After the desktop's components/People.tsx, a person's colour and initials
// from the same file (shared/people.ts). Nothing here is a control: being
// there is only shown, as on the desktop.
import { useQuery } from '@tanstack/react-query';
import { StyleSheet, Text, View } from 'react-native';
import type * as T from '../../shared/api.ts';
import { api } from '../../connection/api.ts';
import { useT } from '../../lib/i18n.tsx';
import { initials, personColor } from '../../shared/people.ts';
import { colors, onBright, personColors } from '../../ui/theme.ts';
import { hereKey, inChat, onAgent, peopleIn, seedHere, typingLine, whereLine } from './here.ts';

type Who = Pick<T.Person, 'id' | 'name'>;

// personHue is a person's colour: the same for the same name on every screen,
// on the desktop and on every AgentBox.
export const personHue = (person: Pick<T.Person, 'name'>): string => personColors[personColor(person)];

// usePeopleIn is who else is in a project now. The daemon's events keep it
// current (connection/events.ts); a daemon that doesn't say, from before
// phones could read it, leaves it empty until somebody moves.
export function usePeopleIn(project: string | undefined): T.PersonHere[] {
  const held = useQuery({ queryKey: hereKey, queryFn: () => api.whoIsHere().then(seedHere), staleTime: Infinity, retry: false }).data;
  return peopleIn(held, project);
}

const sizes = { xs: { box: 18, font: 9 }, sm: { box: 24, font: 11 } };

// PersonAvatar is a person's initials on their colour. Its ring is the
// screen's own colour, so avatars that overlap stay apart.
export function PersonAvatar({ person, size = 'sm', overlap }: { person: Who; size?: 'xs' | 'sm'; overlap?: boolean }) {
  const { box, font } = sizes[size];
  return (
    <View
      accessible
      accessibilityLabel={person.name}
      style={[styles.avatar, { width: box, height: box, borderRadius: box / 2, backgroundColor: personHue(person) }, overlap && { marginLeft: -Math.round(box * 0.3) }]}
    >
      <Text style={[styles.initials, { fontSize: font, lineHeight: font + 2 }]}>{initials(person.name)}</Text>
    </View>
  );
}

// Avatars is a row of people, overlapping: the first few and how many more.
export function Avatars({ people, max = 3, size = 'sm' }: { people: readonly Who[]; max?: number; size?: 'xs' | 'sm' }) {
  const t = useT();
  if (people.length === 0) return null;
  const shown = people.slice(0, max);
  const more = people.length - shown.length;
  return (
    <View accessible accessibilityLabel={people.map((p) => p.name).join(', ')} style={styles.avatars}>
      {shown.map((p, i) => (
        <PersonAvatar key={p.id} person={p} size={size} overlap={i > 0} />
      ))}
      {more > 0 && <Text style={styles.more}>{t('people.more', { count: more })}</Text>}
    </View>
  );
}

// AuthorName heads someone else's message: their avatar and name, in their colour.
export function AuthorName({ person }: { person: Who }) {
  return (
    <View accessible accessibilityLabel={person.name} style={styles.author}>
      <PersonAvatar person={person} size="xs" />
      <Text numberOfLines={1} style={[styles.authorName, { color: personHue(person) }]}>
        {person.name}
      </Text>
    </View>
  );
}

// PresenceLine is everyone else in a project, each with where they are.
export function PresenceLine({ people }: { people: readonly T.PersonHere[] }) {
  const t = useT();
  if (people.length === 0) return null;
  return (
    <View style={styles.presence}>
      {people.map((p) => (
        <View key={p.id} style={styles.presenceItem}>
          <PersonAvatar person={p} size="xs" />
          <Text numberOfLines={1} style={styles.caption}>
            {whereLine(t, p)}
          </Text>
        </View>
      ))}
    </View>
  );
}

// TypingLine says who else is writing a message in what is on screen.
export function TypingLine({ people }: { people: readonly T.PersonHere[] }) {
  const t = useT();
  const text = typingLine(t, people);
  if (!text) return null;
  return (
    <View accessibilityRole="text" accessibilityLiveRegion="polite" style={styles.presenceItem}>
      <Avatars people={people.filter((p) => p.typing)} size="xs" />
      <Text numberOfLines={1} style={styles.caption}>
        {text}
      </Text>
    </View>
  );
}

// HerePeople is who else is in a project, or on one of its agents, as
// avatars, for a screen's header.
export function HerePeople({ project, agent }: { project: string; agent?: string }) {
  const all = usePeopleIn(project);
  return <Avatars people={agent ? onAgent(all, agent) : all} />;
}

// ChatPeople, above a chat's composer, is where everyone else in its project
// is, and who is typing in this chat: a session of the project's chat, or an
// agent's own.
export function ChatPeople({ project, on }: { project: string; on: { session?: string; agent?: string } }) {
  const all = usePeopleIn(project);
  if (all.length === 0) return null;
  const here = inChat(all, on);
  return (
    <View style={styles.chatPeople}>
      <TypingLine people={here} />
      <PresenceLine people={all.filter((p) => !p.typing || !here.includes(p))} />
    </View>
  );
}

const styles = StyleSheet.create({
  avatar: { alignItems: 'center', justifyContent: 'center', borderWidth: 1.5, borderColor: colors.bg },
  initials: { color: onBright, fontWeight: '700', textAlign: 'center', includeFontPadding: false },
  avatars: { flexDirection: 'row', alignItems: 'center', flexShrink: 0 },
  more: { color: colors.subtle, fontSize: 11, marginLeft: 4 },
  author: { flexDirection: 'row', alignItems: 'center', gap: 6, paddingLeft: 4, marginBottom: 3, maxWidth: '85%' },
  authorName: { flexShrink: 1, fontSize: 12.5, fontWeight: '500' },
  presence: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 14, rowGap: 4 },
  presenceItem: { flexDirection: 'row', alignItems: 'center', gap: 6, minWidth: 0, flexShrink: 1 },
  caption: { color: colors.subtle, fontSize: 12, flexShrink: 1 },
  chatPeople: { paddingHorizontal: 14, paddingTop: 8, gap: 4 },
});
