import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi, afterEach } from 'vitest';
import CopyButton from './CopyButton';
import { setLanguage } from './i18n';

afterEach(() => vi.restoreAllMocks());

it('shows that it copied, then goes back', async () => {
  setLanguage('en');
  const writeText = vi.fn(async () => {});
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true });
  render(<CopyButton text="https://example.test/s/abc" label="Copy link" />);
  fireEvent.click(screen.getByRole('button', { name: 'Copy link' }));
  expect(await screen.findByText('Copied')).toBeTruthy();
  expect(writeText).toHaveBeenCalledWith('https://example.test/s/abc');
  expect(screen.getByRole('button').className).toContain('copied');
  await waitFor(() => expect(screen.queryByText('Copied')).toBeNull(), { timeout: 3000 });
});

it('says when copying failed', async () => {
  setLanguage('en');
  const writeText = vi.fn(async () => {
    throw new Error('denied');
  });
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true });
  const failed = vi.fn();
  render(<CopyButton text="x" label="Copy link" failed={failed} />);
  fireEvent.click(screen.getByRole('button', { name: 'Copy link' }));
  expect(await screen.findByText('Could not copy')).toBeTruthy();
  expect(failed).toHaveBeenCalled();
});
