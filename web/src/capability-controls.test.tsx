import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import Admin from './Admin';
import PublicShare from './PublicShare';
import { setLanguage } from './i18n';
const policy = {
  registration_open: false,
  default_quota: 1073741824,
  max_file: 1073741824,
  max_share: 2147483648,
  default_days: 7,
};
it('admin can set a user override back to dynamic default inheritance', async () => {
  setLanguage('en', false);
  const defaults = {
    folders: true,
    upload: true,
    share: true,
    edit_own: false,
    grant_edit: false,
    edit_shared: false,
    add_shared: false,
    delete_shared: false,
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () =>
        url === '/api/admin/users'
          ? [{ id: 'recipient', username: 'recipient', role: 'user', quota: 1073741824, used: 0 }]
          : url === '/api/admin/capabilities'
            ? { defaults, overrides: { recipient: { edit_shared: true } } }
            : {
                backend: 'local',
                reserved_bytes: 0,
                capacity_bytes: 4294967296,
                disk_free_bytes: 4294967296,
              },
    })),
  );
  render(<Admin policy={policy} reload={async () => {}} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Permissions for recipient' }));
  const group = await screen.findByRole('radiogroup', { name: 'Edit shared text: recipient' });
  expect(within(group).getByRole('radio', { name: 'Allow' })).toBeChecked();
  fireEvent.click(within(group).getByRole('radio', { name: 'Default · off' }));

  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      '/api/admin/capabilities/users/recipient',
      expect.objectContaining({
        method: 'PUT',
        body: expect.stringContaining('"edit_shared":null'),
      }),
    ),
  );
  expect(screen.getByLabelText('Edit own files: default')).not.toBeChecked();
});
it('public collaboration uses server rights and keeps add and delete separate', async () => {
  setLanguage('en', false);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () =>
        url.endsWith('/permissions')
          ? { edit: true, add: false, delete: false }
          : {
              id: 'link',
              title: 'Live',
              mode: 'live',
              expires: 0,
              files: [{ id: 'file', name: 'notes.md', size: 4, offset: 4, backend: 'local' }],
            },
    })),
  );
  render(<PublicShare id="link" />);
  expect(await screen.findByRole('button', { name: 'Edit notes.md' })).toBeVisible();
  expect(screen.queryByLabelText('Add shared file (4 MiB maximum)')).not.toBeInTheDocument();
  expect(
    screen.queryByRole('button', { name: 'Permanently delete notes.md' }),
  ).not.toBeInTheDocument();
});
