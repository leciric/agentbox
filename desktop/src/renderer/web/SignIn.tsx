// Connecting a browser to the hub, and picking the environment to open. The
// browser connects on the hub's own page, /hub/connect, which a device already
// connected approves; accounts made before pairing can still use a password.
import { ArrowRight, LoaderCircle, LogIn, Server } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { HubEnvironment } from '../../preload';
import type { HubInfo } from '../../shared/api';
import { Logo } from '../components/Sidebar';
import { Button } from '../components/ui/button';
import { Notice } from '../components/ui/card';
import { Field, Input } from '../components/ui/input';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';

export function SignIn({ signedIn: initiallySignedIn, onReady }: { signedIn: boolean; onReady: () => void }) {
  const t = useT();
  const [signedIn, setSignedIn] = useState(initiallySignedIn);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [environments, setEnvironments] = useState<HubEnvironment[]>();
  const [passwords, setPasswords] = useState(false);

  useEffect(() => {
    if (signedIn) return;
    fetch('/v1/hub')
      .then((res) => (res.ok ? (res.json() as Promise<HubInfo>) : null))
      .then((info) => setPasswords(info?.passwords ?? false))
      .catch(() => {});
  }, [signedIn]);

  useEffect(() => {
    if (!signedIn) return;
    const load = () =>
      window.agentbox.hubs
        .environments(location.origin)
        .then(setEnvironments)
        .catch((err) => setError(errorMessage(err)));
    void load();
    const timer = setInterval(load, 5_000);
    return () => clearInterval(timer);
  }, [signedIn]);

  const signIn = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await window.agentbox.hubs.login(location.origin, email.trim(), password);
      setPassword('');
      setSignedIn(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const open = async (env: HubEnvironment) => {
    await window.agentbox.target.set({ kind: 'hub', hub: location.origin, environmentId: env.id, environmentName: env.name });
    onReady();
  };

  return (
    <div className="flex min-h-full items-center justify-center px-5 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex items-center gap-3">
          <Logo className="size-10" />
          <div>
            <div className="text-lg font-semibold tracking-tight text-title">AgentBox</div>
            <div className="text-[13px] text-subtle">{location.host}</div>
          </div>
        </div>

        {!signedIn ? (
          <div className="panel grid gap-4 rounded-2xl p-5">
            <div>
              <h1 className="text-xl font-semibold tracking-tight text-title">{t('web.signIn.title')}</h1>
              <p className="mt-1 text-[13px] text-muted">{t('web.signIn.subtitle')}</p>
            </div>
            <Button variant="primary" onClick={() => location.assign(`/hub/connect?next=${encodeURIComponent(location.pathname + location.search)}`)}>
              <LogIn />
              {t('web.signIn.connect')}
            </Button>
            {passwords && (
              <form
                className="grid gap-4 border-t border-line pt-4"
                onSubmit={(event) => {
                  event.preventDefault();
                  void signIn();
                }}
              >
                <p className="text-[13px] text-muted">{t('web.signIn.passwordAccounts')}</p>
                <Field label={t('web.signIn.email')} htmlFor="web-email">
                  <Input id="web-email" type="email" autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} />
                </Field>
                <Field label={t('web.signIn.password')} htmlFor="web-password">
                  <Input id="web-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
                </Field>
                {error && <Notice>{error}</Notice>}
                <Button type="submit" disabled={!email.trim() || !password || busy}>
                  {busy && <LoaderCircle className="animate-spin" />}
                  {t('web.signIn.withPassword')}
                </Button>
              </form>
            )}
          </div>
        ) : (
          <div className="grid gap-3">
            <h1 className="text-xl font-semibold tracking-tight text-title">{t('web.signIn.environments')}</h1>
            {error && <Notice>{error}</Notice>}
            {!environments && !error && <div className="text-[13px] text-subtle">{t('common.loading')}</div>}
            {environments?.length === 0 && (
              <Notice tone="info">{t.rich('web.signIn.noEnvironments', { code: (c) => <code className="font-mono">{c}</code> })}</Notice>
            )}
            {environments?.map((env) => (
              <button
                key={env.id}
                data-environment-card={env.name}
                onClick={() => void open(env)}
                className="panel group flex items-center gap-3 rounded-2xl p-4 text-left transition hover:border-line-strong"
              >
                <span className="flex size-9 items-center justify-center rounded-xl bg-surface text-tertiary ring-1 ring-inset ring-line">
                  <Server className="size-4" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block font-medium text-primary">{env.name}</span>
                  <span className="flex items-center gap-1.5 text-[12px] text-subtle">
                    <span className={cn('size-1.5 rounded-full', env.online ? 'bg-emerald-400' : 'bg-faint')} />
                    {env.online ? (env.hostname ? t('web.signIn.onlineHost', { hostname: env.hostname }) : t('web.signIn.online')) : t('web.signIn.offline')}
                  </span>
                </span>
                <ArrowRight className="size-4 text-faint transition group-hover:translate-x-0.5 group-hover:text-tertiary" />
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
