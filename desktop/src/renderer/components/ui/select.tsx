import { Check, ChevronsUpDown, Search } from 'lucide-react';
import { Children, isValidElement, useMemo, useState, type ReactNode } from 'react';
import { cn } from '../../lib/utils';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './menu';

// SelectOption stands in for a native <option>, so a Select's children read
// exactly like a native select's did before this replaced it. It never
// renders itself — Select reads its props out of the element tree.
export function SelectOption({ disabled: _disabled, children }: { value: string; disabled?: boolean; children?: ReactNode }) {
  return children as ReactNode;
}

interface Option {
  value: string;
  label: ReactNode;
  text: string;
  disabled?: boolean;
}

function textOf(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join(' ');
  if (isValidElement(node)) return textOf((node.props as { children?: ReactNode }).children);
  return '';
}

function collectOptions(children: ReactNode): Option[] {
  const options: Option[] = [];
  Children.forEach(children, (child) => {
    if (!isValidElement(child)) return;
    const props = child.props as { value: string; disabled?: boolean; children?: ReactNode };
    options.push({ value: props.value, label: props.children, text: textOf(props.children), disabled: props.disabled });
  });
  return options;
}

// A list worth searching: below this, a search box is noise (see OptionMenu
// and NewAgentDefaults, which use the same threshold for the model menu).
const searchThreshold = 8;

// Select replaces the native <select> with a Menu-based combobox that matches
// the app's own visual language, while keeping the native element's shape:
// <SelectOption value="x">Label</SelectOption> children, a value and an
// onChange. Built on the same Menu primitives as the composer's model picker.
export function Select({
  value,
  onChange,
  disabled,
  className,
  id,
  placeholder = 'Select…',
  children,
  'aria-label': ariaLabel,
  ...rest
}: {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  className?: string;
  id?: string;
  placeholder?: string;
  children: ReactNode;
  'aria-label'?: string;
  [dataAttr: `data-${string}`]: boolean | string | undefined;
}) {
  const [query, setQuery] = useState('');
  const options = useMemo(() => collectOptions(children), [children]);
  const current = options.find((o) => o.value === value);
  const searchable = options.length > searchThreshold;
  const q = query.trim().toLowerCase();
  const filtered = q ? options.filter((o) => o.text.toLowerCase().includes(q)) : options;

  return (
    <Menu onOpenChange={(open) => !open && setQuery('')}>
      <MenuTrigger asChild>
        <button
          type="button"
          id={id}
          disabled={disabled}
          aria-label={ariaLabel}
          {...rest}
          className={cn(
            'flex h-9 w-full items-center gap-2 rounded-lg border border-line-strong bg-sunken px-3 text-left text-sm text-primary shadow-[inset_0_1px_2px_var(--ab-shadow-soft)] transition-colors hover:border-line-vivid focus-visible:border-brand-400/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/25 disabled:opacity-50 data-[state=open]:border-brand-400/60',
            className,
          )}
        >
          <span className="min-w-0 flex-1 truncate">{current ? current.label : placeholder}</span>
          <ChevronsUpDown className="size-3.5 shrink-0 text-subtle" />
        </button>
      </MenuTrigger>
      <MenuContent align="start" className="max-h-80 w-[--radix-popper-anchor-width] min-w-[--radix-popper-anchor-width] overflow-y-auto">
        {searchable && (
          <div className="mb-1 flex items-center gap-2 rounded-lg bg-surface px-2.5 py-1.5">
            <Search className="size-3.5 shrink-0 text-subtle" />
            <input
              autoFocus
              aria-label="Search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.stopPropagation()}
              placeholder="Search"
              className="w-full bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none"
            />
          </div>
        )}
        {filtered.map((option) => (
          <MenuItem
            key={option.value}
            disabled={option.disabled}
            onSelect={() => onChange(option.value)}
            hint={option.value === value ? <Check className="size-3.5 text-brand-300" /> : undefined}
          >
            {option.label}
          </MenuItem>
        ))}
        {filtered.length === 0 && <div className="px-2.5 py-3 text-center text-[12px] text-subtle">Nothing matches "{query.trim()}".</div>}
      </MenuContent>
    </Menu>
  );
}
