import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { addFetchedLog, useJobLog } from '../lib/events';
import { cn, duration } from '../lib/utils';
import { JobStatusBadge } from './state';
import { Notice } from './ui/card';

// JobProgress streams a job's log and status. onDone runs once, when the job
// has finished (straight away if it already had).
export function JobProgress({
  jobId,
  onDone,
  className,
  logClassName,
  header = true,
}: {
  jobId: string;
  onDone?: (job: T.Job) => void;
  className?: string;
  logClassName?: string;
  header?: boolean;
}) {
  const job = useQuery({
    queryKey: ['job', jobId],
    queryFn: () => api.job(jobId),
    // Events keep this up to date; polling covers a reconnecting event stream.
    refetchInterval: (query) => (query.state.data?.status === 'running' ? 3_000 : false),
  });
  const lines = useJobLog(jobId);
  const log = useRef<HTMLDivElement>(null);
  const done = useRef(false);
  const [, setTick] = useState(0);
  const running = !job.data || job.data.status === 'running';

  useEffect(() => {
    void api.jobLog(jobId).then((text) => addFetchedLog(jobId, text), () => {});
  }, [jobId]);

  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => setTick((n) => n + 1), 500);
    return () => clearInterval(timer);
  }, [running]);

  useEffect(() => {
    if (!job.data || job.data.status === 'running' || done.current) return;
    done.current = true;
    void api.jobLog(jobId).then((text) => addFetchedLog(jobId, text), () => {});
    onDone?.(job.data);
  }, [job.data, jobId, onDone]);

  useEffect(() => {
    log.current?.scrollTo({ top: log.current.scrollHeight });
  }, [lines]);

  return (
    <div className={cn('grid gap-3', className)} data-job={jobId}>
      {header && (
        <div className="flex items-center gap-2 text-sm">
          <JobStatusBadge status={job.data?.status ?? 'running'} />
          <span className="text-tertiary">{job.data ? `${job.data.kind} ${job.data.target}` : `job ${jobId}`}</span>
          <span className="ml-auto font-mono text-xs tabular-nums text-subtle">{job.data && duration(job.data.createdAt, job.data.finishedAt)}</span>
        </div>
      )}
      <div
        ref={log}
        className={cn('h-64 overflow-auto rounded-xl border border-line bg-well p-3.5 font-mono text-[11.5px] leading-relaxed text-subtle', logClassName)}
        aria-label="Job log"
      >
        {lines.map((line = '', i) => (
          <div key={i} className={cn('whitespace-pre-wrap break-all', line.startsWith('==>') && 'text-secondary')}>
            {line.startsWith('==>') ? (
              <>
                <span className="text-brand-400">==&gt;</span>
                {line.slice(3)}
              </>
            ) : (
              line || ' '
            )}
          </div>
        ))}
      </div>
      {job.data?.error && <Notice>{job.data.error}</Notice>}
    </div>
  );
}
