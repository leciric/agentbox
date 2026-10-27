// InstallNote offers to keep the phone page on the home screen as an app
// (web/pwa.ts): with the browser's own install button where it has one, how to
// on an iPhone, and, on the local network's plain HTTP, where it can't be,
// that the https address can.
import { Download } from 'lucide-react';
import { Button } from '../components/ui/button';
import { useT } from '../lib/i18n';
import { installable, ios, useInstall, useStandalone } from './pwa';

export function InstallNote({ className = '' }: { className?: string }) {
  const t = useT();
  const install = useInstall();
  const standalone = useStandalone();
  if (standalone) return null;
  const note = (text: string) => <p className={`text-[12px] leading-relaxed text-subtle ${className}`} data-phone-install-note>{text}</p>;
  if (!installable()) return note(t('web.install.needsHttps'));
  if (install) {
    return (
      <div className={`grid justify-items-start gap-2 ${className}`}>
        <p className="text-[12px] leading-relaxed text-subtle">{t('web.install.offer')}</p>
        <Button size="sm" variant="secondary" data-phone-install onClick={() => void install()}>
          <Download className="size-3.5" /> {t('web.install.button')}
        </Button>
      </div>
    );
  }
  return ios() ? note(t('web.install.ios')) : null;
}
