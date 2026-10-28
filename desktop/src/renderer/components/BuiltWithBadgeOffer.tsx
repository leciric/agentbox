// The one-time offer of "Built with AgentBox" on agents' pull requests, shown
// once an agent's pull request has been merged (Settings.builtWithBadgeOffer).
// Either answer closes it for good; the setting stays in Settings.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api } from '../lib/api';
import { errorMessage } from '../lib/utils';
import { useT } from '../lib/i18n';
import { Button } from './ui/button';

export function BuiltWithBadgeOffer() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const answer = useMutation({
    mutationFn: (on: boolean) => api.updateSettings(on ? { builtWithBadge: true } : { dismissBuiltWithBadgeOffer: true }),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  if (!settings.data?.builtWithBadgeOffer) return null;
  return (
    <div role="status" data-built-with-badge-offer className="mx-4 mt-4 grid gap-3 rounded-xl border border-line bg-surface-raised px-4 py-3 md:mx-6">
      <div>
        <div className="text-[13px] font-medium text-primary">{t('builtWithBadge.offer.title')}</div>
        <p className="mt-1 text-[13px] leading-relaxed text-secondary">{t('builtWithBadge.offer.body')}</p>
      </div>
      <div className="flex gap-2">
        <Button size="sm" disabled={answer.isPending} onClick={() => answer.mutate(true)}>
          {t('builtWithBadge.offer.accept')}
        </Button>
        <Button size="sm" variant="ghost" disabled={answer.isPending} onClick={() => answer.mutate(false)}>
          {t('builtWithBadge.offer.decline')}
        </Button>
      </div>
    </div>
  );
}
