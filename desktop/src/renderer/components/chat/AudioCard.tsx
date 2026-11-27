import { Pause, Play, SquareArrowOutUpRight } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useT } from '../../lib/i18n';
import { clock } from '../../lib/media';
import { cn } from '../../lib/utils';

// The card that is playing, so starting another pauses it.
let playing: HTMLAudioElement | null = null;

// AudioCard plays an audio file in place: name, play/pause, a seek bar with
// the time. Nothing plays until it is pressed, and starting one pauses the
// other. onOpen keeps the file's open-in-app action.
export function AudioCard({ name, src, size, onOpen, openLabel }: { name: string; src: string; size?: string; onOpen?: () => void; openLabel?: string }) {
  const t = useT();
  const ref = useRef<HTMLAudioElement>(null);
  const [on, setOn] = useState(false);
  const [time, setTime] = useState(0);
  const [length, setLength] = useState(0);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const el = ref.current;
    return () => {
      el?.pause();
      if (playing === el) playing = null;
    };
  }, []);

  const toggle = () => {
    const el = ref.current;
    if (!el) return;
    if (!el.paused) {
      el.pause();
      return;
    }
    if (playing && playing !== el) playing.pause();
    playing = el;
    el.play().catch((err: unknown) => {
      // A play() cut short by a pause or a new source isn't a broken file.
      if (!(err instanceof DOMException && err.name === 'AbortError')) setFailed(true);
    });
  };
  const label = on ? t('chat.audio.pause', { name }) : t('chat.audio.play', { name });

  return (
    <div className="flex min-w-0 max-w-80 flex-col gap-1 rounded-xl border border-line-strong bg-surface px-2 py-1.5" data-chat-audio={name}>
      <audio
        ref={ref}
        src={src}
        preload="metadata"
        onPlay={() => setOn(true)}
        onPause={() => setOn(false)}
        onEnded={() => setTime(0)}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
        onLoadedMetadata={(e) => Number.isFinite(e.currentTarget.duration) && setLength(e.currentTarget.duration)}
        onDurationChange={(e) => setLength(Number.isFinite(e.currentTarget.duration) ? e.currentTarget.duration : 0)}
        onError={() => setFailed(true)}
      />
      <div className="flex min-w-0 items-center gap-2">
        <button
          type="button"
          aria-label={label}
          title={label}
          disabled={failed}
          onClick={toggle}
          className="flex size-8 shrink-0 items-center justify-center rounded-full bg-surface-raised text-primary transition hover:bg-overlay disabled:opacity-50"
        >
          {on ? <Pause className="size-4" /> : <Play className="size-4" />}
        </button>
        <span className="flex min-w-0 flex-1 flex-col text-left leading-tight">
          <span className="truncate text-ui text-primary">{name}</span>
          {size && <span className="text-caption tabular-nums text-subtle">{size}</span>}
        </span>
        {onOpen && (
          <button type="button" aria-label={openLabel} title={openLabel} onClick={onOpen} className="flex size-7 shrink-0 items-center justify-center rounded-lg text-subtle transition hover:text-primary">
            <SquareArrowOutUpRight className="size-3.5" />
          </button>
        )}
      </div>
      {failed ? (
        <div className="text-caption text-subtle">{t('chat.audio.failed')}</div>
      ) : (
        <div className="flex items-center gap-2 text-caption tabular-nums text-subtle">
          <span className="w-9 shrink-0">{clock(time)}</span>
          <input
            type="range"
            min={0}
            max={length || 1}
            step="any"
            value={Math.min(time, length || 1)}
            disabled={!length}
            aria-label={t('chat.audio.seek', { name })}
            onChange={(e) => {
              const el = ref.current;
              if (el) el.currentTime = Number(e.target.value);
              setTime(Number(e.target.value));
            }}
            className={cn('min-w-0 flex-1 accent-brand-500')}
          />
          <span className="w-9 shrink-0 text-right">{clock(length)}</span>
        </div>
      )}
    </div>
  );
}
