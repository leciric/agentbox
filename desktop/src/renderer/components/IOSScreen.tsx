import { LoaderCircle } from 'lucide-react';
import type * as T from '../../shared/api';
import { useT } from '../lib/i18n';

// IOSScreen is a booted simulator's screen, in the iOS tab.
export function IOSScreen({ agent, device }: { agent: T.Agent; device: T.IOSDevice }) {
  const t = useT();
  return (
    <div className="absolute inset-0 flex items-center justify-center text-sm text-subtle" data-ios-screen={`${agent.ref}:${device.udid}`}>
      <LoaderCircle className="mr-2 size-4 animate-spin" />
      {t('agent.ios.connecting')}
    </div>
  );
}
