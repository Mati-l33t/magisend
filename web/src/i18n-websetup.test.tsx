import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi, afterEach } from 'vitest';
import App from './App';
const response = (data: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => data,
});
afterEach(() => vi.restoreAllMocks());
it('browser English provides tokenless first administrator registration', async () => {
  vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['en-US']);
  vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-US');
  let created = false;
  const fetcher = vi.fn(async (url: string) =>
    url === '/api/status'
      ? response({ setup_required: !created, registration_open: false })
      : url === '/api/setup'
        ? ((created = true), response({}))
        : response({ error: 'Login unavailable' }, url === '/api/me' ? 401 : 503),
  );
  vi.stubGlobal('fetch', fetcher);
  render(<App />);
  await screen.findByRole('button', { name: 'Create administrator' });
  expect(screen.queryByText(/LXC|setup-token|Installationsnyckel/)).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'mati' } });
  fireEvent.change(screen.getByLabelText('Password'), {
    target: { value: 'a-long-test-password' },
  });
  fireEvent.change(screen.getByLabelText('Repeat password'), {
    target: { value: 'a-long-test-password' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      '/api/setup',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ username: 'mati', password: 'a-long-test-password' }),
      }),
    ),
  );
  await screen.findByRole('button', { name: 'Log in' });
  expect(screen.queryByRole('button', { name: 'Create administrator' })).not.toBeInTheDocument();
});
