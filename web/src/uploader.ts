import { ApiError, responseData } from './api';
import { LocalizedError, message } from './i18n';

// Files travel in pieces of this size. The server writes each piece to disk
// before it answers, so a lost connection costs at most one piece.
export const PIECE = 4 * 1024 * 1024;

// Waits between attempts after a failed piece, in seconds: about two minutes
// in total before the upload gives up and can be resumed by hand.
const retryDelays = [1, 2, 4, 8, 15, 30, 30, 30];
const parallelFiles = 3;

export interface UploadJob {
  source: Blob;
  size: number;
  // How much of the file the server already has.
  offset: number;
  url: string;
  headers?: Record<string, string>;
  // Asks the server how much of the file it has now.
  status: (signal: AbortSignal) => Promise<number>;
}

export interface UploadStats {
  sent: number;
  total: number;
  // Bytes per second over the last few seconds; 0 until it is known.
  speed: number;
  // Seconds until done, or null while unknown.
  left: number | null;
  // True while a piece is waiting to be tried again.
  retrying: boolean;
}

function transient(e: unknown) {
  if (e instanceof ApiError) return [408, 425, 429, 500, 502, 503, 504].includes(e.status);
  // fetch rejects with a TypeError when the connection fails.
  return e instanceof TypeError;
}

function aborted(signal: AbortSignal) {
  if (signal.aborted) throw new DOMException('Paused', 'AbortError');
}

function wait(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const done = () => {
      clearTimeout(timer);
      window.removeEventListener('online', done);
      signal.removeEventListener('abort', stop);
      resolve();
    };
    const stop = () => {
      clearTimeout(timer);
      window.removeEventListener('online', done);
      reject(new DOMException('Paused', 'AbortError'));
    };
    const timer = setTimeout(done, ms);
    // Coming back online ends the wait early.
    if (!navigator.onLine) window.addEventListener('online', done, { once: true });
    signal.addEventListener('abort', stop, { once: true });
  });
}

// Measures speed over a sliding window so the figure follows changes in the
// connection without jumping around with every piece.
class SpeedMeter {
  private samples: [number, number][] = [];
  private bytes = 0;
  constructor(private readonly window = 8000) {
    this.samples.push([Date.now(), 0]);
  }
  add(n: number) {
    this.bytes += n;
    const now = Date.now();
    this.samples.push([now, this.bytes]);
    while (this.samples.length > 2 && this.samples[1][0] < now - this.window) this.samples.shift();
  }
  speed() {
    const now = Date.now();
    const [start, base] = this.samples[0];
    const elapsed = (now - start) / 1000;
    if (elapsed < 1 || this.bytes === base) return 0;
    return (this.bytes - base) / elapsed;
  }
}

// Sends every job's remaining pieces, a few files at a time. A failed piece
// is tried again after a pause; when a reply was lost the server is asked how
// far it got. Errors that retrying cannot fix end the upload at once.
export async function uploadAll(
  jobs: UploadJob[],
  options: { signal?: AbortSignal; onProgress?: (stats: UploadStats) => void } = {},
) {
  const outer = options.signal ?? new AbortController().signal;
  const stop = new AbortController();
  const signal = AbortSignal.any ? AbortSignal.any([outer, stop.signal]) : outer;
  const total = jobs.reduce((n, j) => n + j.size, 0);
  let sent = jobs.reduce((n, j) => n + j.offset, 0);
  let retrying = 0;
  const meter = new SpeedMeter();
  const report = () => {
    const speed = meter.speed();
    options.onProgress?.({
      sent,
      total,
      speed,
      left: speed > 0 ? (total - sent) / speed : null,
      retrying: retrying > 0,
    });
  };
  const moved = (n: number) => {
    sent += n;
    if (n > 0) meter.add(n);
    report();
  };

  async function one(job: UploadJob) {
    let offset = job.offset;
    let failures = 0;
    while (offset < job.size) {
      aborted(signal);
      const end = Math.min(job.size, offset + PIECE);
      try {
        const response = await fetch(job.url, {
          method: 'PATCH',
          credentials: 'same-origin',
          headers: {
            'X-MagiSend': '1',
            ...job.headers,
            'Upload-Offset': String(offset),
            'Content-Type': 'application/octet-stream',
          },
          body: job.source.slice(offset, end),
          signal,
        });
        const data = await responseData<{ offset: number }>(response);
        if (!data || data.offset !== end)
          throw new LocalizedError(message('errors.offsetMismatch'));
        moved(end - offset);
        offset = end;
        failures = 0;
        continue;
      } catch (e) {
        if ((e as Error).name === 'AbortError' || signal.aborted) throw e;
        const conflict = e instanceof ApiError && e.status === 409;
        if (!conflict && !transient(e)) throw e;
        if (failures >= retryDelays.length) throw e;
        retrying++;
        report();
        try {
          if (!conflict) await wait(retryDelays[failures] * 1000, signal);
          failures++;
          // The piece may have arrived even though the reply did not.
          let now: number;
          try {
            now = await job.status(signal);
          } catch (s) {
            if ((s as Error).name === 'AbortError' || !transient(s)) throw s;
            continue;
          }
          if (!Number.isSafeInteger(now) || now < 0 || now > job.size)
            throw new LocalizedError(message('errors.invalidOffset'));
          // A conflict the server's position does not explain is final.
          if (conflict && now === offset) throw e;
          moved(now - offset);
          offset = now;
        } finally {
          retrying--;
        }
      }
    }
  }

  let next = 0;
  const worker = async () => {
    while (next < jobs.length) await one(jobs[next++]);
  };
  const ticker = setInterval(report, 1000);
  report();
  try {
    await Promise.all(
      Array.from({ length: Math.min(parallelFiles, jobs.length) }, () =>
        worker().catch((e) => {
          stop.abort();
          throw e;
        }),
      ),
    );
  } finally {
    clearInterval(ticker);
  }
  report();
}

// Asks the browser to confirm before the page is closed during an upload.
export function guardUnload(active: boolean) {
  if (!active) return () => {};
  const warn = (event: BeforeUnloadEvent) => {
    event.preventDefault();
    event.returnValue = '';
  };
  window.addEventListener('beforeunload', warn);
  return () => window.removeEventListener('beforeunload', warn);
}

// What the browser remembers about files in an unfinished upload, so that a
// resumed upload only accepts the same files (the server knows name and size).
interface Remembered {
  name: string;
  size: number;
  lastModified: number;
}
const rememberKey = 'magisend.pending-uploads';
function remembered(): Record<string, Remembered[]> {
  try {
    const value = JSON.parse(localStorage.getItem(rememberKey) || '{}');
    return value && typeof value === 'object' ? value : {};
  } catch {
    return {};
  }
}
function store(value: Record<string, Remembered[]>) {
  try {
    localStorage.setItem(rememberKey, JSON.stringify(value));
  } catch {
    // Without storage a resumed upload matches on name and size only.
  }
}
export function rememberFiles(id: string, files: File[]) {
  store({
    ...remembered(),
    [id]: files.map((f) => ({ name: f.name, size: f.size, lastModified: f.lastModified })),
  });
}
export function forgetFiles(id: string) {
  const all = remembered();
  delete all[id];
  store(all);
}
// Finds the chosen file that matches an unfinished one.
export function matchFile(id: string, target: { name: string; size: number }, chosen: File[]) {
  const known = remembered()[id]?.find((r) => r.name === target.name);
  return chosen.find(
    (f) =>
      f.name === target.name &&
      f.size === target.size &&
      (!known || known.lastModified === f.lastModified),
  );
}
