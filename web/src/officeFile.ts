import { LocalizedError, message } from './i18n';

// Word and Excel files are read in the browser. The server sends them only
// from the preview address (never counted as a download) and only when the
// name and content agree; these limits keep a huge or hostile archive from
// filling the page's memory.
export const officeMaxBytes = 30 * 1024 * 1024;
export const officeMaxPartBytes = 64 * 1024 * 1024;

export function unsupported() {
  return new LocalizedError(message('errors.preview_unsupported'));
}

export async function officeBytes(url: string, type: string, signal: AbortSignal) {
  const response = await fetch(url, {
    credentials: 'same-origin',
    cache: 'no-store',
    headers: { 'X-MagiSend': '1' },
    signal,
  });
  const got = (response.headers.get('Content-Type') || '').split(';')[0].trim();
  const length = Number(response.headers.get('Content-Length') || 0);
  if (!response.ok || got !== type || length > officeMaxBytes) {
    await response.body?.cancel().catch(() => {});
    throw unsupported();
  }
  const data = await response.arrayBuffer();
  if (data.byteLength > officeMaxBytes) throw unsupported();
  return data;
}

// The unpacked size of every part is checked before anything is shown: first
// the sizes the archive declares, then the real ones, by unpacking each part
// as a stream and stopping as soon as it grows past what it declared or past
// the limits, so a part that lies about its size cannot fill the memory.
export async function openArchive(data: ArrayBuffer) {
  const { default: JSZip } = await import('jszip');
  const zip = await JSZip.loadAsync(data);
  let total = 0;
  const parts: { entry: JSZipEntry; size: number }[] = [];
  zip.forEach((_, entry) => {
    if (entry.dir) return;
    const size = (entry as unknown as { _data?: { uncompressedSize?: number } })._data
      ?.uncompressedSize;
    if (size === undefined || size > officeMaxPartBytes) throw unsupported();
    total += size;
    parts.push({ entry: entry as unknown as JSZipEntry, size });
  });
  if (total > officeMaxPartBytes * 2) throw unsupported();
  let unpacked = 0;
  for (const { entry, size } of parts) {
    const real = await realSize(entry, size, officeMaxPartBytes * 2 - unpacked);
    if (real > size) throw unsupported();
    unpacked += real;
  }
  return zip;
}

interface JSZipEntry {
  internalStream(type: 'uint8array'): {
    on(event: 'data', listener: (chunk: Uint8Array) => void): unknown;
    on(event: 'end', listener: () => void): unknown;
    on(event: 'error', listener: (e: Error) => void): unknown;
    pause(): unknown;
    resume(): unknown;
  };
}

// realSize unpacks a part without keeping it, and gives up as soon as it is
// larger than it declared or than the room left.
function realSize(entry: JSZipEntry, declared: number, room: number): Promise<number> {
  return new Promise((resolve, reject) => {
    let seen = 0;
    let done = false;
    const stream = entry.internalStream('uint8array');
    stream.on('data', (chunk) => {
      seen += chunk.length;
      if (!done && (seen > declared || seen > room)) {
        done = true;
        stream.pause();
        reject(unsupported());
      }
    });
    stream.on('end', () => {
      if (!done) {
        done = true;
        resolve(seen);
      }
    });
    stream.on('error', () => {
      if (!done) {
        done = true;
        reject(unsupported());
      }
    });
    stream.resume();
  });
}
