import { SquareDashedMousePointer, X } from 'lucide-react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { pickImageNumber, pickLabel, type PendingElement, type PickedChip } from '../../lib/picks';
import { previewUrl } from '../../lib/chatImages';
import { Tip } from '../ui/tooltip';
import { ImageThumb } from './Images';

// where is a pick's page and whose browser it was in, as a chip's second line.
function where(url: string, from: string): string {
  let host = url;
  try {
    host = new URL(url).host || url;
  } catch {
    // not a URL: shown as it is
  }
  return `${from.split('/').pop()} · ${host}`;
}

// Chip is an element picked in a browser: its crop, which opens whole, what it
// is and where, and its number, which the message can name ("make 1 match 2").
function Chip({ n, label, selector, place, src, onRemove }: { n: number; label: string; selector: string; place: string; src?: string; onRemove?: () => void }) {
  const t = useT();
  return (
    <div className="group/pick relative flex h-14 min-w-0 max-w-72 shrink-0 items-center gap-2 rounded-xl border border-brand-400/40 bg-brand-500/10 p-1 pr-3" data-chat-pick={selector}>
      {src ? (
        <ImageThumb src={src} name={label} contain className="h-full w-16" />
      ) : (
        <span className="flex h-full w-16 shrink-0 items-center justify-center rounded-lg bg-surface-raised">
          <SquareDashedMousePointer className="size-4 text-brand-300" />
        </span>
      )}
      <Tip label={selector}>
        <span className="flex min-w-0 flex-col leading-tight">
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="flex size-4 shrink-0 items-center justify-center rounded bg-brand-500/30 text-[10px] font-semibold tabular-nums text-brand-100">{n}</span>
            <span className="truncate font-mono text-[12px] text-primary">{label}</span>
          </span>
          <span className="truncate text-[11px] text-subtle">{place}</span>
        </span>
      </Tip>
      {onRemove && (
        <button
          type="button"
          aria-label={t('chat.pick.remove', { name: label })}
          onClick={onRemove}
          className="absolute -right-1.5 -top-1.5 flex size-5 items-center justify-center rounded-full border border-line-strong bg-overlay text-subtle opacity-90 shadow transition hover:text-primary group-hover/pick:opacity-100"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}

// PickChip is a pick waiting in the composer.
export function PickChip({ pick, n, onRemove }: { pick: PendingElement; n: number; onRemove: () => void }) {
  const el = pick.element;
  return <Chip n={n} label={pickLabel(el)} selector={el.selector} place={where(el.url, pick.from)} src={el.image ? previewUrl(el.image) : undefined} onRemove={onRemove} />;
}

// SentPicks are the elements a message in the conversation was sent with,
// each with its crop from the message's pictures.
export function SentPicks({ chatRef, picks, images }: { chatRef: string; picks: PickedChip[]; images: T.ChatImage[] }) {
  const crop = (n: number) => images.find((image) => pickImageNumber(image.name) === n);
  return (
    <div className="flex max-w-[85%] flex-wrap justify-end gap-1.5">
      {picks.map((p) => {
        const image = crop(p.n);
        return <Chip key={p.n} n={p.n} label={p.label} selector={p.selector} place={where(p.url, p.from)} src={image ? api.chatImageUrl(chatRef, image.id) : undefined} />;
      })}
    </div>
  );
}
