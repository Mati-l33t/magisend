import { sha256 } from '@noble/hashes/sha2.js';
import { message, LocalizedError } from './i18n';

export interface UploadIdentity {
  name: string;
  size: number;
  lastModified: number;
  sha256: string;
}
function checkAbort(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException('Paused', 'AbortError');
}
async function readBlock(blob: Blob, signal?: AbortSignal): Promise<ArrayBuffer> {
  checkAbort(signal);
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    const abort = () => reader.abort();
    const clean = () => signal?.removeEventListener('abort', abort);
    reader.onload = () => {
      clean();
      resolve(reader.result as ArrayBuffer);
    };
    reader.onerror = () => {
      clean();
      reject(new LocalizedError(message('errors.fileRead')));
    };
    reader.onabort = () => {
      clean();
      reject(new DOMException('Paused', 'AbortError'));
    };
    signal?.addEventListener('abort', abort, { once: true });
    reader.readAsArrayBuffer(blob);
  });
}
export async function fingerprint(file: File, signal?: AbortSignal): Promise<UploadIdentity> {
  const hash = sha256.create();
  try {
    for (let offset = 0; offset < file.size; offset += 1024 * 1024) {
      checkAbort(signal);
      hash.update(
        new Uint8Array(await readBlock(file.slice(offset, offset + 1024 * 1024), signal)),
      );
    }
    checkAbort(signal);
    return {
      name: file.name,
      size: file.size,
      lastModified: file.lastModified,
      sha256: Array.from(hash.digest(), (b) => b.toString(16).padStart(2, '0')).join(''),
    };
  } finally {
    hash.destroy();
  }
}
export function readIdentity(
  id: string,
  memory: Map<string, UploadIdentity[]>,
): UploadIdentity[] | null {
  const cached = memory.get(id);
  if (cached) return cached;
  try {
    const data: unknown = JSON.parse(localStorage.getItem('magisend-upload-' + id) || 'null');
    if (!Array.isArray(data) || data.length === 0 || data.length > 100) return null;
    if (
      !data.every(
        (item) =>
          item &&
          typeof item.name === 'string' &&
          Number.isSafeInteger(item.size) &&
          item.size >= 0 &&
          Number.isFinite(item.lastModified) &&
          typeof item.sha256 === 'string' &&
          /^[a-f0-9]{64}$/.test(item.sha256),
      )
    )
      return null;
    if (new Set(data.map((item) => item.name)).size !== data.length) return null;
    memory.set(id, data);
    return data;
  } catch {
    return null;
  }
}
export function saveIdentity(
  id: string,
  identity: UploadIdentity[],
  memory: Map<string, UploadIdentity[]>,
) {
  memory.set(id, identity);
  try {
    localStorage.setItem('magisend-upload-' + id, JSON.stringify(identity));
  } catch {}
}
export function removeIdentity(id: string, memory: Map<string, UploadIdentity[]>) {
  memory.delete(id);
  try {
    localStorage.removeItem('magisend-upload-' + id);
  } catch {}
}
export function sameIdentity(original: UploadIdentity[], selected: UploadIdentity[]) {
  return (
    original.length === selected.length &&
    original.every((o) =>
      selected.some(
        (n) =>
          n.name === o.name &&
          n.size === o.size &&
          n.lastModified === o.lastModified &&
          n.sha256 === o.sha256,
      ),
    )
  );
}
