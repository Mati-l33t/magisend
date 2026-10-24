import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import App from './App';
import { choose } from './test-select';

const response = (data: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => data,
});
it('requires an exact repeated password before first-administrator creation', async () => {
  localStorage.setItem('magisend-language', 'en');
  const fetcher = vi.fn(async (url: string) =>
    url === '/api/status'
      ? response({ setup_required: true, registration_open: false })
      : response({}, 401),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('button', { name: 'Create administrator' });
  fireEvent.change(screen.getByLabelText('Username', { exact: true }), {
    target: { value: 'owner' },
  });
  fireEvent.change(screen.getByLabelText('Password', { exact: true }), {
    target: { value: 'test-only-long-password' },
  });
  const repeat = screen.getByLabelText('Repeat password', { exact: true });
  expect(repeat).toHaveAttribute('type', 'password');
  expect(repeat).toHaveAttribute('autocomplete', 'new-password');
  expect(repeat).toBeRequired();
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
  expect(screen.getByRole('alert')).toHaveTextContent('Passwords do not match.');
  fireEvent.change(repeat, { target: { value: 'test-only-long-password ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
  expect(screen.getByRole('alert')).toHaveTextContent('Passwords do not match.');
  expect(fetcher.mock.calls.some(([url]) => url === '/api/setup' || url === '/api/login')).toBe(
    false,
  );
});

for (const locale of ['en', 'sv'])
  for (const setup of [true, false]) {
    it(`${locale} ${setup ? 'setup' : 'registration'} submits only matching credentials and clears confirmation before login retry`, async () => {
      localStorage.setItem('magisend-language', locale);
      const labels =
        locale === 'en'
          ? {
              name: 'Username',
              password: 'Password',
              repeat: 'Repeat password',
              create: setup ? 'Create administrator' : 'Create account',
              login: 'Log in',
              mismatch: 'Passwords do not match.',
            }
          : {
              name: 'Användarnamn',
              password: 'Lösenord',
              repeat: 'Upprepa lösenord',
              create: setup ? 'Skapa administratör' : 'Skapa konto',
              login: 'Logga in',
              mismatch: 'Lösenorden stämmer inte överens.',
            };
      let created = false;
      const endpoint = setup ? '/api/setup' : '/api/register';
      const fetcher = vi.fn(async (url: string, _options?: RequestInit) => {
        if (url === '/api/status')
          return response({ setup_required: setup && !created, registration_open: !setup });
        if (url === endpoint) {
          created = true;
          return response({});
        }
        return response({ error: 'Retry login' }, url === '/api/me' ? 401 : 503);
      });
      vi.stubGlobal('fetch', fetcher);
      render(<App />);
      await screen.findByRole('button', { name: setup ? labels.create : labels.login });
      if (!setup) {
        expect(screen.queryByLabelText(labels.repeat, { exact: true })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: labels.create }));
      }
      fireEvent.change(screen.getByLabelText(labels.name, { exact: true }), {
        target: { value: 'new.owner' },
      });
      const password = 'ångström-test-only-password';
      fireEvent.change(screen.getByLabelText(labels.password, { exact: true }), {
        target: { value: password },
      });
      fireEvent.change(screen.getByLabelText(labels.repeat, { exact: true }), {
        target: { value: password + ' ' },
      });
      fireEvent.click(screen.getByRole('button', { name: labels.create }));
      expect(screen.getByRole('alert')).toHaveTextContent(labels.mismatch);
      expect(fetcher.mock.calls.some(([url]) => url === endpoint || url === '/api/login')).toBe(
        false,
      );
      fireEvent.change(screen.getByLabelText(labels.repeat, { exact: true }), {
        target: { value: password },
      });
      fireEvent.click(screen.getByRole('button', { name: labels.create }));
      await waitFor(() =>
        expect(fetcher).toHaveBeenCalledWith(
          endpoint,
          expect.objectContaining({
            method: 'POST',
            body: JSON.stringify({ username: 'new.owner', password }),
          }),
        ),
      );
      await screen.findByText('Retry login');
      expect(screen.queryByLabelText(labels.repeat, { exact: true })).not.toBeInTheDocument();
      expect(screen.getByLabelText(labels.password, { exact: true })).toHaveValue('');
      expect(fetcher.mock.calls.filter(([url]) => url === endpoint)).toHaveLength(1);
      fireEvent.change(screen.getByLabelText(labels.password, { exact: true }), {
        target: { value: password },
      });
      fireEvent.click(screen.getByRole('button', { name: labels.login }));
      await waitFor(() =>
        expect(fetcher.mock.calls.filter(([url]) => url === '/api/login')).toHaveLength(2),
      );
      expect(fetcher.mock.calls.filter(([url]) => url === endpoint)).toHaveLength(1);
    });
  }

it('clears both password fields on registration/login mode changes', async () => {
  localStorage.setItem('magisend-language', 'en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/status'
        ? response({ setup_required: false, registration_open: true })
        : response({}, 401),
    ),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Create account' }));
  fireEvent.change(screen.getByLabelText('Password', { exact: true }), {
    target: { value: 'test-only-password' },
  });
  fireEvent.change(screen.getByLabelText('Repeat password', { exact: true }), {
    target: { value: 'test-only-password' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Back to login' }));
  expect(screen.getByLabelText('Password', { exact: true })).toHaveValue('');
  expect(screen.queryByLabelText('Repeat password', { exact: true })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
  expect(screen.getByLabelText('Password', { exact: true })).toHaveValue('');
  expect(screen.getByLabelText('Repeat password', { exact: true })).toHaveValue('');
});

it('retranslates a visible mismatch error without submitting another request', async () => {
  localStorage.setItem('magisend-language', 'en');
  const fetcher = vi.fn(async (url: string) =>
    url === '/api/status'
      ? response({ setup_required: true, registration_open: false })
      : response({}, 401),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('button', { name: 'Create administrator' });
  fireEvent.change(screen.getByLabelText('Username', { exact: true }), {
    target: { value: 'owner' },
  });
  fireEvent.change(screen.getByLabelText('Password', { exact: true }), {
    target: { value: 'test-only-password' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
  expect(screen.getByRole('alert')).toHaveTextContent('Passwords do not match.');
  const calls = fetcher.mock.calls.length;
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  choose('Language', 'Svenska');
  expect(screen.getByRole('alert')).toHaveTextContent('Lösenorden stämmer inte överens.');
  expect(screen.getByLabelText('Upprepa lösenord', { exact: true })).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(calls);
});
