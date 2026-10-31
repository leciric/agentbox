import { LoaderCircle, Share2 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { dotLabel, FeatureDot, useFeatureDot } from './FeatureDot';
import { Button } from './ui/button';
import { Tip } from './ui/tooltip';

// ShareButton saves what an agent did as one page anyone can open, with no
// network and no AgentBox: its task, timeline and screenshots, the key
// messages of its chat, its changes and its pull request, redacted by the
// daemon (internal/share). The user picks where it goes.
export function ShareButton({ agentRef, className, compact }: { agentRef: string; className?: string; compact?: boolean }) {
  const t = useT();
  const [saving, setSaving] = useState(false);
  const dot = useFeatureDot('share');
  const save = async () => {
    dot.clear();
    setSaving(true);
    try {
      const file = await api.saveShare(agentRef);
      if (file) {
        toast.success(t('share.saved', { name: file.split(/[\\/]/).pop() ?? file }), {
          description: t('share.savedDetail'),
          action: { label: t('share.show'), onClick: () => void window.agentbox.showShare(file) },
        });
      }
    } catch (err) {
      toast.error(t('share.failed'), { description: errorMessage(err) });
    } finally {
      setSaving(false);
    }
  };
  const button = (
    <Button
      size={compact ? 'icon-sm' : 'sm'}
      variant="ghost"
      className={cn('relative', compact ? 'size-6' : 'h-7', className)}
      disabled={saving}
      onClick={() => void save()}
      title={dot.on ? undefined : t('share.title')}
      aria-label={compact ? t('share.button') : undefined}
      data-share-run
    >
      {saving ? <LoaderCircle className="animate-spin" /> : <Share2 />}
      {!compact && t('share.button')}
      <FeatureDot dot={dot} />
    </Button>
  );
  // While it's new, its title is a tooltip, with the hint under it.
  return dot.on ? <Tip label={dotLabel(t('share.title'), dot)}>{button}</Tip> : button;
}
