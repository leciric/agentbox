// A background task's output as the app holds it: bytes [start, end) of
// a file that is still being written, read from the daemon a piece at a
// time (GET …/chat/background/{task}, T.ChatBackgroundDetail). Offsets are
// bytes, so pieces are joined as bytes and read as text only to be shown.
import type * as T from '../../shared/api';

export type OutputBuffer = { start: number; end: number; size: number; text: string };

const encoder = new TextEncoder();
const decoder = new TextDecoder();

// withTail takes the latest read of the output's end. What it holds stays,
// and what is new is added; a read that doesn't join what is held (the file
// grew by more than one piece, or was replaced) starts over from it.
export function withTail(held: OutputBuffer | undefined, d: Pick<T.ChatBackgroundDetail, 'output' | 'outputStart' | 'outputSize'>): OutputBuffer {
  const bytes = encoder.encode(d.output);
  const end = d.outputStart + bytes.length;
  if (!held || d.outputStart > held.end || end < held.end || d.outputStart < held.start) {
    return { start: d.outputStart, end, size: d.outputSize, text: d.output };
  }
  if (end === held.end) return held.size === d.outputSize ? held : { ...held, size: d.outputSize };
  return { start: held.start, end, size: d.outputSize, text: held.text + decoder.decode(bytes.subarray(held.end - d.outputStart)) };
}

// withEarlier puts the piece that ends where the held output starts before it.
export function withEarlier(held: OutputBuffer, d: Pick<T.ChatBackgroundDetail, 'output' | 'outputStart'>): OutputBuffer {
  const bytes = encoder.encode(d.output);
  if (d.outputStart + bytes.length !== held.start) return held;
  return { ...held, start: d.outputStart, text: d.output + held.text };
}
