import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bug, LoaderCircle, Send } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { api } from '../lib/api';
import { formatTime, useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Textarea } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { Switch } from './ui/switch';

// SnapComposer opens on a SnapShot taken with `agentbox snap` (a desktop key
// bind) or the app's own shortcut: the picture, the window it shows, a note,
// and where it goes, a project's chat or one of its agents. The daemon holds
// the capture until it is sent or discarded (internal/daemon/snaps.go); this
// shows the newest, and the ones before it after.

const leadTarget = '';

export function SnapComposer({ project: current, onSent }: { project?: string; onSent: (project: string, agent: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const snaps = useQuery({ queryKey: ['snaps'], queryFn: api.snaps });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled: (snaps.data?.length ?? 0) > 0 });
  const snap = snaps.data?.at(-1);

  const [project, setProject] = useState('');
  const [agent, setAgent] = useState(leadTarget);
  const [note, setNote] = useState('');
  const [tree, setTree] = useState(true);
  const [showTree, setShowTree] = useState(false);

  // Each new capture starts over, on the project it was taken for, the one
  // open in the app, or the first.
  useEffect(() => {
    if (!snap) return;
    setProject(snap.project || current || projects.data?.[0]?.name || '');
    setAgent(leadTarget);
    setNote('');
    setTree(Boolean(snap.accessibility));
    setShowTree(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only a new capture resets the form
  }, [snap?.id]);
  useEffect(() => {
    if (snap && !project && projects.data?.[0]) setProject(current || projects.data[0].name);
  }, [snap, project, current, projects.data]);

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['snaps'] });
  const send = useMutation({
    mutationFn: () => api.sendSnap(snap!.id, { project, agent: agent || undefined, note: note.trim() || undefined, accessibility: tree }),
    onSuccess: async () => {
      toast(agent ? t('chat.snap.sentToAgent', { agent }) : t('chat.snap.sentToChat', { project: projectLabel(project, projects.data) }));
      onSent(project, agent);
      await refresh();
    },
  });
  const discard = async () => {
    if (!snap) return;
    await api.dropSnap(snap.id).catch(() => {});
    await refresh();
  };

  const projectAgents = (agents.data ?? []).filter((a) => a.project === project && a.name !== 'lead');

  return (
    <Dialog open={Boolean(snap)} onOpenChange={(open) => !open && void discard()}>
      <DialogContent className="max-w-2xl" data-snap-composer>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Bug className="size-4 text-brand-400" />
            {t('chat.snap.title')}
          </DialogTitle>
          <DialogDescription>
            {t('chat.snap.description', {
              kind: snap?.window ? 'window' : 'screen',
              time: snap ? formatTime(snap.takenAt, { timeStyle: 'medium' }) : '',
              hasDesktop: snap?.desktop ? 'yes' : 'no',
              desktop: snap?.desktop ?? '',
              more: Math.max(0, (snaps.data?.length ?? 0) - 1),
            })}
          </DialogDescription>
        </DialogHeader>

        {snap && (
          <div className="grid gap-4">
            <figure className="grid min-w-0 gap-2">
              <img
                src={window.agentbox.chatImageUrl(`/v1/snaps/${snap.id}/image`)}
                alt={snap.title || t('chat.snap.alt')}
                className="max-h-72 w-full rounded-xl border border-line-strong bg-sunken object-contain"
              />
              <figcaption className="min-w-0 truncate text-[12.5px] text-subtle">
                {snap.app && <Code>{snap.app}</Code>} {snap.title || (snap.window ? t('chat.snap.untitledWindow') : t('chat.snap.wholeScreen'))}
              </figcaption>
            </figure>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('chat.snap.project')} htmlFor="snap-project">
                <Select id="snap-project" value={project} onChange={(value) => {
                  setProject(value);
                  setAgent(leadTarget);
                }}>
                  {(projects.data ?? []).map((p) => (
                    <SelectOption key={p.name} value={p.name}>
                      {projectLabel(p.name, projects.data)}
                    </SelectOption>
                  ))}
                </Select>
              </Field>
              <Field label={t('chat.snap.sendTo')} htmlFor="snap-agent">
                <Select id="snap-agent" value={agent} onChange={setAgent}>
                  <SelectOption value={leadTarget}>{t('chat.snap.projectChat')}</SelectOption>
                  {projectAgents.map((a) => (
                    <SelectOption key={a.ref} value={a.name}>
                      {a.name}
                      {a.title ? ` · ${a.title}` : ''}
                    </SelectOption>
                  ))}
                </Select>
              </Field>
            </div>

            <Field label={t('chat.snap.whatsWrong')} htmlFor="snap-note">
              <Textarea
                id="snap-note"
                rows={3}
                autoFocus
                placeholder={t('chat.snap.notePlaceholder')}
                value={note}
                onChange={(event) => setNote(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey) && project) send.mutate();
                }}
              />
            </Field>

            {snap.accessibility ? (
              <div className="grid gap-2">
                <label className="flex items-center gap-3 text-[13px] text-secondary">
                  <Switch checked={tree} onCheckedChange={setTree} aria-label={t('chat.snap.includeTree')} />
                  {t('chat.snap.includeTreeLines', { count: snap.accessibility.split('\n').length })}
                  <button type="button" className="ml-auto text-[12px] text-brand-400 hover:underline" onClick={() => setShowTree((v) => !v)}>
                    {showTree ? t('chat.snap.hide') : t('chat.snap.show')}
                  </button>
                </label>
                {showTree && (
                  <pre className="max-h-40 min-w-0 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-line-faint bg-sunken p-2.5 font-mono text-[11.5px] text-subtle">
                    {snap.accessibility}
                  </pre>
                )}
              </div>
            ) : (
              <p className="text-[12px] text-subtle">{t('chat.snap.noTree')}</p>
            )}

            {send.error && <Notice>{errorMessage(send.error)}</Notice>}
          </div>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={() => void discard()}>
            {t('chat.snap.discard')}
          </Button>
          <Button variant="primary" disabled={!project || send.isPending} onClick={() => send.mutate()}>
            {send.isPending ? <LoaderCircle className="animate-spin" /> : <Send />}
            {t('chat.snap.send')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
