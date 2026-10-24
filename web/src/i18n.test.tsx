import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi, afterEach } from 'vitest';
import App from './App';
import Admin from './Admin';
import PublicShare from './PublicShare';
import Workspace from './Workspace';
import {
  resolveLanguage,
  translate,
  setLanguage,
  getLanguage,
  validateCredentials,
  formatDate,
  formatNumber,
} from './i18n';
import { responseData, formatBytes } from './api';
import { choose } from './test-select';
const response = (data: unknown, status = 200) =>
  ({ ok: status >= 200 && status < 300, status, json: async () => data }) as Response;
afterEach(() => vi.restoreAllMocks());
it('resolves browser priority, regions, missing languages and saved choices', () => {
  expect(resolveLanguage(['fr', 'sv-SE', 'en-US'], 'en-US', null)).toBe('sv');
  expect(resolveLanguage([], 'sv-SE', null)).toBe('sv');
  expect(resolveLanguage(['fr'], 'sv-SE', null)).toBe('sv');
  expect(resolveLanguage(undefined, undefined, null)).toBe('en');
  expect(resolveLanguage(['fr'], 'de', null)).toBe('en');
  expect(resolveLanguage(['sv'], 'sv', 'en')).toBe('en');
  expect(resolveLanguage(['sv'], 'sv', 'unsupported')).toBe('sv');
  expect(resolveLanguage(['toString', 'sv'], 'en', 'toString')).toBe('sv');
});
it('falls back per key and interpolates untrusted values as plain strings', () => {
  expect(translate('auth.username', {}, 'sv', { sv: {} })).toBe('Username');
  expect(translate('files.remove', { name: '<script>alert(1)</script>' }, 'en')).toBe(
    'Remove <script>alert(1)</script>',
  );
  const view = render(
    <p>{translate('files.remove', { name: '<script>alert(1)</script>' }, 'en')}</p>,
  );
  expect(view.container.querySelector('script')).toBeNull();
  expect(screen.getByText('Remove <script>alert(1)</script>')).toBeVisible();
});
it('language settings persist and switch live with automatic browser matching', async () => {
  vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['en-US']);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/status'
        ? response({ setup_required: false, registration_open: false })
        : response({}, 401),
    ),
  );
  render(<App />);
  await screen.findByRole('button', { name: 'Log in' });
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  choose('Language', 'Svenska');
  expect(await screen.findByRole('button', { name: 'Logga in' })).toBeVisible();
  expect(document.documentElement.lang).toBe('sv');
  expect(localStorage.getItem('magisend-language')).toBe('sv');
  choose('Språk', 'Automatiskt');
  expect(screen.getByRole('button', { name: 'Log in' })).toBeVisible();
  expect(document.documentElement.lang).toBe('en');
});
it('saved choice survives remount and storage denial does not prevent switching', async () => {
  localStorage.setItem('magisend-language', 'en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/status'
        ? response({ setup_required: false, registration_open: false })
        : response({}, 401),
    ),
  );
  const view = render(<App />);
  await screen.findByRole('button', { name: 'Log in' });
  view.unmount();
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('denied');
  });
  render(<App />);
  await screen.findByRole('button', { name: 'Log in' });
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  choose('Language', 'Svenska');
  expect(screen.getByRole('button', { name: 'Logga in' })).toBeVisible();
});
it('localizes stable API codes, preserves English unknown-code fallback and handles invalid JSON', async () => {
  setLanguage('sv');
  await expect(
    responseData(
      response({ error: 'Incorrect username or password', error_code: 'invalid_login' }, 401),
    ),
  ).rejects.toThrow('Fel användarnamn eller lösenord');
  await expect(
    responseData(response({ error: 'English fallback', error_code: 'future_code' }, 400)),
  ).rejects.toThrow('English fallback');
  await expect(responseData(response({}, 503))).rejects.toThrow(
    'Operationen misslyckades (HTTP 503)',
  );
  setLanguage('en');
  await expect(
    responseData({ ok: true, status: 200, text: async () => '{' } as Response),
  ).rejects.toThrow('The server sent an invalid or empty response');
  expect(formatBytes(1536)).toBe('1.5 KiB');
});
it('validates UTF-8 password byte limits rather than character counts', () => {
  setLanguage('en');
  expect(validateCredentials('mati', 'å'.repeat(6))).toBe('');
  expect(validateCredentials('mati', 'å'.repeat(37))).toMatch(/12.*72.*UTF-8/);
  expect(validateCredentials('mati', 'å'.repeat(5))).toMatch(/12.*72.*UTF-8/);
  expect(validateCredentials('INVALID', 'a'.repeat(12))).toMatch(/3.*40/);
});
it('formats numbers and dates with the selected locale', () => {
  const date = new Date('2026-01-02T12:00:00Z').getTime();
  setLanguage('en');
  expect(formatNumber(1234.5)).toBe('1,234.5');
  expect(formatDate(date)).toBe(new Date(date).toLocaleDateString('en'));
  setLanguage('sv');
  expect(formatBytes(1536)).toBe('1,5 KiB');
  expect(formatDate(date, true)).toBe(new Date(date).toLocaleString('sv'));
});
it('renders public and administrator pages in English', async () => {
  setLanguage('en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/admin/users'
        ? response([])
        : url === '/api/admin/storage'
          ? response({ reserved_bytes: 0, capacity_bytes: 100, disk_free_bytes: 100 })
          : response({}, 401),
    ),
  );
  const view = render(<PublicShare id={'a'.repeat(48)} />);
  expect(await screen.findByRole('button', { name: 'Open share' })).toBeVisible();
  view.unmount();
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
  expect(screen.getByRole('button', { name: 'Save policy' })).toBeVisible();
  expect(getLanguage()).toBe('en');
  await waitFor(() => expect(screen.getByText('Local disk')).toBeVisible());
});
it('switches the public page live using the global settings button', async () => {
  const id = 'a'.repeat(48);
  window.history.replaceState({}, '', '/s/' + id);
  localStorage.setItem('magisend-language', 'en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/status'
        ? response({ setup_required: false, registration_open: false })
        : response({}, 401),
    ),
  );
  try {
    render(<App />);
    await screen.findByRole('button', { name: 'Open share' });
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    choose('Language', 'Svenska');
    expect(screen.getByRole('button', { name: 'Öppna delning' })).toBeVisible();
  } finally {
    window.history.replaceState({}, '', '/');
  }
});
it('switches authenticated admin navigation without resetting the current tab', async () => {
  localStorage.setItem('magisend-language', 'en');
  const me = {
    user: { id: 'u', username: 'mati', role: 'admin', quota: 100, used: 0 },
    policy: {
      registration_open: false,
      default_quota: 100,
      max_file: 100,
      max_share: 100,
      default_days: 7,
    },
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      response(
        url === '/api/status'
          ? { setup_required: false, registration_open: false }
          : url === '/api/me'
            ? me
            : url === '/api/admin/storage'
              ? { reserved_bytes: 0, capacity_bytes: 100, disk_free_bytes: 100 }
              : [],
      ),
    ),
  );
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: 'Administration' }));
  await screen.findByRole('button', { name: 'Save policy' });
  fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
  choose('Language', 'Svenska');
  expect(screen.getByRole('button', { name: 'Spara policy' })).toBeVisible();
});
it('setup rejects overlong UTF-8 passwords without API calls and accepts twelve bytes', async () => {
  localStorage.setItem('magisend-language', 'en');
  const fetcher = vi.fn(async (url: string) =>
    url === '/api/status'
      ? response({ setup_required: true, registration_open: false })
      : response({}, 401),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('button', { name: 'Create administrator' });
  fireEvent.change(screen.getByLabelText('Setup code'), { target: { value: 'ABCD-EFGH-JKLM' } });
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'mati' } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'å'.repeat(37) } });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
  expect(screen.getByRole('alert')).toHaveTextContent('12–72 UTF-8 bytes');
  expect(fetcher.mock.calls.some(([url]) => url === '/api/setup')).toBe(false);
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'å'.repeat(6) } });
  fireEvent.change(screen.getByLabelText('Repeat password'), { target: { value: 'å'.repeat(6) } });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      '/api/setup',
      expect.objectContaining({
        body: JSON.stringify({
          username: 'mati',
          password: 'å'.repeat(6),
          setup_code: 'ABCD-EFGH-JKLM',
        }),
      }),
    ),
  );
});
it('upload rejection and administrator API errors use the active language', async () => {
  setLanguage('en');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/admin/users'
        ? response({ error: 'Log in to continue', error_code: 'login_required' }, 401)
        : response([]),
    ),
  );
  const me = {
    user: { id: 'u', username: 'mati', role: 'admin' as const, quota: 100, used: 0 },
    policy: {
      registration_open: false,
      default_quota: 100,
      max_file: 100,
      max_share: 100,
      default_days: 7,
    },
  };
  const view = render(<Workspace me={me} reload={async () => {}} />);
  fireEvent.change(screen.getByLabelText('Choose files'), {
    target: { files: [new File(['a'], 'same.txt'), new File(['b'], 'same.txt')] },
  });
  expect(screen.getByRole('alert')).toHaveTextContent('Multiple files have the same name');
  view.unmount();
  setLanguage('sv');
  render(<Admin policy={me.policy} reload={async () => {}} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Logga in för att fortsätta');
});
it('administrator account creation validates empty fields and UTF-8 lengths locally', async () => {
  setLanguage('en');
  const fetcher = vi.fn(async (url: string) =>
    response(
      url === '/api/admin/users'
        ? []
        : { reserved_bytes: 0, capacity_bytes: 100, disk_free_bytes: 100 },
    ),
  );
  vi.stubGlobal('fetch', fetcher);
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
  fireEvent.click(screen.getByRole('button', { name: 'Create user' }));
  expect(screen.getByRole('alert')).toHaveTextContent('3–40');
  fireEvent.change(screen.getByLabelText('New username'), { target: { value: 'bob' } });
  fireEvent.change(screen.getByLabelText('Temporary password'), {
    target: { value: 'å'.repeat(37) },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create user' }));
  expect(screen.getByRole('alert')).toHaveTextContent('12–72 UTF-8 bytes');
  fireEvent.change(screen.getByLabelText('Temporary password'), {
    target: { value: 'å'.repeat(6) },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create user' }));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      '/api/admin/users',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ username: 'bob', password: 'å'.repeat(6), quota: 100 }),
      }),
    ),
  );
});
