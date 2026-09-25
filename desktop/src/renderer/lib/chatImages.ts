// Pictures attached to a chat message. The daemon keeps each one beside the
// conversation and hands it to the AI tool as an ACP image block; this is the
// renderer's half: reading what was pasted, dropped or picked into something
// the daemon will take.
import type * as T from '../../shared/api';

// The daemon's limits, api.MaxChatImages and api.MaxChatImageBytes: 5 MiB of
// base64 — what Anthropic's API takes in one image — as the file it decodes to.
export const maxImages = 8;
export const maxImageBytes = ((5 << 20) / 4) * 3;

// What the daemon takes, and the model reads.
export const imageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];

export interface PendingImage extends T.ChatImageUpload {
  key: string;
  size: number;
}

let next = 0;

// prepareImage reads an image into what the daemon takes. One the model can't
// read (a BMP, a TIFF) becomes a PNG, and one over the size cap is scaled
// down and made a JPEG, a step at a time, until it fits: a full-screen
// screenshot is often over it as a PNG, and the model resizes anything past
// about 1,568 pixels a side anyway.
export async function prepareImage(file: File): Promise<PendingImage> {
  let blob: Blob = file;
  if (!imageTypes.includes(blob.type)) blob = await reencode(file, 1, 'image/png');
  for (const [edge, quality] of [
    [2560, 0.9],
    [2048, 0.85],
    [1568, 0.85],
    [1024, 0.8],
  ] as const) {
    if (blob.size <= maxImageBytes) break;
    blob = await reencode(file, edge, 'image/jpeg', quality);
  }
  if (blob.size > maxImageBytes) throw new Error(`${file.name || 'The image'} is too big to send, even scaled down.`);
  return {
    key: `img-${++next}`,
    name: file.name || undefined,
    mimeType: blob.type,
    size: blob.size,
    data: await base64(blob),
  };
}

// reencode draws an image at no more than edge pixels a side (or scale, for
// an edge of 1) and encodes it as type.
async function reencode(file: Blob, edge: number, type: string, quality?: number): Promise<Blob> {
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error("That file isn't an image AgentBox can read.");
  });
  const scale = edge === 1 ? 1 : Math.min(1, edge / Math.max(bitmap.width, bitmap.height));
  const canvas = new OffscreenCanvas(Math.max(1, Math.round(bitmap.width * scale)), Math.max(1, Math.round(bitmap.height * scale)));
  const context = canvas.getContext('2d');
  if (!context) throw new Error("Couldn't draw the image to resize it.");
  if (type === 'image/jpeg') {
    // JPEG has no transparency: what was see-through becomes white, not black.
    context.fillStyle = '#fff';
    context.fillRect(0, 0, canvas.width, canvas.height);
  }
  context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  return canvas.convertToBlob({ type, quality });
}

function base64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ''));
    reader.onerror = () => reject(reader.error ?? new Error("Couldn't read the image."));
    reader.readAsDataURL(blob);
  });
}

export const previewUrl = (image: T.ChatImageUpload) => `data:${image.mimeType};base64,${image.data}`;

// imageFiles picks the images out of what was pasted or dropped.
export function imageFiles(data: DataTransfer | null): File[] {
  return Array.from(data?.files ?? []).filter((file) => file.type.startsWith('image/'));
}
