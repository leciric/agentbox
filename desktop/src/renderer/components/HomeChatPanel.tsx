import { useQuery } from '@tanstack/react-query';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import { t, useT } from '../lib/i18n';
import { useChatOpenAt } from '../lib/reveal';
import { ChatHeaderControls, ChatTab } from './chat/ChatTab';

// homeAgentFrom builds the Agent ChatTab expects out of the Home chat: a lead
// like a project's, kept under T.HomeProject, standing in ~/.agentbox rather
// than in a worktree.
export function homeAgentFrom(info: T.ProjectChat): T.Agent {
  return {
    ref: info.ref,
    project: T.HomeProject,
    name: T.LeadName,
    title: t('chat.home.agentTitle'),
    instance: '',
    ai: 'claude',
    autonomous: false,
    branch: '',
    baseRef: '',
    baseCommit: '',
    worktree: info.worktree || '',
    source: '',
    claudeAccount: '',
    githubAccount: '',
    interface: 'chat',
    chat: info.chat,
    state: 'running', // no machine: the chat can always run
    ip: '',
    createdAt: '',
  };
}

// The Home chat: the user's main chat, across every project and tied to none.
// Its tools list the projects and their agents, create agents, tell a
// project's own chat something, search any project's memory and add projects,
// so it's there even before the first project is.
export function HomeChatPanel() {
  const t = useT();
  const chat = useQuery({ queryKey: ['projectChat', T.HomeProject], queryFn: () => api.projectChat(T.HomeProject) });
  const info = chat.data;
  const openAt = useChatOpenAt(T.HomeProject);
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-3 px-4 pb-3 pt-5 md:px-8">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight text-title">{t('chat.home.title')}</h1>
          <p className="mt-0.5 text-[13px] text-muted">{t('chat.home.subtitle')}</p>
        </div>
        {info && (
          <div className="ml-auto flex shrink-0 items-center gap-2">
            <ChatHeaderControls agent={homeAgentFrom(info)} />
          </div>
        )}
      </div>
      <div className="mx-2 mb-2 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line md:mx-6 md:mb-6">
        {info ? (
          <ChatTab agent={homeAgentFrom(info)} starting={false} onStart={() => {}} openAt={openAt} />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-subtle">{chat.error ? t('chat.unavailable') : t('common.loading')}</div>
        )}
      </div>
    </div>
  );
}
