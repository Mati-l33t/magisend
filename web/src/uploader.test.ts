import { afterEach, expect, it, vi } from 'vitest';
import { PIECE, uploadAll, type UploadStats } from './uploader';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const reply = (status: number, data: unknown) => ({
  ok: status < 400,
  status,
  json: async () => data,
});

it('retries a piece after a lost connection and continues where the server is', async () => {
  vi.useFakeTimers();
  const size = PIECE * 2 + 10;
  let server = 0;
  let calls = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, o: RequestInit) => {
      const offset = Number((o.headers as Record<string, string>)['Upload-Offset']);
      const length = (o.body as Blob).size;
      calls++;
      // The second piece is written, but its reply never arrives.
      if (calls === 2) {
        server = offset + length;
        throw new TypeError('Failed to fetch');
      }
      if (offset !== server) return reply(409, { error_code: 'upload_offset' });
      server = offset + length;
      return reply(200, { offset: server });
    }),
  );
  const seen: UploadStats[] = [];
  const done = uploadAll(
    [
      {
        source: new Blob([new Uint8Array(size)]),
        size,
        offset: 0,
        url: '/api/uploads/f1',
        status: async () => server,
      },
    ],
    { onProgress: (s) => seen.push(s) },
  );
  await vi.advanceTimersByTimeAsync(5000);
  await done;
  expect(server).toBe(size);
  // Each piece went over the wire once: the lost reply was not resent.
  expect(calls).toBe(3);
  expect(seen.some((s) => s.retrying)).toBe(true);
  expect(seen.at(-1)).toMatchObject({ sent: size, total: size, retrying: false });
});

it('stops at once on an error that retrying cannot fix', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => reply(507, { error: 'Full', error_code: 'storage_full' })),
  );
  await expect(
    uploadAll([
      {
        source: new Blob(['hello']),
        size: 5,
        offset: 0,
        url: '/api/uploads/f1',
        status: async () => 0,
      },
    ]),
  ).rejects.toMatchObject({ status: 507 });
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('sends several files at the same time', async () => {
  let open = 0;
  let most = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, o: RequestInit) => {
      open++;
      most = Math.max(most, open);
      await new Promise((r) => setTimeout(r, 5));
      open--;
      const offset = Number((o.headers as Record<string, string>)['Upload-Offset']);
      return reply(200, { offset: offset + (o.body as Blob).size });
    }),
  );
  const job = (n: number) => ({
    source: new Blob(['file ' + n]),
    size: 6,
    offset: 0,
    url: '/api/uploads/f' + n,
    status: async () => 0,
  });
  await uploadAll([job(1), job(2), job(3), job(4), job(5)]);
  expect(most).toBe(3);
  expect(fetch).toHaveBeenCalledTimes(5);
});
