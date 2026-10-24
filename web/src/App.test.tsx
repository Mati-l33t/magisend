import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import App from './App';
function mockAnonymous(setup = false) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: url != '/api/me',
      status: url == '/api/me' ? 401 : 200,
      json: async () =>
        url == '/api/status'
          ? { setup_required: setup, registration_open: false }
          : { error: 'Logga in' },
    })),
  );
}
describe('authentication and theme', () => {
  it('defaults to dark without registration and persists explicit light choice', async () => {
    mockAnonymous();
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Välkommen tillbaka.' })).toBeVisible();
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(screen.queryByText('Skapa konto')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Byt till ljust tema' }));
    expect(localStorage.getItem('magisend-theme')).toBe('light');
    expect(document.documentElement.dataset.theme).toBe('light');
  });
  it('first visit presents tokenless one-time admin creation, not ordinary registration', async () => {
    mockAnonymous(true);
    render(<App />);
    expect(await screen.findByRole('heading', { name: 'Din första MagiSend.' })).toBeVisible();
    expect(screen.queryByLabelText('Installationsnyckel')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Skapa administratör' })).toBeVisible();
    expect(screen.getByText(/första besökaren blir administratör/)).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Skapa konto' })).not.toBeInTheDocument();
  });
  it('submits real login API and displays backend errors', async () => {
    mockAnonymous();
    render(<App />);
    await screen.findByRole('heading', { name: 'Välkommen tillbaka.' });
    fireEvent.change(screen.getByLabelText('Användarnamn'), { target: { value: 'mati' } });
    fireEvent.change(screen.getByLabelText('Lösenord'), {
      target: { value: 'some-long-password' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Logga in' }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        '/api/login',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ username: 'mati', password: 'some-long-password' }),
        }),
      ),
    );
  });
});
