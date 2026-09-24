import { useMutation, useQuery } from '@tanstack/react-query';
import { ListChecks, LoaderCircle } from 'lucide-react';
import { useState } from 'react';
import { api } from '../lib/api';
import { cn, duration, errorMessage, timeAgo } from '../lib/utils';
import { JobProgress } from './JobProgress';
import { JobStatusBadge } from './state';
import { Button } from './ui/button';
import { Card, EmptyState, Notice } from './ui/card';

export function JobsView() {
  const jobs = useQuery({ queryKey: ['jobs'], queryFn: api.jobs });
  const [selected, setSelected] = useState<string | null>(null);
  const current = jobs.data?.find((j) => j.id === selected);
  const cancel = useMutation({ mutationFn: (id: string) => api.cancelJob(id) });

  if (jobs.data?.length === 0) {
    return (
      <EmptyState icon={ListChecks} title="Nothing has run yet">
        Creating agents, forking, restoring snapshots and building the base image run as jobs. Their progress and logs show up here.
      </EmptyState>
    );
  }

  return (
    <div className="grid h-full grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)] gap-4 overflow-hidden p-6">
      <Card title="Recent jobs" icon={ListChecks} className="flex min-h-0 flex-col" bodyClassName="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        <div className="grid gap-1" aria-label="Jobs">
          {jobs.data?.map((job) => (
            <button
              key={job.id}
              className={cn(
                'flex items-center gap-3 rounded-xl px-3 py-2.5 text-left text-[13px] transition hover:bg-surface',
                job.id === selected && 'bg-surface-raised shadow-[inset_0_1px_0_rgb(255_255_255/0.05)]',
              )}
              onClick={() => setSelected(job.id)}
            >
              <JobStatusBadge status={job.status} />
              <span className="font-medium text-secondary">{job.kind}</span>
              <span className="truncate text-subtle">{job.target}</span>
              <span className="ml-auto shrink-0 text-xs text-subtle">{timeAgo(job.createdAt)}</span>
              <span className="w-14 shrink-0 text-right font-mono text-[11px] tabular-nums text-faint">{duration(job.createdAt, job.finishedAt)}</span>
            </button>
          ))}
        </div>
      </Card>
      <Card
        title={current ? `${current.kind} ${current.target}` : 'Job log'}
        description={current ? `job ${current.id} · ${duration(current.createdAt, current.finishedAt)}` : undefined}
        className="flex min-h-0 flex-col"
        bodyClassName="min-h-0 flex-1 overflow-y-auto"
        action={
          current &&
          (current.status === 'running' ? (
            <Button size="sm" disabled={cancel.isPending} onClick={() => cancel.mutate(current.id)}>
              {cancel.isPending && <LoaderCircle className="animate-spin" />}
              Cancel and roll back
            </Button>
          ) : (
            <JobStatusBadge status={current.status} />
          ))
        }
      >
        {cancel.error && <Notice className="mb-3">{errorMessage(cancel.error)}</Notice>}
        {selected ? <JobProgress key={selected} jobId={selected} header={false} /> : <p className="text-sm text-subtle">Pick a job to see its log.</p>}
      </Card>
    </div>
  );
}
