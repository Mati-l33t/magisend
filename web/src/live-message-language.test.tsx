import { render, screen, fireEvent } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import App from './App';
import Admin from './Admin';
import { setLanguage } from './i18n';
import { choose } from './test-select';
const response = (data: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => data,
});
it('an already displayed API error changes language without another request', async () => {
  localStorage.setItem('magisend-language', 'en');
  const fetcher = vi.fn(async (url: string) =>
    url === '/api/status'
      ? response({ setup_required: false, registration_open: false })
      : url === '/api/login'
        ? response({ error: 'Incorrect username or password', error_code: 'invalid_login' }, 401)
        : response({}, 401),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('button', { name: 'Log in' });
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'mati' } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'incorrect-password' } });
  fireEvent.click(screen.getByRole('button', { name: 'Log in' }));
  await screen.findByText('Incorrect username or password');
  const calls = fetcher.mock.calls.length;
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  choose('Language', 'Svenska');
  expect(screen.getByRole('alert')).toHaveTextContent('Fel användarnamn eller lösenord');
  expect(fetcher.mock.calls.length).toBe(calls);
});
it('an already displayed administrator success notice changes language', async () => {
  setLanguage('en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      response(
        url === '/api/admin/users'
          ? []
          : { reserved_bytes: 0, capacity_bytes: 100, disk_free_bytes: 100 },
      ),
    ),
  );
  render(
    <Admin
      policy={{
        registration_open: false,
        default_quota: 1073741824,
        max_file: 1073741824,
        max_share: 2147483648,
        default_days: 7,
      }}
      reload={async () => {}}
    />,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Save policy' }));
  await screen.findByText('Policy saved.');
  // act via a synchronous event on the document to mirror an external-store update.
  const button = document.createElement('button');
  button.onclick = () => setLanguage('sv');
  document.body.append(button);
  fireEvent.click(button);
  button.remove();
  expect(screen.getByRole('status')).toHaveTextContent('Policyn är sparad.');
});
it('stored local validation errors switch language too', async () => {
  localStorage.setItem('magisend-language', 'en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/status'
        ? response({ setup_required: true, registration_open: false })
        : response({}, 401),
    ),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Create administrator' }));
  expect(screen.getByRole('alert')).toHaveTextContent('Use 3–40');
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  choose('Language', 'Svenska');
  expect(screen.getByRole('alert')).toHaveTextContent('Använd 3–40');
});
