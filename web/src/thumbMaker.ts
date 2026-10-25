import { extension } from './fileKind';
import { browserDraws, heifThumb, isHeif } from './heic';

// Pictures the server cannot draw itself: the first page of a PDF, a frame of
// a video, and image types beyond PNG, JPEG and GIF. The owner's browser draws
// them and sends a small PNG, which the server checks and stores like any
// other thumbnail. Each file is tried once; one that fails keeps its icon.
export const browserThumbName = /\.(pdf|mp4|m4v|mov|webm|webp|avif|svg|heic|heif|bmp)$/i;

const edge = 256;
const maxPdfBytes = 100 * 1024 * 1024;
const maxImageBytes = 50 * 1024 * 1024;
const waitLimit = 20_000;
const failedKey = 'magisend.thumbFailed';
const tryingKey = 'magisend.thumbTrying';
// Tries that never finished (the tab closed or crashed) before a file is
// given up on, so a file that crashes the tab is not tried on every visit.
const maxTries = 2;

type Source = { server: { shareId: string; fileId: string }; local?: File };

function kindOf(name: string) {
  const ext = extension(name);
  if (ext === 'pdf') return 'pdf';
  if (['mp4', 'm4v', 'mov', 'webm'].includes(ext)) return 'video';
  return 'image';
}

const imageTypes: Record<string, string> = {
  svg: 'image/svg+xml',
  webp: 'image/webp',
  avif: 'image/avif',
  heic: 'image/heic',
  heif: 'image/heif',
  bmp: 'image/bmp',
};

function fileUrl(source: { shareId: string; fileId: string }, part = '') {
  return (
    '/api/shares/' +
    encodeURIComponent(source.shareId) +
    '/files/' +
    encodeURIComponent(source.fileId) +
    part
  );
}

async function fetched(url: string, limit: number) {
  const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store' });
  if (!response.ok || Number(response.headers.get('Content-Length') || 0) > limit)
    throw new Error('unavailable');
  const blob = await response.blob();
  if (blob.size > limit) throw new Error('too large');
  return blob;
}

function within<T>(promise: Promise<T>) {
  return Promise.race([
    promise,
    new Promise<never>((_, reject) => setTimeout(() => reject(new Error('timeout')), waitLimit)),
  ]);
}

function event(target: EventTarget, name: string) {
  return within(
    new Promise<void>((resolve, reject) => {
      target.addEventListener(name, () => resolve(), { once: true });
      target.addEventListener('error', () => reject(new Error('unreadable')), { once: true });
    }),
  );
}

function fitted(width: number, height: number, draw: (c: CanvasRenderingContext2D) => void) {
  if (!width || !height) throw new Error('empty');
  const scale = Math.min(1, edge / Math.max(width, height));
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(width * scale));
  canvas.height = Math.max(1, Math.round(height * scale));
  const context = canvas.getContext('2d');
  if (!context) throw new Error('no canvas');
  context.scale(canvas.width / width, canvas.height / height);
  draw(context);
  return canvas;
}

async function videoFrame(url: string) {
  const video = document.createElement('video');
  video.muted = true;
  video.playsInline = true;
  video.preload = 'metadata';
  try {
    const ready = event(video, 'loadedmetadata');
    video.src = url;
    await ready;
    const seeked = event(video, 'seeked');
    video.currentTime = Math.min(1, (video.duration || 0) / 4);
    await seeked;
    return fitted(video.videoWidth, video.videoHeight, (c) => c.drawImage(video, 0, 0));
  } finally {
    video.removeAttribute('src');
    video.load();
  }
}

async function imageFrame(blob: Blob, name: string) {
  const typed = new Blob([blob], { type: imageTypes[extension(name)] || blob.type });
  // Only Safari draws HEIC itself; elsewhere it is decoded here.
  if (isHeif(typed.type) && !(await browserDraws(typed))) return within(heifThumb(typed, edge));
  const url = URL.createObjectURL(typed);
  try {
    const image = new Image();
    image.src = url;
    await within(image.decode());
    // An SVG without a size of its own is drawn as a square.
    const width = image.naturalWidth || edge;
    const height = image.naturalHeight || edge;
    return fitted(width, height, (c) => c.drawImage(image, 0, 0, width, height));
  } finally {
    URL.revokeObjectURL(url);
  }
}

async function drawn(source: Source, name: string): Promise<HTMLCanvasElement> {
  const kind = kindOf(name);
  if (kind === 'pdf') {
    const blob = source.local ?? (await fetched(fileUrl(source.server, '/media'), maxPdfBytes));
    if (blob.size > maxPdfBytes) throw new Error('too large');
    const { firstPage } = await import('./pdfThumb');
    return firstPage(new Uint8Array(await blob.arrayBuffer()), edge);
  }
  // A video is always read from the server: the page's policy does not let
  // players use local blob URLs, and the player only fetches the parts it needs.
  if (kind === 'video') return videoFrame(fileUrl(source.server, '/media'));
  const blob = source.local ?? (await fetched(fileUrl(source.server), maxImageBytes));
  if (blob.size > maxImageBytes) throw new Error('too large');
  return imageFrame(blob, name);
}

function failed(): string[] {
  try {
    const list = JSON.parse(localStorage.getItem(failedKey) || '[]');
    return Array.isArray(list) ? list : [];
  } catch {
    return [];
  }
}

function trying(): Record<string, number> {
  try {
    const tries = JSON.parse(localStorage.getItem(tryingKey) || '{}');
    return tries && typeof tries === 'object' && !Array.isArray(tries) ? tries : {};
  } catch {
    return {};
  }
}

// Counts a try before it starts; done clears it again.
function startTry(fileId: string) {
  try {
    const tries = trying();
    tries[fileId] = (Number(tries[fileId]) || 0) + 1;
    const kept = Object.entries(tries).slice(-100);
    localStorage.setItem(tryingKey, JSON.stringify(Object.fromEntries(kept)));
  } catch {
    // Without storage the file is simply tried again on the next visit.
  }
}

function endTry(fileId: string) {
  try {
    const tries = trying();
    delete tries[fileId];
    localStorage.setItem(tryingKey, JSON.stringify(tries));
  } catch {
    // Nothing was stored.
  }
}

function givenUp(fileId: string) {
  return failed().includes(fileId) || (Number(trying()[fileId]) || 0) >= maxTries;
}

function rememberFailure(fileId: string) {
  try {
    localStorage.setItem(failedKey, JSON.stringify([...failed(), fileId].slice(-500)));
  } catch {
    // Without storage the file is simply tried again on the next visit.
  }
}

async function make(shareId: string, fileId: string, name: string, local?: File) {
  if (givenUp(fileId)) return false;
  startTry(fileId);
  try {
    const canvas = await drawn({ server: { shareId, fileId }, local }, name);
    const png = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'));
    canvas.width = 0;
    if (!png) throw new Error('no picture');
    const response = await fetch(fileUrl({ shareId, fileId }, '/thumbnail'), {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'X-MagiSend': '1', 'Content-Type': 'image/png' },
      body: png,
    });
    if (!response.ok) throw new Error('refused');
    return true;
  } catch {
    rememberFailure(fileId);
    return false;
  } finally {
    endTry(fileId);
  }
}

// One file at a time, so a big folder never holds many videos or PDFs open.
const made = new Map<string, Promise<boolean>>();
let queue: Promise<unknown> = Promise.resolve();

function queued(shareId: string, fileId: string, name: string, local?: File) {
  let job = made.get(fileId);
  if (!job) {
    job = queue.then(() => make(shareId, fileId, name, local));
    queue = job;
    made.set(fileId, job);
  }
  return job;
}

// Called for a file the owner sees that has no picture yet. Resolves true
// once the server has one.
export function ensureThumb(shareId: string, file: { id: string; name: string }) {
  if (!browserThumbName.test(file.name) || givenUp(file.id)) return Promise.resolve(false);
  return queued(shareId, file.id, file.name);
}

// Right after an upload the browser still has the files, so nothing needs to
// be downloaded again.
export function thumbsFromUpload(shareId: string, files: { id: string; source: File }[]) {
  for (const { id, source } of files)
    if (browserThumbName.test(source.name)) void queued(shareId, id, source.name, source);
}
