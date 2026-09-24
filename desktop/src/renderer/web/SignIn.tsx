// Signing in to the hub in a browser, and picking the environment to open.
import { ArrowRight, LoaderCircle, Server } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { HubEnvironment } from '../../preload';
import { Logo } from '../components/Sidebar';
import { Button } from '../components/ui/button';
import { Notice } from '../components/ui/card';
import { Field, Input } from '../components/ui/input';
import { cn, errorMessage } from '../lib/utils';

export function SignIn({ signedIn: initiallySignedIn, onReady }: { signedIn: boolean; onReady: () => void }) {
  const [signedIn, setSignedIn] = useState(initiallySignedIn);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [environments, setEnvironments] = useState<HubEnvironment[]>();

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
          <form
            className="panel grid gap-4 rounded-2xl p-5"
            onSubmit={(event) => {
              event.preventDefault();
              void signIn();
            }}
          >
            <div>
              <h1 className="text-xl font-semibold tracking-tight text-title">Sign in</h1>
              <p className="mt-1 text-[13px] text-muted">Your agents, on every machine connected to this hub.</p>
            </div>
            <Field label="Email" htmlFor="web-email">
              <Input id="web-email" type="email" autoComplete="username" autoFocus value={email} onChange={(event) => setEmail(event.target.value)} />
            </Field>
            <Field label="Password" htmlFor="web-password">
              <Input id="web-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
            </Field>
            {error && <Notice>{error}</Notice>}
            <Button type="submit" variant="primary" disabled={!email.trim() || !password || busy}>
              {busy && <LoaderCircle className="animate-spin" />}
              Sign in
            </Button>
          </form>
        ) : (
          <div className="grid gap-3">
            <h1 className="text-xl font-semibold tracking-tight text-title">Environments</h1>
            {error && <Notice>{error}</Notice>}
            {!environments && !error && <div className="text-[13px] text-subtle">Loading…</div>}
            {environments?.length === 0 && (
              <Notice tone="info">No environments yet. Add one from the desktop app or with agentbox env add, then connect its machine.</Notice>
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
                    {env.online ? `online${env.hostname ? ` · ${env.hostname}` : ''}` : 'offline'}
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
