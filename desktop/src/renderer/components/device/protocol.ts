// The device stream's wire format, the viewer's half: what the server sends
// (config, frames, status) and the input the viewer sends back. The format
// itself is described in internal/devicestream/protocol.go; it is the same for
// every kind of device, so this file knows nothing of Android.

export type DeviceConfig = {
  v: number;
  platform: string;
  codec: string; // RFC 6381, like "avc1.42c01f"
  width: number;
  height: number;
  description: Uint8Array; // the AVCDecoderConfigurationRecord
};

export type ServerMessage =
  | { kind: 'config'; config: DeviceConfig }
  | { kind: 'frame'; key: boolean; timestamp: number; data: Uint8Array }
  | { kind: 'status'; error: string };

const decoder = new TextDecoder();

function base64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// parseMessage reads a server message, or returns null for one it doesn't know.
export function parseMessage(data: Uint8Array): ServerMessage | null {
  if (data.length === 0) return null;
  switch (data[0]) {
    case 0x01: {
      const raw = JSON.parse(decoder.decode(data.subarray(1))) as Omit<DeviceConfig, 'description'> & { description: string };
      return { kind: 'config', config: { ...raw, description: base64(raw.description) } };
    }
    case 0x02: {
      if (data.length < 10) return null;
      const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
      return { kind: 'frame', key: (data[1] & 1) === 1, timestamp: Number(view.getBigUint64(2)), data: data.subarray(10) };
    }
    case 0x03:
      return { kind: 'status', error: (JSON.parse(decoder.decode(data.subarray(1))) as { error: string }).error };
  }
  return null;
}

export type TouchAction = 'down' | 'move' | 'up' | 'cancel';
export type DeviceButton = 'home' | 'back' | 'recents' | 'rotate';

export type DeviceInput =
  | { type: 'touch'; action: TouchAction; x: number; y: number; id: number }
  | { type: 'key'; action: 'down' | 'up'; key: string }
  | { type: 'text'; text: string }
  | { type: 'wheel'; x: number; y: number; dx: number; dy: number }
  | { type: 'button'; button: DeviceButton }
  | { type: 'keyframe' };

export type Rect = { left: number; top: number; width: number; height: number };

// videoPoint maps a point on the page to the video shown in rect, from 0 to
// 1 across it, clamped to its edges.
export function videoPoint(clientX: number, clientY: number, rect: Rect): { x: number; y: number } {
  const clamp = (v: number) => Math.min(1, Math.max(0, v));
  return { x: clamp((clientX - rect.left) / rect.width), y: clamp((clientY - rect.top) / rect.height) };
}

// fit is the largest size of a width x height video that fits in the box.
export function fit(width: number, height: number, boxWidth: number, boxHeight: number): { width: number; height: number } {
  if (width <= 0 || height <= 0 || boxWidth <= 0 || boxHeight <= 0) return { width: 0, height: 0 };
  const scale = Math.min(boxWidth / width, boxHeight / height);
  return { width: Math.floor(width * scale), height: Math.floor(height * scale) };
}

// The keys sent by name: the rest of the keyboard is text.
const namedKeys = new Set([
  'Enter',
  'Backspace',
  'Delete',
  'Tab',
  'Escape',
  'ArrowLeft',
  'ArrowRight',
  'ArrowUp',
  'ArrowDown',
  'Home',
  'End',
  'PageUp',
  'PageDown',
  'Shift',
  'Control',
  'Alt',
  'Meta',
]);

type KeyLike = { key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean };

// keyInput is what a key press sends: a character typed as text, a named
// key down or up, or nothing for keys the device has no use for (F1, a dead
// key). A character with Control or Meta held (Control-A) goes as a key, so
// the device sees the shortcut.
export function keyInput(e: KeyLike, action: 'down' | 'up'): DeviceInput | null {
  if (namedKeys.has(e.key)) return { type: 'key', action, key: e.key };
  if ([...e.key].length !== 1) return null;
  if (e.ctrlKey || e.metaKey) return { type: 'key', action, key: e.key };
  return action === 'down' ? { type: 'text', text: e.key } : null;
}

// wheelClicks turns a WheelEvent's delta into wheel clicks, which is what
// the device scrolls by: about 100 pixels, or 3 lines, a click.
export function wheelClicks(delta: number, deltaMode: number): number {
  if (deltaMode === 1) return delta / 3; // lines
  if (deltaMode === 2) return delta * 3; // pages
  return delta / 100;
}
