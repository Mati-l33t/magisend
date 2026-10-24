import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import PublicShare from './PublicShare';
import LibraryShareDialog from './LibraryShareDialog';
import Admin from './Admin';
import { setLanguage } from './i18n';
const share = { id: 'link', title: 'Shared', mode: 'live', expires: 0, files: [] };
const caps = {
  folders: true,
  upload: true,
  share: true,
  edit_own: false,
  grant_edit: true,
  edit_shared: false,
  add_shared: false,
  delete_shared: false,
};
function response(data: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => data };
}
it('anonymous recipient can sign in on the shared URL despite having no rights yet', async () => {
  setLanguage('en', false);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      response(
        url.endsWith('/permissions')
          ? { authenticated: false, edit: false, add: false, delete: false }
          : share,
      ),
    ),
  );
  render(<PublicShare id="link" />);
  expect(await screen.findByRole('button', { name: 'Log in' })).toBeVisible();
  expect(screen.getByLabelText('Username')).toBeVisible();
});
it('failed recipient upload preserves the original error without a second POST', async () => {
  setLanguage('en', false);
  const mock = vi.fn(async (url: string, options?: RequestInit) =>
    options?.method === 'POST'
      ? response({ error: 'quota exhausted', error_code: 'quota_exceeded' }, 413)
      : response(
          url.endsWith('/permissions')
            ? { authenticated: true, edit: false, add: true, delete: false }
            : share,
        ),
  );
  vi.stubGlobal('fetch', mock);
  const { container } = render(<PublicShare id="link" />);
  await screen.findByRole('button', { name: /Add shared file/ });
  const file = new File(['abc'], 'test.txt');
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new ArrayBuffer(3) });
  fireEvent.change(container.querySelector('input[type=file]')!, { target: { files: [file] } });
  await screen.findByRole('alert');
  expect(screen.getByRole('alert')).toHaveTextContent('Your storage quota is insufficient');
  expect(mock.mock.calls.filter(([, o]) => o?.method === 'POST')).toHaveLength(1);
});
it('oversize recipient file is rejected before reading or posting it', async () => {
  setLanguage('en', false);
  const mock = vi.fn(async (url: string, _options?: RequestInit) =>
    response(
      url.endsWith('/permissions')
        ? { authenticated: true, edit: false, add: true, delete: false }
        : share,
    ),
  );
  vi.stubGlobal('fetch', mock);
  const { container } = render(<PublicShare id="link" />);
  await screen.findByRole('button', { name: /Add shared file/ });
  const file = new File(['a'], 'large.txt');
  Object.defineProperty(file, 'size', { value: 4 * 1024 * 1024 + 1 });
  const read = vi.fn(async () => new ArrayBuffer(1));
  Object.defineProperty(file, 'arrayBuffer', { value: read });
  fireEvent.change(container.querySelector('input[type=file]')!, { target: { files: [file] } });
  await screen.findByRole('alert');
  expect(read).not.toHaveBeenCalled();
  expect(mock.mock.calls.filter(([, o]) => o?.method === 'POST')).toHaveLength(0);
});
it('create shares and explicit permissions in one POST, not a later PATCH', async () => {
  setLanguage('en', false);
  const mock = vi.fn(async (_url: string, _options?: RequestInit) => response(share, 201));
  vi.stubGlobal('fetch', mock);
  render(
    <LibraryShareDialog
      files={['file']}
      daysDefault={7}
      caps={caps}
      close={() => {}}
      changed={() => {}}
    />,
  );
  fireEvent.change(screen.getByLabelText('Recipient usernames'), {
    target: { value: 'recipient' },
  });
  fireEvent.click(screen.getByLabelText('Allow text edits'));
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await screen.findByRole('textbox', { name: 'Share link' });
  expect(mock).toHaveBeenCalledTimes(1);
  expect(JSON.parse(mock.mock.calls[0][1]?.body as string)).toMatchObject({
    recipients: ['recipient'],
    edit: true,
    add: false,
    delete: false,
  });
});
it('Swedish capability controls are translated and autosave has no misleading Save button', async () => {
  setLanguage('sv', false);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      response(
        url === '/api/admin/users'
          ? [{ id: 'r', username: 'recipient', role: 'user', quota: 100, used: 0 }]
          : url === '/api/admin/capabilities'
            ? { defaults: caps, overrides: {} }
            : {},
      ),
    ),
  );
  render(
    <Admin
      policy={{
        registration_open: false,
        default_quota: 100,
        max_file: 100,
        max_share: 100,
        default_days: 7,
      }}
      reload={async () => {}}
    />,
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Behörigheter för recipient' }));
  expect(
    await screen.findByRole('radiogroup', { name: 'Redigera delad text: recipient' }),
  ).toBeVisible();
  expect(screen.queryByRole('button', { name: /Spara behörigheter:/ })).not.toBeInTheDocument();
});
