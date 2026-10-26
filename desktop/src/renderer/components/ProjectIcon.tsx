// A project's icon: the one the user chose (an image or an emoji), else the
// one the daemon found in its repository (a favicon, a logo, a manifest's
// icon), else its first letter. The daemon
// keeps both (GET /v1/projects/{project}/icon); Project.iconVersion changes
// when either does, so the cache here is keyed by it and never refetched
// otherwise. v2 draws it on the lead rows, the project's header and Overview,
// and anything else that names a project can: <ProjectIcon project={p} />.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ImageUp, RotateCcw } from 'lucide-react';
import { useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './ui/dialog';

type IconProject = Pick<T.Project, 'name' | 'displayName' | 'iconVersion'>;

export const projectIconKey = (p: Pick<T.Project, 'name' | 'iconVersion'>) => ['projectIcon', p.name, p.iconVersion ?? 0] as const;

export function useProjectIcon(p: Pick<T.Project, 'name' | 'iconVersion'>) {
  return useQuery({ queryKey: projectIconKey(p), queryFn: () => api.projectIcon(p.name), staleTime: Infinity, retry: false });
}

// ProjectIcon is the icon in a rounded square of className's size (size-6 by
// default); an emoji and the letter scale with its text size.
export function ProjectIcon({ project, className }: { project: IconProject; className?: string }) {
  const icon = useProjectIcon(project).data;
  const box = cn('flex size-6 shrink-0 items-center justify-center overflow-hidden rounded-md bg-surface-strong text-[11px] font-semibold uppercase text-secondary', className);
  if (icon?.image) {
    return (
      <span className={box} data-project-icon="image" aria-hidden>
        <img src={icon.image} alt="" className="size-full object-contain" draggable={false} />
      </span>
    );
  }
  if (icon?.emoji) {
    return (
      <span className={cn(box, 'text-[1.15em] font-normal normal-case')} data-project-icon="emoji" aria-hidden>
        {icon.emoji}
      </span>
    );
  }
  return (
    <span className={box} data-project-icon="letter" aria-hidden>
      {projectLabel(project as T.Project).slice(0, 1)}
    </span>
  );
}

// A few to pick from without a keyboard's emoji panel.
const suggested = ['🚀', '🧪', '📦', '🛠️', '🎨', '📱', '🌐', '🤖', '📚', '🔒', '⚡', '🐳', '🦀', '🐍', '🧩', '🗂️'];

const maxUpload = 512 * 1024;

// ProjectIconDialog is "Change icon": upload an image, pick or type an emoji,
// or go back to the icon found in the repository.
export function ProjectIconDialog({ project, open, onOpenChange }: { project: T.Project; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const icon = useProjectIcon(project).data;
  const [emoji, setEmoji] = useState('');
  const file = useRef<HTMLInputElement>(null);
  // The new iconVersion keys the new icon; the old key is dropped too.
  const done = async () => {
    await queryClient.invalidateQueries({ queryKey: ['projects'] });
    await queryClient.invalidateQueries({ queryKey: ['projectIcon', project.name] });
    onOpenChange(false);
  };
  const save = useMutation({
    mutationFn: (req: T.SetProjectIconRequest) => api.setProjectIcon(project.name, req),
    onSuccess: done,
    onError: (err) => toast.error(t('projectIcon.failed'), { description: errorMessage(err) }),
  });
  const reset = useMutation({
    mutationFn: () => api.resetProjectIcon(project.name),
    onSuccess: done,
    onError: (err) => toast.error(t('projectIcon.failed'), { description: errorMessage(err) }),
  });
  const upload = (f: File | undefined) => {
    if (!f) return;
    if (f.size > maxUpload) {
      toast.error(t('projectIcon.tooBig'));
      return;
    }
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === 'string' && save.mutate({ image: reader.result });
    reader.readAsDataURL(f);
  };
  const busy = save.isPending || reset.isPending;
  const name = projectLabel(project);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md" data-project-icon-dialog>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2.5">
            <ProjectIcon project={project} className="size-8 text-[14px]" />
            {t('projectIcon.title', { project: name })}
          </DialogTitle>
          <DialogDescription>{t('projectIcon.description')}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <div className="text-[12px] font-medium text-secondary">{t('projectIcon.emoji')}</div>
          <div className="flex flex-wrap gap-1">
            {suggested.map((e) => (
              <button
                key={e}
                type="button"
                disabled={busy}
                onClick={() => save.mutate({ emoji: e })}
                className={cn('flex size-8 items-center justify-center rounded-lg text-[17px] hover:bg-surface-raised', icon?.source === 'chosen' && icon.emoji === e && 'bg-surface-strong ring-1 ring-line-heavy')}
                data-icon-emoji={e}
              >
                {e}
              </button>
            ))}
          </div>
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (emoji.trim()) save.mutate({ emoji: emoji.trim() });
            }}
          >
            <input
              value={emoji}
              onChange={(e) => setEmoji(e.target.value)}
              placeholder={t('projectIcon.emojiPlaceholder')}
              aria-label={t('projectIcon.emoji')}
              className="h-8 min-w-0 flex-1 rounded-lg border border-line-strong bg-sunken px-2.5 text-[13px] text-primary outline-none placeholder:text-faint focus:border-brand-400/60"
            />
            <Button size="sm" type="submit" disabled={busy || !emoji.trim()}>
              {t('projectIcon.useEmoji')}
            </Button>
          </form>
        </div>
        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <input ref={file} type="file" accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml,image/x-icon,.ico" className="hidden" onChange={(e) => upload(e.target.files?.[0])} />
          <Button size="sm" disabled={busy} onClick={() => file.current?.click()} data-icon-upload>
            <ImageUp />
            {t('projectIcon.upload')}
          </Button>
          {icon?.source === 'chosen' && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => reset.mutate()} data-icon-reset>
              <RotateCcw />
              {icon.hasDetected ? t('projectIcon.reset') : t('projectIcon.remove')}
            </Button>
          )}
          {icon?.source === 'detected' && icon.detectedFrom && <span className="text-[11.5px] text-subtle">{t('projectIcon.detectedFrom', { from: icon.detectedFrom })}</span>}
        </div>
      </DialogContent>
    </Dialog>
  );
}
