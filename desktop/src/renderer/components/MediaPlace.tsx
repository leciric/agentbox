import { useQuery } from '@tanstack/react-query';
import { ChevronRight, ExternalLink, FolderGit2, GitPullRequest } from 'lucide-react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { projectLabel } from '../lib/projectName';
import { cn, timeAgo } from '../lib/utils';
import { Badge, type BadgeVariant } from './ui/badge';

const prVariant: Record<string, BadgeVariant> = { open: 'success', merged: 'brand', closed: 'danger' };

// MediaPlace is the strip under a media viewer's header that says where the
// item came from, for a viewer opened from a notification or the
// all-projects Media view: its project, its agent and what it was for, and
// the agent's pull request, each a link. fromNotice is when the notification
// that opened it was made.
export function MediaPlace({ item, fromNotice, onSelect }: { item: T.MediaItem; fromNotice?: string; onSelect: (view: View) => void }) {
  const [project, name] = item.agent.split('/');
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const fleet = useQuery({ queryKey: ['fleet', project], queryFn: () => api.fleet(project), enabled: !item.agentGone });
  const agent = fleet.data?.agents.find((a) => a.ref === item.agent);
  const title = agent?.title ?? item.agentTitle;
  const pr = agent?.pr;
  const projectName = projectLabel(project, projects.data);
  const variant = !pr || pr.draft ? 'default' : (prVariant[pr.state] ?? 'default');
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 border-b border-line bg-surface-faint px-5 py-2 text-[12.5px]" data-viewer-links>
      <button
        className="flex min-w-0 max-w-[30%] items-center gap-1.5 rounded-md px-1.5 py-0.5 text-muted transition hover:bg-surface-raised hover:text-primary"
        onClick={() => onSelect({ kind: 'project', project })}
        title={`Open ${projectName}`}
      >
        <FolderGit2 className="size-3.5 shrink-0" />
        <span className="truncate">{projectName}</span>
      </button>
      <ChevronRight className="size-3.5 shrink-0 text-faint" />
      {item.agentGone ? (
        <span className="flex min-w-0 max-w-[45%] items-center gap-1.5 px-1.5 py-0.5 text-muted">
          <span className="font-mono text-[12px]">{name}</span>
          <span className="truncate">(agent removed)</span>
        </span>
      ) : (
        <button
          className="flex min-w-0 max-w-[45%] items-center gap-1.5 rounded-md px-1.5 py-0.5 text-secondary transition hover:bg-surface-raised hover:text-primary"
          onClick={() => onSelect({ kind: 'agent', ref: item.agent, tab: 'chat' })}
          title={`Open ${name}`}
        >
          <span className="font-mono text-[12px]">{name}</span>
          {title && <span className="truncate text-muted">· {title}</span>}
        </button>
      )}
      {pr && (
        <button
          className="flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-muted transition hover:bg-surface-raised hover:text-primary"
          onClick={() => void window.agentbox.openExternal(pr.url)}
          title={pr.title}
        >
          <GitPullRequest className="size-3.5" />#{pr.number}
          {/* brand-300 is too faint on light surfaces. */}
          <Badge variant={variant} className={cn('ml-0.5', variant === 'brand' && '[:root[data-appearance=light]_&]:text-brand-600')}>
            {pr.draft ? 'draft' : pr.state}
          </Badge>
          <ExternalLink className="size-3 text-subtle" />
        </button>
      )}
      {fromNotice && <span className="ml-auto shrink-0 text-[11.5px] text-subtle">From a notification · {timeAgo(fromNotice)}</span>}
    </div>
  );
}
