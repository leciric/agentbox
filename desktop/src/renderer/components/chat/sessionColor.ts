import { useQuery } from '@tanstack/react-query';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { sessionColor } from '../../lib/sessions';

// A chat's sessions are told apart by colour (LeadSession.Color) once it has
// more than one: a thin border around the shown session's chat
// (ProjectChatPanel), and the same on the cards of the agents it made
// (AgentList), whose topic is in the card's tooltip. What each colour looks like is styles.css' [data-session-color].

// useAgentSession is the session of agent's project chat that owns it, the
// one its notices go to, when the chat shows colours: undefined for an agent
// no session owns, and while the chat has one session.
export function useAgentSession(agent: T.Agent): { session: T.LeadSession; color: string } | undefined {
  const sessions = useQuery({ queryKey: ['leadSessions', agent.project], queryFn: () => api.leadSessions(agent.project), enabled: !!agent.leadSession });
  const list = sessions.data ?? [];
  const own = list.find((s) => s.id === agent.leadSession);
  const color = sessionColor(list, own);
  return own && color ? { session: own, color } : undefined;
}
