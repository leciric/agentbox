import { LoaderCircle, Mic } from 'lucide-react';
import { useEffect, useRef, useState, type RefObject } from 'react';
import { toast } from 'sonner';
import { useT } from '../../lib/i18n';
import { silent } from '../../lib/voice/audio';
import { formatMB, pickModel } from '../../lib/voice/models';
import { record, type Recording } from '../../lib/voice/recorder';
import { useVoiceSettings } from '../../lib/voice/settings';
import { preloadWhisper, probeWhisper, transcribe, useWhisper, whisperState } from '../../lib/voice/whisper';
import { cn, errorMessage } from '../../lib/utils';
import { Tip } from '../ui/tooltip';

// VoiceButton is push-to-talk in the chat composer. Hold it, or hold
// Ctrl+Space while the composer is on screen, and talk; let go and what you
// said is transcribed on this machine by Whisper (lib/voice) and handed to
// onText. A quick click instead starts listening until the next click.
//
// holdTime tells a click from a hold.
const holdTime = 350;

// Said once a session: Whisper is on the CPU, and why.
let warnedCPU = false;

export function VoiceButton({ disabled, onText, scope }: { disabled: boolean; onText: (text: string) => void; scope: RefObject<HTMLElement | null> }) {
  const t = useT();
  const settings = useVoiceSettings();
  const whisper = useWhisper();
  const [phase, setPhase] = useState<'idle' | 'starting' | 'listening' | 'transcribing'>('idle');
  const [seconds, setSeconds] = useState(0);
  const recording = useRef<Recording | null>(null);
  const pressedAt = useRef(0);
  const pressing = useRef(false);
  // stopRequested is a let-go that came while the microphone was still
  // opening: it stops as soon as it's open.
  const stopRequested = useRef(false);
  const deliver = useRef(onText);
  deliver.current = onText;

  useEffect(() => probeWhisper(), []);

  useEffect(() => {
    if (phase !== 'listening') return;
    setSeconds(0);
    const started = Date.now();
    const timer = setInterval(() => setSeconds(Math.floor((Date.now() - started) / 1000)), 250);
    return () => clearInterval(timer);
  }, [phase]);

  const finish = async () => {
    const r = recording.current;
    recording.current = null;
    if (!r) return;
    setPhase('transcribing');
    try {
      const audio = await r.stop();
      if (silent(audio)) {
        toast(t('chat.voice.nothingHeard'), { description: t('chat.voice.nothingHeardHint') });
        return;
      }
      const { text } = await transcribe(audio);
      if (text) deliver.current(text);
      else toast(t('chat.voice.nothingHeard'));
    } catch (err) {
      toast.error(t('chat.voice.transcribeFailed', { error: errorMessage(err) }));
    } finally {
      setPhase('idle');
    }
  };

  const begin = async () => {
    if (disabled || phase !== 'idle') return;
    stopRequested.current = false;
    setPhase('starting');
    // The model loads while you talk, so a short phrase isn't waiting on it.
    preloadWhisper();
    try {
      recording.current = await record();
    } catch (err) {
      setPhase('idle');
      toast.error(t('chat.voice.micFailed', { error: errorMessage(err) }));
      return;
    }
    setPhase('listening');
    const { device, reason } = whisperState();
    if (device === 'wasm' && !warnedCPU) {
      warnedCPU = true;
      toast.warning(t('chat.voice.noWebGPU', { model: pickModel(settings.model, 'wasm').label }), { description: t('chat.voice.noWebGPUHint', { reason: reason ?? '' }).trim() });
    }
    if (stopRequested.current) void finish();
  };

  const end = () => {
    if (recording.current) void finish();
    else stopRequested.current = true;
  };

  // Ctrl+Space, held, is the button held — while this composer is on screen
  // (a hidden tab's composer is mounted too, but not visible).
  const active = useRef({ begin, end });
  active.current = { begin, end };
  useEffect(() => {
    if (disabled) return;
    let holding = false;
    const down = (event: KeyboardEvent) => {
      if (event.code !== 'Space' || !event.ctrlKey || event.altKey || event.metaKey || event.shiftKey) return;
      const el = scope.current;
      if (!el || !el.offsetParent) return;
      event.preventDefault();
      if (holding || event.repeat) return;
      holding = true;
      void active.current.begin();
    };
    const up = (event: KeyboardEvent) => {
      if (!holding || (event.code !== 'Space' && event.key !== 'Control')) return;
      holding = false;
      active.current.end();
    };
    const blur = () => {
      if (!holding) return;
      holding = false;
      active.current.end();
    };
    window.addEventListener('keydown', down);
    window.addEventListener('keyup', up);
    window.addEventListener('blur', blur);
    return () => {
      window.removeEventListener('keydown', down);
      window.removeEventListener('keyup', up);
      window.removeEventListener('blur', blur);
    };
  }, [disabled, scope]);

  useEffect(() => () => recording.current?.cancel(), []);

  const model = pickModel(settings.model, whisper.device ?? 'webgpu');
  const loading = whisper.loading;
  const percent = loading && loading.total ? Math.round((loading.loaded / loading.total) * 100) : undefined;
  const status =
    phase === 'listening'
      ? t('chat.voice.listening', { time: `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}` })
      : phase === 'transcribing' && loading
        ? percent !== undefined ? t('chat.voice.downloadingPercent', { percent }) : t('chat.voice.downloading')
        : phase === 'transcribing'
          ? t('chat.voice.transcribing')
          : undefined;
  const label = disabled
    ? t('chat.voice.disabled')
    : t('chat.voice.hold', {
        model: model.label,
        device: whisper.device ?? 'unknown',
        first: whisper.ready !== model.id ? 'yes' : 'no',
        size: formatMB(model.size[whisper.device ?? 'webgpu']),
      });

  return (
    <div className="flex shrink-0 items-center gap-1.5">
      {status && (
        <span className="max-w-48 truncate text-[11px] tabular-nums text-subtle" aria-live="polite">
          {status}
          {phase !== 'listening' && whisper.device === 'wasm' && t('chat.voice.cpuSuffix')}
        </span>
      )}
      <Tip label={label}>
        <span className="shrink-0">
          <button
            type="button"
            aria-label={phase === 'listening' ? t('chat.voice.stopTalking') : t('chat.voice.talk')}
            aria-pressed={phase === 'listening'}
            disabled={disabled || phase === 'transcribing'}
            onPointerDown={(event) => {
              if (event.button !== 0) return;
              // A second click ends what a first, quick one started.
              if (phase !== 'idle') return end();
              // Captured, so letting go off the button still counts.
              event.currentTarget.setPointerCapture(event.pointerId);
              pressing.current = true;
              pressedAt.current = Date.now();
              void begin();
            }}
            onPointerUp={() => {
              // A hold stops on letting go; a quick click keeps listening
              // until the next one.
              if (!pressing.current) return;
              pressing.current = false;
              if (Date.now() - pressedAt.current > holdTime) end();
            }}
            onClick={(event) => {
              // Enter or Space on the focused button: a click, toggling.
              if (event.detail !== 0) return;
              if (phase === 'idle') void begin();
              else end();
            }}
            className={cn(
              'flex size-8 items-center justify-center rounded-full text-subtle transition hover:bg-surface-raised hover:text-primary disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent',
              (phase === 'listening' || phase === 'starting') && 'bg-rose-500/90 text-white hover:bg-rose-500 hover:text-white',
            )}
          >
            {phase === 'transcribing' ? <LoaderCircle className="size-4 animate-spin" /> : <Mic className={cn('size-4', phase === 'listening' && 'animate-pulse')} />}
          </button>
        </span>
      </Tip>
    </div>
  );
}
