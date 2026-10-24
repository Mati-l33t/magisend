import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import Workspace from './Workspace';
import PublicShare from './PublicShare';
import type { Me } from './api';
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
it('uploads selected bytes through resumable API and publishes only after completion', async () => {
  const id = 'a'.repeat(48);
  let complete = false;
  const sh = {
    id,
    title: 'hello.txt',
    expires: 1900000000,
    created: 1800000000,
    published: false,
    protected: false,
    files: [{ id: 'f1', name: 'hello.txt', size: 5, offset: 0, backend: 'local' }],
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options: RequestInit) => {
      let data: unknown = [];
      if (url === '/api/shares' && options.method === 'POST') data = sh;
      if (url === '/api/uploads/f1' && options.method === 'PATCH') {
        complete = true;
        data = { offset: 5 };
      }
      if (url === '/api/uploads/f1' && options.method === 'GET') data = sh.files[0];
      if (url.endsWith('/publish')) {
        expect(complete).toBe(true);
        data = { ...sh, published: true };
      }
      return { ok: true, status: 200, json: async () => data };
    }),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  const file = new File(['hello'], 'hello.txt', { type: 'text/plain', lastModified: 123 });
  fireEvent.change(screen.getByLabelText('Välj filer'), { target: { files: [file] } });
  await screen.findByText('hello.txt');
  fireEvent.click(screen.getByRole('button', { name: 'Skapa delningslänk' }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      '/api/uploads/f1',
      expect.objectContaining({
        method: 'PATCH',
        headers: expect.objectContaining({ 'Upload-Offset': '0' }),
      }),
    ),
  );
  await screen.findByRole('heading', { name: 'Redo att delas.' });
  expect(screen.getByDisplayValue(`http://localhost:3000/s/${id}`)).toBeVisible();
});
it('password-protected public share stays locked until unlock succeeds', async () => {
  let unlocked = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.endsWith('/unlock')) {
        unlocked = true;
        return { ok: true, status: 200, json: async () => ({ ok: true }) };
      }
      return unlocked
        ? {
            ok: true,
            status: 200,
            json: async () => ({
              id: 's1',
              title: 'Secret share',
              expires: 1900000000,
              files: [{ id: 'f1', name: 'secret.txt', size: 5 }],
            }),
          }
        : { ok: false, status: 401, json: async () => ({ error: 'Delningen kräver lösenord' }) };
    }),
  );
  render(<PublicShare id="s1" />);
  await screen.findByLabelText('Delningens lösenord');
  expect(screen.queryByText('secret.txt')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Delningens lösenord'), { target: { value: 'secret' } });
  fireEvent.click(screen.getByRole('button', { name: 'Öppna delning' }));
  await screen.findByText('secret.txt');
  expect(screen.getByRole('link', { name: 'Ladda ner secret.txt' })).toHaveAttribute(
    'href',
    '/api/public/s1/files/f1',
  );
});
