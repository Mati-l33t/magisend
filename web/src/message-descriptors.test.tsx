import { act, render, screen, fireEvent } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import Admin from './Admin';
import { ApiError, responseData } from './api';
import {
  message,
  LocalizedError,
  renderMessage,
  setLanguage,
  t,
  useMessage,
  type Message,
} from './i18n';
const response = (data: unknown, status = 200) =>
  ({ ok: status >= 200 && status < 300, status, json: async () => data }) as Response;
function StoredMessage({ value }: { value: Message }) {
  const [text] = useMessage(value);
  return <p role="alert">{text}</p>;
}

it('renders stored nested descriptors and API errors live while preserving Error.message and status', () => {
  setLanguage('en');
  const error = new ApiError(message('errors.invalid_login'), 401);
  const original = error.message;
  const nested = message('errors.publishRefresh', {
    error: new LocalizedError(message('errors.draftRefresh', { error })),
  });
  render(<StoredMessage value={nested} />);
  expect(screen.getByRole('alert').textContent).toBe(renderMessage(nested));
  act(() => setLanguage('sv'));
  expect(screen.getByRole('alert').textContent).toBe(
    t('errors.publishRefresh', {
      error: t('errors.draftRefresh', { error: t('errors.invalid_login') }),
    }),
  );
  expect(error.message).toBe(original);
  expect(error.status).toBe(401);
  expect(error).toBeInstanceOf(Error);
});

it('does not interpret unknown server messages or filenames as translation keys or translated text', async () => {
  setLanguage('en');
  const raw = 'Incorrect username or password';
  const failure = await responseData(
    response({ error: raw, error_code: 'future_code' }, 400),
  ).catch((error: Error) => error);
  const view = render(<StoredMessage value={failure as Error} />);
  act(() => setLanguage('sv'));
  expect(screen.getByRole('alert').textContent).toBe(raw);
  view.unmount();
  const filename = 'auth.username';
  render(<StoredMessage value={message('files.remove', { name: filename })} />);
  expect(screen.getByRole('alert').textContent).toBe(t('files.remove', { name: filename }));
  act(() => setLanguage('en'));
  expect(screen.getByRole('alert').textContent).toBe(t('files.remove', { name: filename }));
});

it('uses the current language for an admin notice when language changes during the request', async () => {
  setLanguage('en');
  let finish!: () => void;
  const pending = new Promise<void>((resolve) => {
    finish = resolve;
  });
  const fetcher = vi.fn(async (url: string) => {
    if (url === '/api/admin/policy') await pending;
    return response(
      url === '/api/admin/users'
        ? []
        : { reserved_bytes: 0, capacity_bytes: 4294967296, disk_free_bytes: 4294967296 },
    );
  });
  vi.stubGlobal('fetch', fetcher);
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
  await screen.findByText('Local disk');
  fireEvent.click(screen.getByRole('button', { name: 'Save policy' }));
  const calls = fetcher.mock.calls.length;
  act(() => setLanguage('sv'));
  expect(fetcher.mock.calls.length).toBe(calls);
  await act(async () => {
    finish();
    await pending;
  });
  expect(await screen.findByRole('status')).toHaveTextContent(t('admin.policySaved'));
  act(() => setLanguage('en'));
  expect(screen.getByRole('status')).toHaveTextContent('Policy saved.');
});
