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

// The unpacked size of every part is checked before anything is unpacked.
export async function openArchive(data: ArrayBuffer) {
  const { default: JSZip } = await import('jszip');
  const zip = await JSZip.loadAsync(data);
  let total = 0;
  zip.forEach((_, entry) => {
    if (entry.dir) return;
    const size = (entry as unknown as { _data?: { uncompressedSize?: number } })._data
      ?.uncompressedSize;
    if (size === undefined || size > officeMaxPartBytes) throw unsupported();
    total += size;
  });
  if (total > officeMaxPartBytes * 2) throw unsupported();
  return zip;
}
