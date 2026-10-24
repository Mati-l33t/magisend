import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import Workspace from './Workspace';
import { setLanguage } from './i18n';
import type { Me } from './api';
const me: Me = {
  user: { id: 'u', username: 'owner', role: 'admin', quota: 1073741824, used: 0 },
  policy: {
    registration_open: false,
    default_quota: 1073741824,
    max_file: 1073741824,
    max_share: 2147483648,
    default_days: 7,
  },
};
it('library lists uploaded files and sharing starts read-only with independent opt-ins', async () => {
  setLanguage('en', false);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () =>
        url === '/api/capabilities'
          ? { folders: true, upload: true, share: true, edit_own: true, grant_edit: true }
          : url === '/api/folders'
            ? [{ id: 'folder', name: 'Project' }]
            : url === '/api/library/files'
              ? [
                  {
                    id: 'file',
                    name: 'notes.md',
                    size: 4,
                    offset: 4,
                    backend: 'local',
                    folder_id: 'folder',
                    share_id: 'container',
                  },
                ]
              : [],
    })),
  );
  render(<Workspace me={me} reload={async () => {}} />);
  fireEvent.click(screen.getByRole('button', { name: 'File library' }));
  await screen.findByText('notes.md');
  fireEvent.click(screen.getByLabelText('Select notes.md'));
  fireEvent.click(screen.getByRole('button', { name: 'Share selected files' }));
  expect(screen.getByText(/selection keeps the same file membership/i)).toBeVisible();
  for (const name of ['Allow text edits', 'Allow adding files', 'Allow deleting files'])
    expect(screen.getByLabelText(name)).not.toBeChecked();
  expect(screen.getByLabelText('Recipient usernames')).toBeVisible();
  expect(screen.getByRole('combobox', { name: 'Validity period' })).toHaveTextContent('7 days');
});
it('text editor preserves unsaved text after a stale-write conflict instead of retrying overwrite', async () => {
  setLanguage('en', false);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options: RequestInit) => {
      const data =
        url === '/api/capabilities'
          ? { folders: true, upload: true, share: true, edit_own: true }
          : url === '/api/folders'
            ? []
            : url === '/api/library/files'
              ? [
                  {
                    id: 'file',
                    name: 'notes.txt',
                    size: 4,
                    offset: 4,
                    backend: 'local',
                    folder_id: '',
                    share_id: 'container',
                  },
                ]
              : url.endsWith('/text')
                ? options.method === 'PUT'
                  ? { error: 'File changed. Reload before saving.', error_code: 'text_conflict' }
                  : { text: 'old', version: 1 }
                : [];
      return {
        ok: !(url.endsWith('/text') && options.method === 'PUT'),
        status: url.endsWith('/text') && options.method === 'PUT' ? 409 : 200,
        json: async () => data,
      };
    }),
  );
  render(<Workspace me={me} reload={async () => {}} />);
  fireEvent.click(screen.getByRole('button', { name: 'File library' }));
  await screen.findByText('notes.txt');
  fireEvent.click(screen.getByRole('button', { name: 'Edit notes.txt' }));
  const editor = await screen.findByLabelText('Plain text');
  fireEvent.change(editor, { target: { value: 'unsaved' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save text' }));
  await screen.findByText('File changed. Reload before saving.');
  expect(editor).toHaveValue('unsaved');
  await waitFor(() =>
    expect(
      vi
        .mocked(fetch)
        .mock.calls.filter(
          ([url, options]) => String(url).endsWith('/text') && options?.method === 'PUT',
        ),
    ).toHaveLength(1),
  );
});
it('a folder counts the files in its subfolders', async () => {
  setLanguage('en', false);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () =>
        url === '/api/capabilities'
          ? { folders: true, upload: true, share: true }
          : url === '/api/folders'
            ? [
                { id: 'work', name: 'Work' },
                { id: 'inner', name: 'Inner', parent_id: 'work' },
              ]
            : url === '/api/library/files'
              ? [
                  {
                    id: 'file',
                    name: 'plan.txt',
                    size: 4,
                    offset: 4,
                    backend: 'local',
                    folder_id: 'inner',
                    share_id: 'container',
                  },
                ]
              : [],
    })),
  );
  render(<Workspace me={me} reload={async () => {}} />);
  fireEvent.click(screen.getByRole('button', { name: 'File library' }));
  expect(await screen.findByText('1 folder · 1 file')).toBeVisible();
  const work = screen.getAllByRole('button', { name: /^Work/ }).find((b) => b.closest('nav'));
  expect(work?.querySelector('.count')).toHaveTextContent('1');
});
