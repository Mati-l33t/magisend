import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import Admin from './Admin';
const policy = {
  registration_open: false,
  default_quota: 1073741824,
  max_file: 1073741824,
  max_share: 2147483648,
  default_days: 7,
};
it('allows admin to create regular users and edit explicit registration policy', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () =>
        url === '/api/admin/users'
          ? []
          : {
              backend: 'local',
              reserved_bytes: 0,
              capacity_bytes: 4294967296,
              disk_free_bytes: 5000000000,
            },
    })),
  );
  render(<Admin policy={policy} reload={vi.fn(async () => {})} />);
  await screen.findByRole('heading', { name: 'Administration.' });
  fireEvent.change(screen.getByLabelText('Nytt användarnamn'), { target: { value: 'bob' } });
  fireEvent.change(screen.getByLabelText('Tillfälligt lösenord'), {
    target: { value: 'a-long-user-password' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Skapa användare' }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      '/api/admin/users',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          username: 'bob',
          password: 'a-long-user-password',
          quota: 1073741824,
        }),
      }),
    ),
  );
  fireEvent.click(screen.getByLabelText('Tillåt vanlig registrering'));
  fireEvent.click(screen.getByRole('button', { name: 'Spara policy' }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      '/api/admin/policy',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ ...policy, registration_open: true }),
      }),
    ),
  );
  expect(screen.queryByRole('combobox', { name: 'Roll' })).not.toBeInTheDocument();
});
