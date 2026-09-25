import { useQuery } from '@tanstack/react-query';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { ChatTab } from './chat/ChatTab';

// leadAgentFrom builds the Agent ChatTab expects out of a project's chat: the
// lead runs on the host, with no machine of its own, so it's not a real entry
// in the agents list.
export function leadAgentFrom(project: T.Project, info: T.ProjectChat): T.Agent {
  return {
    ref: info.ref,
    project: project.name,
    name: T.LeadName,
    title: 'Project chat',
    instance: '',
    ai: 'claude',
    autonomous: false,
    branch: '', // it stands on baseRef, detached, and commits nothing
    baseRef: info.baseRef || project.branch,
    baseCommit: '',
    worktree: info.worktree || '',
    source: '',
    claudeAccount: project.claudeAccount,
    githubAccount: '', // the lead has no shell, so no use for a GitHub token
    interface: 'chat',
    chat: info.chat,
    // There is no machine to start: "running" is how ChatTab asks whether the
    // conversation can run at all, and a project's chat always can.
    state: 'running',
    ip: '',
    limits: { cpu: '', allowance: '', memory: '' }, // no machine to cap
    createdAt: project.createdAt,
  };
}

// The project's chat, driven by its lead. ChatTab is shared with the agents'
// chats, so the lead is handed to it as an agent.
//
// The conversation is what you and the lead said to each other, and only that.
// What the project's agents reported is a thread per agent in the rail beside
// the whole view (AgentRail), rather than notices in the middle of it.
//
// Opening it starts the lead's AI tool, with no turn, so its settings can be
// chosen before the first message. A project whose chat is never opened
// costs nothing: adding one makes no lead.
export function ProjectChatPanel({ project }: { project: T.Project }) {
  const chat = useQuery({ queryKey: ['projectChat', project.name], queryFn: () => api.projectChat(project.name) });
  const info = chat.data;

  if (!info) {
    return <div className="flex h-full items-center justify-center text-sm text-subtle">{chat.error ? 'The chat is unavailable.' : 'Loading…'}</div>;
  }

  return <ChatTab agent={leadAgentFrom(project, info)} starting={false} onStart={() => {}} />;
}
