// Files attached to a chat message. A picture goes to the AI tool as an ACP
// image block (chatImages.ts); anything else, or a picture for a tool that
// can't read images, the daemon puts in the chat's working directory and
// names in the prompt (internal/chat/files.go). This sorts what was picked,
// pasted or dropped into the one or the other and checks the daemon's limits.
import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';
import { maxImages, prepareImage, type PendingImage } from './chatImages.ts';
import { pickImageName, type PendingElement } from './picks.ts';

// The daemon's limits: api.MaxChatFiles, api.MaxChatFileBytes and
// api.MaxChatFilesBytes.
export const maxFiles = 10;
export const maxFileBytes = 25 << 20;
export const maxFilesBytes = 50 << 20;

export interface PendingFile extends T.ChatFileUpload {
  key: string;
  size: number;
  file: true;
}

// Pending is one thing waiting in the composer: a picture, a file or an
// element picked in a browser (picks.ts).
export type Pending = PendingImage | PendingFile | PendingElement;

export const isFile = (p: Pending): p is PendingFile => 'file' in p;
export const isElement = (p: Pending): p is PendingElement => 'element' in p;

let next = 0;

export async function prepareFile(file: File): Promise<PendingFile> {
  const name = file.name || t('chat.files.theFile');
  if (file.size > maxFileBytes) throw new Error(t('chat.files.tooBig', { name, max: formatSize(maxFileBytes) }));
  return { key: `file-${++next}`, name, mimeType: file.type || undefined, size: file.size, data: await base64(file), file: true };
}

// prepareAttachment reads what was attached: a picture as one, when the tool
// takes pictures and the app can draw it, and as a file otherwise.
export async function prepareAttachment(file: File, images: boolean): Promise<Pending> {
  if (images && file.type.startsWith('image/')) {
    try {
      return await prepareImage(file);
    } catch {
      // Not a picture the app can read (or shrink enough): the tool gets the file.
    }
  }
  return prepareFile(file);
}

// hasPicture is whether p goes to the AI tool as a picture.
const hasPicture = (p: Pending) => (isElement(p) ? !!p.element.image : !isFile(p));

// room says why adding next to what waits already would break a limit, or ''.
export function room(waiting: Pending[], next: Pending): string {
  if (isFile(next)) {
    const files = waiting.filter(isFile);
    if (files.length >= maxFiles) return t('chat.files.maxFiles', { count: maxFiles });
    if (files.reduce((sum, f) => sum + f.size, next.size) > maxFilesBytes) return t('chat.files.maxTotal', { max: formatSize(maxFilesBytes) });
    return '';
  }
  if (isElement(next) && !next.element.image) return '';
  return waiting.filter(hasPicture).length >= maxImages ? t('chat.composer.maxImages', { count: maxImages }) : '';
}


// uploads is what the daemon takes for what waits in the composer. A pick's
// crop is a picture, or a file for a tool that takes no pictures; its text
// is withPicks's.
export function uploads(pending: Pending[], pictures = true): { images: T.ChatImageUpload[]; files: T.ChatFileUpload[] } {
  const images: T.ChatImageUpload[] = [];
  const files: T.ChatFileUpload[] = [];
  let n = 0;
  for (const p of pending) {
    if (isElement(p)) {
      n++;
      const image = p.element.image;
      if (!image) continue;
      const crop = { mimeType: image.mimeType, data: image.data, name: pickImageName(n, image) };
      (pictures ? images : files).push(crop);
    } else if (isFile(p)) files.push({ name: p.name, mimeType: p.mimeType, data: p.data });
    else images.push({ mimeType: p.mimeType, data: p.data, name: p.name });
  }
  return { images, files };
}

// droppedFiles is every file pasted or dropped.
export const droppedFiles = (data: DataTransfer | null): File[] => Array.from(data?.files ?? []);

// pastedFiles is what a paste attaches: the files on the clipboard, unless it
// holds text as well and they are only a picture of it. Word, Excel and
// LibreOffice put a PNG of the copied cells or paragraphs beside their text,
// and the page sees that PNG as a file; pasting it as one drops the text,
// which is what the person copied. A screenshot has no text, and files copied
// in a file manager aren't bitmaps: Finder and the Linux managers add their
// names or paths as text, which doesn't make them a picture of it. [] is
// "paste the text".
export function pastedFiles(data: DataTransfer | null): File[] {
  const files = droppedFiles(data);
  const text = data?.getData('text/plain') ?? '';
  if (files.length === 0 || text.trim() === '') return files;
  const picturesOfText = files.every((f) => f.type.startsWith('image/')) && !namesOnly(text, files);
  return picturesOfText ? [] : files;
}

// namesOnly says whether every line of the text is the name or path of one of
// the files: what a file manager puts on the clipboard beside a copied file.
function namesOnly(text: string, files: File[]): boolean {
  const names = new Set(files.map((f) => f.name));
  const lines = text.split(/\r\n|\r|\n/).filter((line) => line.trim() !== '');
  return lines.every((line) => {
    const last = lastPart(line.trim());
    // As written first: a file may be called chart%20draft.png. Then as a
    // file:// URL writes it, with its escapes decoded.
    return names.has(last) || names.has(decoded(last));
  });
}

// lastPart is the last part of a path or file:// URL.
const lastPart = (path: string): string => path.replace(/[/\\]+$/, '').split(/[/\\]/).pop() ?? '';

function decoded(part: string): string {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1 << 20) return `${Math.round(bytes / 1024)} KB`;
  if (bytes < 1 << 30) return `${(bytes / (1 << 20)).toFixed(1)} MB`;
  return `${(bytes / (1 << 30)).toFixed(1)} GB`;
}

export type FileKind = 'pdf' | 'sheet' | 'archive' | 'code' | 'text' | 'image' | 'audio' | 'video' | 'other';

const byExtension: Record<string, FileKind> = {
  pdf: 'pdf',
  csv: 'sheet', tsv: 'sheet', xls: 'sheet', xlsx: 'sheet', ods: 'sheet', numbers: 'sheet',
  zip: 'archive', tar: 'archive', gz: 'archive', tgz: 'archive', bz2: 'archive', xz: 'archive', zst: 'archive', '7z': 'archive', rar: 'archive',
  json: 'code', js: 'code', ts: 'code', tsx: 'code', jsx: 'code', go: 'code', py: 'code', rs: 'code', rb: 'code', java: 'code', c: 'code', h: 'code',
  cpp: 'code', sh: 'code', yaml: 'code', yml: 'code', toml: 'code', xml: 'code', html: 'code', css: 'code', sql: 'code',
  mp3: 'audio', wav: 'audio', ogg: 'audio', oga: 'audio', opus: 'audio', m4a: 'audio', aac: 'audio', flac: 'audio', weba: 'audio',
  txt: 'text', md: 'text', log: 'text', rtf: 'text', doc: 'text', docx: 'text', odt: 'text',
};

// fileKind is what a file's icon shows it to be, from its name, else its type.
export function fileKind(name: string, mimeType?: string): FileKind {
  const ext = /\.([^./]+)$/.exec(name)?.[1]?.toLowerCase();
  const known = ext ? byExtension[ext] : undefined;
  if (known) return known;
  const type = mimeType ?? '';
  if (type === 'application/pdf') return 'pdf';
  if (/zip|compressed|tar|gzip/.test(type)) return 'archive';
  for (const kind of ['image', 'audio', 'video', 'text'] as const) if (type.startsWith(`${kind}/`)) return kind;
  return 'other';
}

function base64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ''));
    reader.onerror = () => reject(reader.error ?? new Error(t('chat.files.read')));
    reader.readAsDataURL(blob);
  });
}
