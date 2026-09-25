import { CornerDownLeft } from 'lucide-react';
import { useState } from 'react';
import { MenuLabel, MenuSeparator } from './ui/menu';

// ModelByName names a model the menu above doesn't list.
//
// It exists because of a chicken and egg. A model outside the curated list an
// account is offered — Fable — is advertised by Claude Code only once a session
// is already running on it, so it can never be picked off a menu the first
// time. Claude Code has the same problem in its own picker and solves it the
// same way, and says so there: "For other/previous model names, specify with
// --model." This is that, remembered.
//
// It is deliberately not a list AgentBox keeps. A committed catalogue is how
// t3code ends up offering models an account can't run, and the menu above
// stays the adapter's own (D40, D45). What's typed here is the user naming a
// model, which is a different claim: AgentBox stores it, writes it into the
// tool's settings before the next session, and the adapter then advertises it
// back — so from the second session on it is an ordinary entry on that menu.
export function ModelByName({ onPick, disabled }: { onPick: (value: string) => void; disabled?: boolean }) {
  const [value, setValue] = useState('');
  const model = value.trim();
  // "default" is the menu's own word for "no model of my own", not a model id.
  // Written as one, every turn fails, so it's the one name that can't be given.
  const usable = model !== '' && model !== 'default';
  const submit = () => {
    if (!usable) return;
    setValue('');
    onPick(model);
  };
  return (
    <>
      <MenuSeparator />
      <MenuLabel>Another model</MenuLabel>
      <div className="px-1 pb-1">
        <div className="flex items-center gap-1.5 rounded-lg bg-surface px-2.5 py-1.5 focus-within:bg-surface-raised">
          <input
            data-model-by-name
            aria-label="Name another model"
            value={value}
            disabled={disabled}
            onChange={(e) => setValue(e.target.value)}
            // The menu treats typing as its own keyboard navigation, and Enter
            // as picking the highlighted row; this input needs both itself.
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === 'Enter') {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="claude-fable-5-1"
            className="w-full bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none disabled:opacity-50"
          />
          <button
            type="button"
            aria-label="Use this model"
            disabled={!usable || disabled}
            onClick={submit}
            className="shrink-0 rounded-md p-0.5 text-subtle transition hover:text-secondary disabled:opacity-30 disabled:hover:text-subtle"
          >
            <CornerDownLeft className="size-3.5" />
          </button>
        </div>
        <p className="px-1 pt-1.5 text-[11px] leading-relaxed text-subtle">
          A model id this account can run but Claude Code's menu doesn't offer. It takes effect when the chat next starts, and says so if the model
          doesn't exist.
        </p>
      </div>
    </>
  );
}
