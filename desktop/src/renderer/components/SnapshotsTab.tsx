import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, GitCommitHorizontal, GitFork, LoaderCircle, RotateCcw, X } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { errorMessage, shortCommit, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { JobProgress } from './JobProgress';
import { Button } from './ui/button';
import { Card, Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Label } from './ui/input';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

export function SnapshotsTab({ agent, onOpenAgent }: { agent: T.Agent; onOpenAgent: (ref: string) => void }) {
  const queryClient = useQueryClient();
  const snapshots = useQuery({ queryKey: ['snapshots', agent.ref], queryFn: () => api.snapshots(agent.ref) });
  const [name, setName] = useState('');
  const [consistent, setConsistent] = useState(false);
  const [restoreJob, setRestoreJob] = useState<{ id: string; snapshot: string } | null>(null);
  const [restoring, setRestoring] = useState<T.Snapshot | null>(null);
  const [deleting, setDeleting] = useState<T.Snapshot | null>(null);
  const [forking, setForking] = useState<T.Snapshot | null>(null);

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['snapshots', agent.ref] });
  const take = useMutation({
    mutationFn: () => api.takeSnapshot(agent.ref, { name: name.trim() || undefined, consistent: consistent || undefined }),
    onSuccess: async (snapshot) => {
      setName('');
      toast(`Snapshot “${snapshot.name}” taken`);
      await refresh();
    },
  });

  return (
    <div className="h-full overflow-y-auto p-5">
      <div className="grid gap-4">
        <Card title="Take a snapshot" icon={Camera} description="The whole machine and the worktree, including uncommitted and untracked files. Running processes aren't kept.">
          <form
            className="flex flex-wrap items-end gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              take.mutate();
            }}
          >
            <Field label="Name" htmlFor="snapshot-name">
              <Input id="snapshot-name" className="w-64 font-mono text-[13px]" placeholder="automatic (date and time)" value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
            <div className="flex h-9 items-center gap-2.5">
              <Switch id="snapshot-consistent" checked={consistent} onCheckedChange={setConsistent} />
              <Label htmlFor="snapshot-consistent" className="font-normal">
                Pause the agent while snapshotting
              </Label>
            </div>
            <Button type="submit" variant="primary" className="ml-auto" disabled={take.isPending || agent.state === 'incomplete' || agent.state === 'initializing'}>
              {take.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
              Take snapshot
            </Button>
          </form>
          {take.error && <Notice className="mt-3">{errorMessage(take.error)}</Notice>}
        </Card>

        {restoreJob && (
          <Card
            title={`Restoring ${restoreJob.snapshot}`}
            icon={RotateCcw}
            action={
              <Button variant="ghost" size="icon-sm" aria-label="Hide" onClick={() => setRestoreJob(null)}>
                <X />
              </Button>
            }
          >
            <JobProgress jobId={restoreJob.id} onDone={() => void refresh()} />
          </Card>
        )}

        <Card title={`Snapshots (${snapshots.data?.length ?? 0})`}>
          {snapshots.error && <Notice>{errorMessage(snapshots.error)}</Notice>}
          {snapshots.data?.length === 0 && <p className="py-4 text-sm text-subtle">No snapshots yet.</p>}
          <ol className="relative grid gap-2" aria-label="Snapshots">
            {snapshots.data?.map((snapshot, i) => (
              <li
                key={snapshot.name}
                data-snapshot={snapshot.name}
                className="group relative flex items-center gap-4 rounded-xl border border-line-faint bg-surface-faint py-2.5 pl-10 pr-3 transition hover:border-line-strong hover:bg-surface"
              >
                <span className="absolute left-4 top-1/2 size-2.5 -translate-y-1/2 rounded-full border-2 border-brand-400 bg-ink" />
                {i < (snapshots.data?.length ?? 0) - 1 && <span className="absolute left-[21px] top-[calc(50%+8px)] h-[calc(100%-4px)] w-px bg-line-strong" />}
                <div className="min-w-0">
                  <div className="font-mono text-[13px] text-primary">{snapshot.name}</div>
                  <div className="mt-0.5 flex items-center gap-2 text-[11.5px] text-subtle">
                    <span title={new Date(snapshot.createdAt).toLocaleString()}>{timeAgo(snapshot.createdAt)}</span>
                    <span className="flex items-center gap-1 font-mono">
                      <GitCommitHorizontal className="size-3" />
                      {shortCommit(snapshot.head)}
                    </span>
                  </div>
                </div>
                <div className="ml-auto flex items-center gap-1">
                  <Button size="sm" onClick={() => setRestoring(snapshot)}>
                    <RotateCcw />
                    Restore
                  </Button>
                  <Button size="sm" onClick={() => setForking(snapshot)}>
                    <GitFork />
                    Fork
                  </Button>
                  <Tip label="Delete">
                    <Button variant="ghost" size="icon-sm" aria-label={`Delete snapshot ${snapshot.name}`} onClick={() => setDeleting(snapshot)}>
                      <X />
                    </Button>
                  </Tip>
                </div>
              </li>
            ))}
          </ol>
        </Card>
      </div>

      <ConfirmDialog
        open={restoring !== null}
        onOpenChange={(open) => !open && setRestoring(null)}
        title={`Restore ${agent.title || agent.name} to “${restoring?.name}”?`}
        description={
          <>
            The machine restarts from the snapshot, and the branch and files go back to how they were. The current state is kept on{' '}
            <Code>refs/agentbox/pre-restore/…</Code>, so later commits aren't lost.
          </>
        }
        confirmLabel="Restore"
        destructive
        onConfirm={async () => {
          const snapshot = restoring!.name;
          const job = await api.restore(agent.ref, snapshot);
          setRestoreJob({ id: job.id, snapshot });
        }}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Delete snapshot “${deleting?.name}”?`}
        description="Deletes the machine snapshot and its worktree commit."
        confirmLabel="Delete"
        destructive
        onConfirm={async () => {
          await api.deleteSnapshot(agent.ref, deleting!.name);
          await refresh();
        }}
      />
      <ForkDialog agent={agent} snapshot={forking} onClose={() => setForking(null)} onOpenAgent={onOpenAgent} />
    </div>
  );
}

function ForkDialog({
  agent,
  snapshot,
  onClose,
  onOpenAgent,
}: {
  agent: T.Agent;
  snapshot: T.Snapshot | null;
  onClose: () => void;
  onOpenAgent: (ref: string) => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [title, setTitle] = useState('');
  const [job, setJob] = useState<T.Job | null>(null);
  const [failed, setFailed] = useState(false);
  const fork = useMutation({
    mutationFn: () => api.fork(agent.ref, { name: name.trim() || undefined, title: title.trim() || undefined, snapshot: snapshot!.name }),
    onSuccess: setJob,
  });
  const close = () => {
    setName('');
    setTitle('');
    setJob(null);
    setFailed(false);
    fork.reset();
    onClose();
  };

  return (
    <Dialog open={snapshot !== null} onOpenChange={(open) => !open && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Fork {agent.name}@{snapshot?.name}
          </DialogTitle>
          <DialogDescription>A new agent with a copy of the machine, a new branch at the snapshot's commit, and the snapshot's uncommitted and untracked files.</DialogDescription>
        </DialogHeader>
        {job ? (
          <>
            <JobProgress
              jobId={job.id}
              onDone={async (done) => {
                if (done.status !== 'succeeded') return setFailed(true);
                await queryClient.invalidateQueries({ queryKey: ['agents'] });
                const ref = (done.result as T.Agent).ref;
                close();
                onOpenAgent(ref);
              }}
            />
            <DialogFooter>
              <Button onClick={close}>{failed ? 'Close' : 'Keep running in background'}</Button>
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              fork.mutate();
            }}
          >
            <Field label="Title" htmlFor="fork-title" hint={agent.title ? `Default: ${agent.title} (fork)` : 'Optional.'}>
              <Input id="fork-title" autoFocus value={title} onChange={(event) => setTitle(event.target.value)} />
            </Field>
            <Field label="Name" htmlFor="fork-name" hint="Optional: the next free agent-NN.">
              <Input id="fork-name" className="font-mono text-[13px]" placeholder="agent-NN" value={name} onChange={(event) => setName(event.target.value)} />
            </Field>
            {fork.error && <Notice>{errorMessage(fork.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={close}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={fork.isPending}>
                {fork.isPending ? <LoaderCircle className="animate-spin" /> : <GitFork />}
                Fork
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
