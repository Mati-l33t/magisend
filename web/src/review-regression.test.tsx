import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi, afterEach } from 'vitest';
import { api, ApiError, type Me } from './api';
import App from './App';
import Workspace from './Workspace';
import PublicShare from './PublicShare';
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
  id: 'a'.repeat(48),
  title: 'draft',
  expires: 1900000000,
  created: 1800000000,
  published: false,
  protected: false,
  files: [{ id: 'f1', name: 'hello.txt', size: 5, offset: 0, backend: 'local' }],
};
afterEach(() => vi.restoreAllMocks());
it('preserves HTTP401 status when error response is not JSON', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({
      ok: false,
      status: 401,
      json: async () => {
        throw new SyntaxError('not JSON');
      },
    })),
  );
  await expect(api('/api/me')).rejects.toBeInstanceOf(ApiError);
  await expect(api('/api/me')).rejects.toMatchObject({ status: 401 });
});
it('theme renders even if localStorage access is denied', async () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('denied');
  });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: url != '/api/me',
      status: url === '/api/me' ? 401 : 200,
      json: async () =>
        url === '/api/status'
          ? { setup_required: false, registration_open: false }
          : { error: 'Login' },
    })),
  );
  render(<App />);
  expect(await screen.findByRole('heading', { name: 'Välkommen tillbaka.' })).toBeVisible();
});
it('successful logout unmounts private workspace even when subsequent refresh fails', async () => {
  let logout = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/logout') {
        logout = true;
        return { ok: true, status: 200, json: async () => ({ ok: true }) };
      }
      if (logout) throw new Error('Network unavailable');
      return {
        ok: true,
        status: 200,
        json: async () =>
          url === '/api/status'
            ? { setup_required: false, registration_open: false }
            : url === '/api/me'
              ? me
              : [],
      };
    }),
  );
  render(<App />);
  await screen.findByRole('heading', { name: 'Från dig. Till vem som helst.' });
  fireEvent.click(screen.getByRole('button', { name: 'Kontomeny' }));
  fireEvent.click(screen.getByRole('button', { name: 'Logga ut' }));
  await waitFor(() =>
    expect(
      screen.queryByRole('heading', { name: 'Från dig. Till vem som helst.' }),
    ).not.toBeInTheDocument(),
  );
});
it('publication success is reflected even when browser persistence is unavailable', async () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('denied');
  });
  vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
    throw new Error('denied');
  });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, o: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () =>
        url === '/api/shares'
          ? o.method === 'POST'
            ? sh
            : []
          : url === '/api/uploads/f1'
            ? o.method === 'PATCH'
              ? { offset: 5 }
              : sh.files[0]
            : { ...sh, published: true },
    })),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['hello'], 'hello.txt', { lastModified: 123 })] },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Skapa delningslänk' }));
  expect(await screen.findByRole('heading', { name: 'Redo att delas.' })).toBeVisible();
});
it('failed upload draft remains visible and deletable in share list', async () => {
  let created = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, o: RequestInit) => ({
      ok: !(url === '/api/uploads/f1' && o.method === 'PATCH'),
      status: url === '/api/uploads/f1' && o.method === 'PATCH' ? 507 : 200,
      json: async () =>
        url === '/api/shares'
          ? o.method === 'POST'
            ? ((created = true), sh)
            : created
              ? [sh]
              : []
          : url === '/api/uploads/f1'
            ? o.method === 'PATCH'
              ? { error: 'Storage unavailable' }
              : sh.files[0]
            : {},
    })),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText('Välj filer'), {
    target: { files: [new File(['hello'], 'hello.txt', { lastModified: 123 })] },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Skapa delningslänk' }));
  await screen.findByText('Storage unavailable');
  fireEvent.click(screen.getByRole('button', { name: /Mina delningar/ }));
  expect(await screen.findByRole('button', { name: 'Radera draft' })).toBeVisible();
});
it('transient public share error offers an in-page retry', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      throw new Error('Network unavailable');
    }),
  );
  render(<PublicShare id="x" />);
  expect(await screen.findByRole('button', { name: 'Försök igen' })).toBeVisible();
});
