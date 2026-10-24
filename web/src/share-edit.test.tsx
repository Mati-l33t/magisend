import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { setLanguage } from './i18n';
import { EditShareDialog } from './ShareEdit';
import { choose } from './test-select';
import type { Share } from './api';

const share: Share = {
  id: 'a'.repeat(48),
  title: 'Holiday photos',
  description: '',
  slug: '',
  expires: Math.floor(Date.now() / 1000) + 7 * 86400,
  created: 0,
  published: true,
  protected: true,
  files: [],
  download_limit: 0,
};

it('sends only the changed fields and reports the saved share', async () => {
  setLanguage('en', false);
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.setAttribute('open', '');
  };
  const fetcher = vi.fn(
    async (_path: string, options: RequestInit) =>
      new Response(JSON.stringify({ ...share, ...JSON.parse(String(options.body)) }), {
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetcher);
  const saved = vi.fn();
  const close = vi.fn();
  render(<EditShareDialog share={share} close={close} saved={saved} />);
  fireEvent.change(screen.getByLabelText('Share name'), { target: { value: 'Trip' } });
  choose('Validity period', 'Never expires');
  choose('Downloads per file', 'Once');
  fireEvent.click(screen.getByLabelText('Remove the password'));
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  const [path, options] = fetcher.mock.calls[0];
  expect(path).toBe('/api/shares/' + share.id);
  expect(options.method).toBe('PATCH');
  expect(JSON.parse(String(options.body))).toEqual({
    title: 'Trip',
    days: 0,
    permanent: true,
    download_limit: 1,
    password: '',
  });
  expect(close).toHaveBeenCalled();
});
