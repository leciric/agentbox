import { LoaderCircle, SkipForward, Square, Volume2, VolumeOff } from 'lucide-react';
import { skip, stop, useReader } from '../../lib/voice/reader';
import { setReadAloud, useReadAloudSettings } from '../../lib/voice/settings';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Tip } from '../ui/tooltip';

// ReadAloudControls is the chat's speaker: on, the agent's replies are read
// aloud as they come in (useReadAloud, in ChatTab). While it reads, skip and
// stop sit beside it; the first time, it shows the voice downloading.
export function ReadAloudControls() {
  const { on } = useReadAloudSettings();
  const reader = useReader();
  const loading = on && reader.status === 'loading';
  const label = reader.error
    ? `The voice failed: ${reader.error}`
    : loading
      ? reader.progress === undefined
        ? 'Starting the voice'
        : `Downloading the voice, ${Math.round(reader.progress * 100)}% — only the first time`
      : on
        ? 'Stop reading replies aloud'
        : 'Read replies aloud';
  return (
    <span className="flex items-center gap-0.5" data-read-aloud={on ? reader.status : 'off'}>
      {on && reader.status === 'speaking' && (
        <>
          <Tip label="Skip this sentence">
            <Button size="sm" variant="ghost" className="h-7 px-2" aria-label="Skip this sentence" onClick={skip}>
              <SkipForward />
            </Button>
          </Tip>
          <Tip label="Stop reading">
            <Button size="sm" variant="ghost" className="h-7 px-2" aria-label="Stop reading" onClick={stop}>
              <Square />
            </Button>
          </Tip>
        </>
      )}
      <Tip label={label}>
        <Button
          size="sm"
          variant="ghost"
          className={cn('h-7 px-2', on && 'text-brand-300', reader.error && on && 'text-rose-400')}
          aria-label={on ? 'Stop reading replies aloud' : 'Read replies aloud'}
          aria-pressed={on}
          onClick={() => setReadAloud({ on: !on })}
        >
          {loading ? <LoaderCircle className="animate-spin" /> : on ? <Volume2 /> : <VolumeOff />}
          {loading && reader.progress !== undefined && <span className="text-[11.5px] tabular-nums">{Math.round(reader.progress * 100)}%</span>}
        </Button>
      </Tip>
    </span>
  );
}
