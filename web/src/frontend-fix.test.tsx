import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi, afterEach } from 'vitest';
import App from './App';
import Workspace from './Workspace';
import PublicShare from './PublicShare';
import { api, type Me } from './api';
import { fingerprint, sameIdentity, readIdentity, type UploadIdentity } from './uploadIdentity';
const me: Me = {
  user: { id: 'u1', username: 'mati', role: 'admin', quota: 1073741824, used: 0 },
  policy: {
    registration_open: false,
    default_quota: 1073741824,
    max_file: 1073741824,
    max_share: 2147483648,
    default_days: 7,
  },
};
const sh = {
  id: 'b'.repeat(48),
  title: 'draft',
  expires: 1900000000,
  created: 1800000000,
  published: false,
  protected: false,
  files: [{ id: 'f1', name: 'hello.txt', size: 5, offset: 2, backend: 'local' }],
};
const response = (data: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => data,
});
afterEach(() => vi.restoreAllMocks());
it('full byte fingerprints distinguish equal name size and mtime without WebCrypto', async () => {
  const original = await fingerprint(new File(['hello'], 'hello.txt', { lastModified: 123 }));
  const other = await fingerprint(new File(['jello'], 'hello.txt', { lastModified: 123 }));
  expect(original.sha256).toMatch(/^[a-f0-9]{64}$/);
  expect(other.sha256).not.toBe(original.sha256);
  expect(sameIdentity([original], [other])).toBe(false);
  expect(
    sameIdentity(
      [original],
      [await fingerprint(new File(['hello'], 'hello.txt', { lastModified: 123 }))],
    ),
  ).toBe(true);
});
it('hashes the whole file in bounded 1MiB blocks', async () => {
  const file = new File([new Uint8Array(1048577)], 'blocks.bin');
  const slice = vi.spyOn(file, 'slice');
  await fingerprint(file);
  expect(slice).toHaveBeenNthCalledWith(1, 0, 1048576);
  expect(slice).toHaveBeenNthCalledWith(2, 1048576, 2097152);
  expect(slice).toHaveBeenCalledTimes(2);
});
it('rejects malformed and legacy metadata-only resume storage safely', () => {
  const memory = new Map<string, UploadIdentity[]>();
  for (const value of ['{', '{}', '[{"name":"hello.txt","size":5,"lastModified":123}]']) {
    localStorage.setItem('magisend-upload-x', value);
    expect(readIdentity('x', memory)).toBeNull();
  }
});
it('rejects different bytes on actual nonzero-offset resume before PATCH or publish', async () => {
  const original = await fingerprint(new File(['hello'], 'hello.txt', { lastModified: 123 }));
  localStorage.setItem('magisend-upload-' + sh.id, JSON.stringify([original]));
  const fetcher = vi.fn(async (url: string) =>
    response(url === '/api/shares' ? [sh] : sh.files[0]),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  await screen.findByRole('button', { name: /Mina delningar/ });
  fireEvent.click(screen.getByRole('button', { name: /Mina delningar/ }));
  fireEvent.click(await screen.findByRole('button', { name: 'Återuppta' }));
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['jello'], 'hello.txt', { lastModified: 123 })] },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Fortsätt uppladdning' }));
  expect(await screen.findByText(/Innehåll, namn, storlek/)).toBeVisible();
  expect(
    fetcher.mock.calls
      // Reads only: the update check and the folders a link can keep its files in.
      .filter(([url]) => !['/api/admin/update', '/api/capabilities', '/api/folders'].includes(url))
      .every(([url]) => url === '/api/shares'),
  ).toBe(true);
});
it('resumes identical bytes from the saved offset', async () => {
  const original = await fingerprint(new File(['hello'], 'hello.txt', { lastModified: 123 }));
  localStorage.setItem('magisend-upload-' + sh.id, JSON.stringify([original]));
  const fetcher = vi.fn(async (url: string, o: RequestInit) =>
    response(
      url === '/api/shares'
        ? [sh]
        : url.endsWith('/publish')
          ? { ...sh, published: true }
          : o.method === 'PATCH'
            ? { offset: 5 }
            : sh.files[0],
    ),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.click(screen.getByRole('button', { name: /Mina delningar/ }));
  fireEvent.click(await screen.findByRole('button', { name: 'Återuppta' }));
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['hello'], 'hello.txt', { lastModified: 123 })] },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Fortsätt uppladdning' }));
  await screen.findByRole('heading', { name: 'Redo att delas.' });
  expect(fetcher).toHaveBeenCalledWith(
    '/api/uploads/f1',
    expect.objectContaining({
      method: 'PATCH',
      headers: expect.objectContaining({ 'Upload-Offset': '2' }),
      body: expect.objectContaining({ size: 3 }),
    }),
  );
});
it('conflicting basenames produce a visible rejection', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => response([])),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['hello'], 'hello.txt'), new File(['other'], 'hello.txt')] },
  });
  expect(screen.getByRole('alert')).toHaveTextContent('Flera filer har samma namn');
  expect(screen.getByRole('button', { name: 'Skapa delningslänk' })).toBeDisabled();
});
it('rejects an out-of-bounds initial offset before sending bytes', async () => {
  const fetcher = vi.fn(async (url: string, o: RequestInit) =>
    response(
      url === '/api/shares' ? (o.method === 'POST' ? sh : []) : { ...sh.files[0], offset: 6 },
    ),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['hello'], 'hello.txt')] },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Skapa delningslänk' }));
  expect(await screen.findByText('Serverns uppladdningsposition är ogiltig.')).toBeVisible();
  expect(fetcher.mock.calls.some(([, o]) => o.method === 'PATCH')).toBe(false);
});
it('publication and deletion survive refresh and local cleanup errors', async () => {
  vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
    throw new Error('denied');
  });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, o: RequestInit) =>
      response(
        url === '/api/shares'
          ? o.method === 'POST'
            ? sh
            : []
          : url.endsWith('/publish')
            ? { ...sh, published: true }
            : o.method === 'PATCH'
              ? { offset: 5 }
              : o.method === 'DELETE'
                ? {}
                : sh.files[0],
      ),
    ),
  );
  render(
    <Workspace
      me={me}
      reload={vi.fn(async () => {
        throw new Error('refresh unavailable');
      })}
    />,
  );
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['hello'], 'hello.txt')] },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Skapa delningslänk' }));
  await screen.findByRole('heading', { name: 'Redo att delas.' });
  expect(await screen.findByText(/Delningen publicerades, men kontot/)).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Stäng' }));
  fireEvent.click(screen.getByRole('button', { name: /Mina delningar/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Radera draft' }));
  fireEvent.click(screen.getByRole('button', { name: 'Radera delning' }));
  await screen.findByText(/Delningen raderades, men kontot/);
  expect(screen.queryByRole('button', { name: 'Radera draft' })).not.toBeInTheDocument();
});
it('tokenless setup is once-only and failed login recovers without repeating setup', async () => {
  let created = false;
  let loginAttempts = 0;
  const fetcher = vi.fn(async (url: string) => {
    if (url === '/api/setup') {
      created = true;
      return response({});
    }
    if (url === '/api/login') {
      loginAttempts++;
      return loginAttempts === 1 ? response({ error: 'Login unavailable' }, 503) : response({});
    }
    if (url === '/api/me')
      return loginAttempts >= 2 ? response(me) : response({ error: 'Login' }, 401);
    return url === '/api/status'
      ? response({ setup_required: !created, registration_open: false })
      : response([]);
  });
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('button', { name: 'Skapa administratör' });
  expect(screen.queryByLabelText('Installationsnyckel')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Användarnamn'), { target: { value: 'mati' } });
  fireEvent.change(screen.getByLabelText('Lösenord'), { target: { value: 'some-long-password' } });
  fireEvent.change(screen.getByLabelText('Upprepa lösenord'), {
    target: { value: 'some-long-password' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Skapa administratör' }));
  await screen.findByText('Login unavailable');
  expect(screen.queryByRole('button', { name: 'Skapa administratör' })).not.toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledWith(
    '/api/setup',
    expect.objectContaining({
      body: JSON.stringify({ username: 'mati', password: 'some-long-password' }),
    }),
  );
  expect(screen.getByLabelText('Lösenord')).toHaveValue('');
  fireEvent.change(screen.getByLabelText('Lösenord'), { target: { value: 'some-long-password' } });
  fireEvent.click(screen.getByRole('button', { name: 'Logga in' }));
  await waitFor(() => expect(loginAttempts).toBe(2));
  await screen.findByRole('heading', { name: 'Från dig. Till vem som helst.' });
  expect(fetcher.mock.calls.filter(([url]) => url === '/api/setup')).toHaveLength(1);
});
it('anonymous session refresh failure is visible and retries without resending credentials', async () => {
  let loggedIn = false;
  let failed = true;
  const fetcher = vi.fn(async (url: string) => {
    if (url === '/api/login') {
      loggedIn = true;
      return response({});
    }
    if (url === '/api/status') return response({ setup_required: false, registration_open: false });
    if (url === '/api/me') {
      if (loggedIn && failed) throw new Error('Session refresh unavailable');
      return loggedIn ? response(me) : response({}, 401);
    }
    return response([]);
  });
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('heading', { name: 'Välkommen tillbaka.' });
  fireEvent.change(screen.getByLabelText('Användarnamn'), { target: { value: 'mati' } });
  fireEvent.change(screen.getByLabelText('Lösenord'), { target: { value: 'some-long-password' } });
  fireEvent.click(screen.getByRole('button', { name: 'Logga in' }));
  await screen.findByText('Session refresh unavailable');
  expect(screen.getByLabelText('Lösenord')).toHaveValue('');
  failed = false;
  fireEvent.click(screen.getByRole('button', { name: 'Försök igen' }));
  await screen.findByRole('heading', { name: 'Från dig. Till vem som helst.' });
  expect(fetcher.mock.calls.filter(([url]) => url === '/api/login')).toHaveLength(1);
});
it('public transient retry resets error state and loads the share', async () => {
  let failed = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      if (failed) throw new Error('Network unavailable');
      return response({ ...sh, published: true });
    }),
  );
  render(<PublicShare id={sh.id} />);
  await screen.findByRole('button', { name: 'Försök igen' });
  failed = false;
  fireEvent.click(screen.getByRole('button', { name: 'Försök igen' }));
  await screen.findByRole('heading', { name: 'draft' });
  expect(screen.queryByText('Network unavailable')).not.toBeInTheDocument();
});
it('empty no-content success does not invent an upload acknowledgement', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({
      ok: true,
      status: 204,
      json: async () => {
        throw new SyntaxError('empty');
      },
    })),
  );
  await expect(api('/api/logout', 'POST', {})).resolves.toBeUndefined();
});
it('accepts an empty successful body and preserves a non-JSON failure with real body decoding', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, status: 200, text: async () => '' })),
  );
  await expect(api('/api/logout', 'POST', {})).resolves.toBeUndefined();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: false, status: 503, text: async () => '<html>Unavailable</html>' })),
  );
  await expect(api('/api/me')).rejects.toMatchObject({ status: 503 });
});
