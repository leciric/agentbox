// The project base, in the app.
//
// A base is a photograph of one machine: every new agent of the project is
// copied from it. That is the whole model, and this panel exists to teach it,
// because the question users ask — "can the two images be merged?" — has the
// answer "no", and everything else follows from that. You update a base by
// creating an agent *from the last photograph*, changing what has gone stale,
// and taking a new one. So Refresh never offers a clean start: an agent that
// began clean would, the moment you saved from it, throw away everything the
// old base had.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, Ellipsis, Layers, LoaderCircle, RefreshCw, RotateCcw, Trash2, TriangleAlert } from 'lucide-react';
import { useCallback, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { JobProgress } from './JobProgress';
import { Button } from './ui/button';
import { Card, Code, Notice, Row } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Select, SelectOption } from './ui/select';

// What the refresh agent is told to do. It is a brief rather than a command
// because the point of a base is the machine around the worktree, and only an
// agent that understands that can tell what has gone stale. The last line is
// the one that matters most: a disk image nobody can read is how a project
// ends up not daring to touch its own base.
const refreshTask = `Bring this project's base machine up to date.

You were created **from the project's current base**, so this machine already has everything that base had: the runtimes, the packages, the Docker images and the caches. Your job is to update what has gone stale, not to set the project up from scratch.

- Update the language runtimes and package managers this project uses (mise, npm or pnpm, pip, Go — whatever is here), and the system packages.
- Pull the Docker images the project runs, so a new agent starts with them warm.
- Install the project's dependencies and check the application still runs: build it, start it, run its tests.
- Write down what is installed and at what version — in the repository, or in the project's notes — so the knowledge isn't trapped inside a disk image.

Don't change application code to make something pass; say so instead. When you're done, say what you updated and what everything is on now. The machine is then saved as the project's new base.`;

export function ProjectBasePanel({ project, className, onOpenAgent }: { project: T.Project; className?: string; onOpenAgent: (ref: string) => void }) {
  const name = project.name;
  const queryClient = useQueryClient();
  const base = useQuery({ queryKey: ['base', name], queryFn: () => api.base(name) });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const image = useQuery({ queryKey: ['image'], queryFn: api.image, staleTime: Infinity });
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [reverting, setReverting] = useState(false);
  const [dropping, setDropping] = useState(false);
  const mine = (agents.data ?? []).filter((a) => a.project === name);
  const previous = base.data?.previous;
  const refresh = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: ['base', name] });
  }, [name, queryClient]);

  return (
    <Card
      className={className}
      title="Project base"
      icon={Layers}
      description="A photograph of one machine. Every new agent of this project is copied from it."
      action={
        <Menu>
          <MenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" aria-label="Project base actions">
              <Ellipsis />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuItem
              icon={Camera}
              disabled={mine.length === 0}
              hint={mine.length === 0 ? 'no agents' : undefined}
              onSelect={() => setSaving(true)}
            >
              Save from an agent…
            </MenuItem>
            {previous && (
              <MenuItem icon={Trash2} onSelect={() => setDropping(true)}>
                Drop what the last save kept
              </MenuItem>
            )}
            <MenuItem
              icon={RotateCcw}
              destructive
              disabled={!base.data}
              hint={base.data ? undefined : 'no base'}
              onSelect={() => setRemoving(true)}
            >
              Go back to the plain image
            </MenuItem>
          </MenuContent>
        </Menu>
      }
    >
      {base.data ? (
        <>
          <Row label="Saved from" mono>
            {base.data.savedFrom}
          </Row>
          <Row label="Saved">
            <Age iso={base.data.savedAt} />
          </Row>
          <Row label="Snapshot" mono>
            <span className="truncate" title={base.data.snapshot}>
              {base.data.snapshot}
            </span>
          </Row>
          {stale(base.data.savedAt) && (
            <Notice tone="warning" className="mt-3.5">
              This base is {age(base.data.savedAt)}. Every agent of {name} starts from the machine as it was then, and spends its first minutes catching
              up. Refresh it: start an agent from this base, let it update what has drifted, and save that machine.
            </Notice>
          )}
          {previous && (
            <div className="mt-3.5 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-line bg-surface-faint px-3.5 py-3">
              <p className="min-w-0 flex-1 text-[12.5px] leading-relaxed text-muted">
                The base this one replaced — saved from <span className="font-mono text-[12px] text-tertiary">{previous.savedFrom}</span>,{' '}
                {age(previous.savedAt)} — is still here, so the last save can be undone. It is the one step back there is: the next save drops it.
              </p>
              <Button size="sm" variant="secondary" onClick={() => setReverting(true)}>
                <RotateCcw />
                Go back to it
              </Button>
            </div>
          )}
        </>
      ) : (
        <p className="text-[13px] leading-relaxed text-muted">
          None yet. New agents of {name} start from the plain base image{' '}
          {image.data?.snapshot && <Code>{image.data.snapshot}</Code>} and set the project up themselves — installing dependencies and filling caches,
          each one from cold. Once an agent has the project working, save its machine here and every agent after it starts from a copy of it.
        </p>
      )}

      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line-faint pt-3.5">
        <Button variant="primary" size="sm" onClick={() => setRefreshing(true)}>
          <RefreshCw />
          {base.data ? 'Refresh the base' : 'Set the project up in an agent'}
        </Button>
        <Button variant="secondary" size="sm" disabled={mine.length === 0} onClick={() => setSaving(true)}>
          <Camera />
          Save from an agent…
        </Button>
        <span className="text-xs leading-relaxed text-subtle">
          {base.data ? 'Start from the last photograph, change what has drifted, take a new one.' : 'Nothing is merged: a save is a photograph of one machine.'}
        </span>
      </div>

      <RefreshDialog
        open={refreshing}
        onOpenChange={setRefreshing}
        project={name}
        base={base.data ?? null}
        onCreated={(ref) => {
          setRefreshing(false);
          onOpenAgent(ref);
        }}
      />
      <SaveDialog open={saving} onOpenChange={setSaving} project={name} base={base.data ?? null} agents={mine} onSaved={() => void refresh()} />
      <ConfirmDialog
        open={reverting}
        onOpenChange={setReverting}
        title="Go back to the base the last save replaced?"
        description={
          previous ? (
            <>
              {name} goes back to the base saved from <Code>{previous.savedFrom}</Code>, and the one saved from <Code>{base.data?.savedFrom}</Code> is
              deleted. Only agents created after this are affected — the ones you have keep the machines they were made from. Afterwards there is
              nothing left to go back to.
            </>
          ) : undefined
        }
        confirmLabel="Go back to it"
        onConfirm={async () => {
          const back = await api.revertBase(name);
          await refresh();
          toast(`${name} is back on the base saved from ${back.savedFrom}`, { description: 'New agents are copied from it. There is nothing left to revert to.' });
        }}
      />
      <ConfirmDialog
        open={dropping}
        onOpenChange={setDropping}
        title="Drop the base the last save replaced?"
        description={
          <>
            It gives back the disk it holds. The base your new agents are copied from doesn't change — you only lose the one step back, so the last save
            can no longer be undone.
          </>
        }
        confirmLabel="Drop it"
        destructive
        onConfirm={async () => {
          await api.removePreviousBase(name);
          await refresh();
          toast(`Dropped the base ${name}'s last save replaced`);
        }}
      />
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={`Go back to the plain base image for ${name}?`}
        description={
          <>
            New agents of {name} start from the plain base image again, and set the project up themselves. The base and anything a save kept beside it
            are deleted, and can't be brought back.
          </>
        }
        confirmLabel="Go back to the plain image"
        destructive
        onConfirm={async () => {
          await api.removeBase(name);
          await refresh();
          toast(`${name} has no base: new agents start from the plain image`);
        }}
      >
        <p className="text-[13px] leading-relaxed text-muted">
          Nothing else is touched. The agents you have keep running on the machines they were made from, and the project's memory, its events, its
          branches and its worktrees are left exactly as they are.
        </p>
      </ConfirmDialog>
    </Card>
  );
}

// RefreshDialog makes the agent that will carry the next photograph. It is the
// one place the "start from the last base" rule is enforced: the request never
// carries clean, and the dialog says why, because a user who reached for New
// agent and ticked "skip the project base" would get a machine that looks fine
// and a base that has quietly lost everything.
function RefreshDialog({
  open,
  onOpenChange,
  project,
  base,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  project: string;
  base: T.Base | null;
  onCreated: (ref: string) => void;
}) {
  const queryClient = useQueryClient();
  const [task, setTask] = useState(refreshTask);
  const [title, setTitle] = useState('');
  const [job, setJob] = useState<T.Job | null>(null);
  const [done, setDone] = useState<T.Job | null>(null);
  const create = useMutation({
    mutationFn: () =>
      api.createAgent({
        project,
        title: title.trim() || (base ? 'Refresh the project base' : 'Set the project up'),
        ai: 'claude',
        interface: 'chat',
        autonomous: true,
        task: task.trim(),
        // Never clean. An agent made from the plain image would look like a
        // working machine and make a base that has lost everything the old one
        // had, because a save is a photograph and photographs don't merge.
      }),
    onSuccess: setJob,
  });

  const reset = (next: boolean) => {
    if (!next) {
      setJob(null);
      setDone(null);
      setTask(refreshTask);
      setTitle('');
      create.reset();
    }
    onOpenChange(next);
  };

  const onDone = useCallback(
    async (finished: T.Job) => {
      setDone(finished);
      if (finished.status !== 'succeeded') return;
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    [queryClient],
  );
  // A job carries the agent it made; a failed one carries the reason, which
  // JobProgress is already showing.
  const made = done?.status === 'succeeded' ? (done.result as T.Agent) : null;

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{base ? 'Refresh the project base' : `Set ${project} up in an agent`}</DialogTitle>
          <DialogDescription>
            {base
              ? 'An agent started from the base as it is now, to bring it up to date. You save its machine when you are happy with it.'
              : 'An agent to install what this project needs. You save its machine as the base when it works.'}
          </DialogDescription>
        </DialogHeader>

        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={(done) => void onDone(done)} />
            {made && (
              <Notice tone="info">
                <Code>{made.ref}</Code> is on it. When its machine is the way you want the base to be, come back here and save from it — the base isn't
                touched until you do.
              </Notice>
            )}
            <DialogFooter>
              <Button variant={made ? 'ghost' : 'secondary'} onClick={() => reset(false)}>
                {done ? 'Close' : 'Keep running in background'}
              </Button>
              {made && (
                <Button variant="primary" onClick={() => onCreated(made.ref)}>
                  Open {made.name}
                </Button>
              )}
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              create.mutate();
            }}
          >
            <Notice tone="info">
              {base ? (
                <>
                  It starts from the base saved from <Code>{base.savedFrom}</Code>, not from a clean machine — so it already has everything the base
                  has, and only changes what has drifted. Two machine images can't be merged, so an agent that started clean would, the moment you saved
                  from it, throw the rest away.
                </>
              ) : (
                <>
                  {project} has no base yet, so this agent starts from the plain base image, like every other agent of the project does today.
                </>
              )}
            </Notice>
            <Field label="What it works on" htmlFor="base-refresh-title" hint="A title for the sidebar.">
              <Input
                id="base-refresh-title"
                autoFocus
                maxLength={80}
                placeholder={base ? 'Refresh the project base' : 'Set the project up'}
                value={title}
                onChange={(event) => setTitle(event.target.value)}
              />
            </Field>
            <Field label="What it is told to do" htmlFor="base-refresh-task" hint="Sent as its first message. Edit it for what this project needs.">
              <Textarea
                id="base-refresh-task"
                className="min-h-56 font-mono text-[12px]"
                spellCheck={false}
                value={task}
                onChange={(event) => setTask(event.target.value)}
              />
            </Field>
            {create.error && <Notice>{errorMessage(create.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={() => reset(false)}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={create.isPending || !task.trim()}>
                {create.isPending && <LoaderCircle className="animate-spin" />}
                Create the agent
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// SaveDialog takes the photograph. It is the irreversible one, so it says so
// before it runs rather than after: the base it replaces is kept for exactly
// one step back, and the agent the old base came from is the older safety net
// that still holds when that step is used up.
function SaveDialog({
  open,
  onOpenChange,
  project,
  base,
  agents,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  project: string;
  base: T.Base | null;
  agents: T.Agent[];
  onSaved: () => void;
}) {
  // The newest agent is the one a refresh just made, which is what this dialog
  // is usually opened for.
  const newest: T.Agent | undefined = [...agents].sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
  const [agent, setAgent] = useState('');
  const [job, setJob] = useState<T.Job | null>(null);
  const [done, setDone] = useState<T.Job | null>(null);
  const chosen = agents.find((a) => a.name === agent) ?? newest;
  // The machine the old base was saved from, when it is still here: its disk
  // still holds the old state, so it can be saved from again long after the
  // kept base has been spent.
  const source = base && agents.find((a) => a.ref === base.savedFrom);
  const save = useMutation({
    mutationFn: () => (chosen ? api.saveBase(project, chosen.name) : Promise.reject(new Error(`${project} has no agent to save from`))),
    onSuccess: setJob,
  });

  const reset = (next: boolean) => {
    if (!next) {
      setJob(null);
      setDone(null);
      setAgent('');
      save.reset();
    }
    onOpenChange(next);
  };

  const finished = useCallback(
    (job: T.Job) => {
      setDone(job);
      if (job.status === 'succeeded') onSaved();
    },
    [onSaved],
  );

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Save {chosen ? chosen.ref : 'an agent'} as the base</DialogTitle>
          <DialogDescription>
            A photograph of that machine as it is now. The agent keeps running, and its worktree, logins and AI sessions are left out of the base.
          </DialogDescription>
        </DialogHeader>

        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={finished} />
            {done?.status === 'succeeded' && <Notice tone="info">New agents of {project} are now copied from this machine.</Notice>}
            <DialogFooter>
              <Button onClick={() => reset(false)}>{done ? 'Close' : 'Keep running in background'}</Button>
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate();
            }}
          >
            <Field
              label="Save from"
              htmlFor="base-save-agent"
              hint={chosen ? "Its machine becomes the base; its worktree doesn't." : `${project} has no agent yet: make one, set the project up in it, then save from it.`}
            >
              <Select id="base-save-agent" value={chosen?.name ?? ''} onChange={setAgent}>
                {agents.map((a) => (
                  <SelectOption key={a.name} value={a.name}>
                    {a.name}
                    {a.title ? ` — ${a.title}` : ''}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            {base ? (
              <Notice tone="warning">
                This replaces the base saved from <Code>{base.savedFrom}</Code>. Nothing is merged: whatever that base has and this machine doesn't, the
                new base won't have either. The base it replaces is kept, so this one save can be undone — until the save after it, which drops it.
              </Notice>
            ) : (
              <Notice tone="info">
                {project} has no base yet, so this replaces nothing. From now on its new agents are copied from this machine instead of the plain image.
              </Notice>
            )}
            {source && (
              <p className="flex items-start gap-2.5 text-[12.5px] leading-relaxed text-muted">
                <TriangleAlert className="mt-px size-4 shrink-0 text-amber-300" />
                <span>
                  Keep <span className="font-mono text-[12px] text-tertiary">{source.ref}</span> alive until the new base has proved itself. Its machine
                  still holds the old state, so you can save from it again — long after the kept base has been spent by another save.
                </span>
              </p>
            )}
            {save.error && <Notice>{errorMessage(save.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={() => reset(false)}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={!chosen || save.isPending}>
                {save.isPending && <LoaderCircle className="animate-spin" />}
                <Camera />
                Save as the base
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// Age says how old a base is in words as well as in a date, because the
// problem this whole panel exists for is a base nobody noticed going stale:
// "12 Sep 2025" is a fact and "a year old" is the fact you can act on.
function Age({ iso }: { iso: string }) {
  const when = new Date(iso);
  return (
    <span className="flex flex-wrap items-baseline gap-x-2">
      <span>{when.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })}</span>
      <span className={cn('text-[12.5px]', stale(iso) ? 'text-amber-300' : 'text-subtle')}>{age(iso)}</span>
    </span>
  );
}

const DAY = 86_400_000;

// A base older than this is worth a warning. Two months is roughly when a
// project's dependencies have moved enough that every new agent pays for it.
const STALE = 60 * DAY;

const stale = (iso: string, now = Date.now()): boolean => now - new Date(iso).getTime() >= STALE;

function age(iso: string, now = Date.now()): string {
  const days = Math.max(0, Math.floor((now - new Date(iso).getTime()) / DAY));
  if (days < 1) return 'today';
  if (days === 1) return 'yesterday';
  if (days < 31) return `${days} days old`;
  const months = Math.round(days / 30.44);
  if (days < 365) return `${months} months old`;
  const years = Math.floor(days / 365);
  return years === 1 ? 'over a year old' : `over ${years} years old`;
}
