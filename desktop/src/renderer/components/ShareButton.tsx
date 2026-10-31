import { LoaderCircle, Share2 } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';

// ShareButton saves what an agent did as one page anyone can open, with no
// network and no AgentBox: its task, timeline and screenshots, the key
// messages of its chat, its changes and its pull request, redacted by the
// daemon (internal/share). The user picks where it goes.
export function ShareButton({ agentRef, className, compact }: { agentRef: string; className?: string; compact?: boolean }) {
  const t = useT();
  const [saving, setSaving] = useState(false);
  const save = async () => {
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
  return (
    <Button
      size={compact ? 'icon-sm' : 'sm'}
      variant="ghost"
      className={cn(compact ? 'size-6' : 'h-7', className)}
      disabled={saving}
      onClick={() => void save()}
      title={t('share.title')}
      aria-label={compact ? t('share.button') : undefined}
      data-share-run
    >
      {saving ? <LoaderCircle className="animate-spin" /> : <Share2 />}
      {!compact && t('share.button')}
    </Button>
  );
}
