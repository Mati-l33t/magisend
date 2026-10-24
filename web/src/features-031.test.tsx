import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import Workspace, { ShareDialog } from './Workspace';
import PublicShare from './PublicShare';
import FileThumb from './FileThumb';
import { catalogs, setLanguage, t } from './i18n';
import type { Me, Share } from './api';

const me: Me = {
  user: { id: 'owner', username: 'mati', role: 'user', quota: 1073741824, used: 0 },
  policy: {
    registration_open: false,
    default_quota: 1073741824,
    max_file: 1073741824,
    max_share: 2147483648,
    default_days: 7,
  },
};
const json = (data: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  headers: new Headers({ 'Content-Type': 'application/json' }),
  text: async () => JSON.stringify(data),
});
const imageFile = {
  id: 'f'.repeat(48),
  name: 'holiday.png',
  size: 9,
  offset: 9,
  backend: 'local',
  has_thumb: true,
};
const textFile = {
  id: 'e'.repeat(48),
  name: 'notes.txt',
  size: 9,
  offset: 9,
  backend: 'local',
  has_thumb: false,
};
const owned: Share = {
  id: 'a'.repeat(48),
  title: 'Bilder',
  description: 'Två rader\nrad två',
  slug: 'bilder-2026',
  expires: 0,
  created: 1800000000,
  published: true,
  protected: false,
  files: [imageFile, textFile],
};
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('shows a real thumbnail per image in my shares and keeps the file icon when there is none', async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      calls.push(url);
      if (url === '/api/shares') return json([owned]);
      return json({});
    }),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.click(screen.getByRole('button', { name: new RegExp(t('workspace.shares')) }));
  const card = (await screen.findByRole('heading', { name: 'Bilder' })).closest('article')!;
  const images = Array.from(card.querySelectorAll('img'));
  expect(images).toHaveLength(1);
  expect(images[0]).toHaveAttribute(
    'src',
    '/api/shares/' + owned.id + '/files/' + imageFile.id + '/thumbnail',
  );
  expect(images[0]).toHaveAttribute('loading', 'lazy');
  expect(images[0]).toHaveAttribute('alt', '');
  // The text file must not trigger a thumbnail request at all.
  expect(calls.some((url) => url.includes(textFile.id))).toBe(false);
  // The text file keeps the plain icon; only its row shows one.
  const rows = card.querySelector('.owner-preview-files')!;
  expect(rows.querySelectorAll('span.file-icon')).toHaveLength(1);
  expect(rows.querySelectorAll('span.file-thumb img')).toHaveLength(1);
  fireEvent.click(rows.querySelector('button.filename-preview')!);
  await waitFor(() =>
    expect(calls.some((url) => url.endsWith('/files/' + imageFile.id + '/preview'))).toBe(true),
  );
});

it('never requests a thumbnail for files that are not raster images', async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      calls.push(url);
      if (url === '/api/shares') return json([{ ...owned, files: [textFile] }]);
      return json({});
    }),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.click(screen.getByRole('button', { name: new RegExp(t('workspace.shares')) }));
  const card = (await screen.findByRole('heading', { name: 'Bilder' })).closest('article')!;
  expect(card.querySelectorAll('img')).toHaveLength(0);
  expect(card.querySelector('span.file-thumb')).toBeNull();
  expect(calls.some((url) => url.includes('/thumbnail'))).toBe(false);
});

it('still requests a thumbnail for an image the backend has not generated yet', async () => {
  const pending = { ...imageFile, has_thumb: false };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/shares' ? json([{ ...owned, files: [pending] }]) : json({}),
    ),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.click(screen.getByRole('button', { name: new RegExp(t('workspace.shares')) }));
  const card = (await screen.findByRole('heading', { name: 'Bilder' })).closest('article')!;
  const image = card.querySelector('img')!;
  expect(image).toHaveAttribute(
    'src',
    '/api/shares/' + owned.id + '/files/' + pending.id + '/thumbnail',
  );
  // A failing or unavailable thumbnail falls back to the plain icon.
  fireEvent.error(image);
  await waitFor(() => expect(card.querySelector('img')).toBeNull());
  expect(card.querySelector('span.file-icon')).not.toBeNull();
});

it('uses the custom link, shows the description as plain text and previews images in the ready dialog', async () => {
  // has_thumb is still false right after publish: the dialog must not depend on it.
  render(
    <ShareDialog
      share={{
        ...owned,
        description: 'Rad ett\n<img src=x onerror=alert(1)>',
        files: [{ ...imageFile, has_thumb: false }, textFile],
      }}
      showQr={false}
      close={() => {}}
    />,
  );
  expect(await screen.findByRole('heading', { name: t('share.ready') })).toBeVisible();
  expect(screen.getByDisplayValue('http://localhost:3000/s/bilder-2026')).toBeVisible();
  const description = document.querySelector('.share-description')!;
  expect(description.textContent).toContain('onerror=alert(1)');
  // Injected markup stays text: no element children inside the description node.
  expect(description.children).toHaveLength(0);
  expect(description.classList.contains('share-description')).toBe(true);
  // The picture is shown large; its thumbnail stands in while it loads.
  const thumbnails = document.querySelectorAll('.ready-image img');
  expect(thumbnails).toHaveLength(1);
  expect(thumbnails[0]).toHaveAttribute(
    'src',
    '/api/shares/' + owned.id + '/files/' + imageFile.id + '/thumbnail',
  );
});

it('falls back to the generated id link when no custom link is set', async () => {
  render(<ShareDialog share={{ ...owned, slug: '' }} showQr={false} close={() => {}} />);
  expect(await screen.findByDisplayValue('http://localhost:3000/s/' + owned.id)).toBeVisible();
});

it('sends the description and a lowercased custom link when creating a share', async () => {
  let posted: Record<string, unknown> | null = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options: RequestInit) => {
      if (url === '/api/shares' && options.method === 'POST') {
        const body = JSON.parse(String(options.body)) as Record<string, unknown>;
        posted = body;
        return json({
          ...owned,
          slug: String(body.slug),
          description: String(body.description),
          published: false,
          files: [
            { id: 'f1', name: 'hello.txt', size: 5, offset: 0, backend: 'local', has_thumb: false },
          ],
        });
      }
      if (url === '/api/uploads/f1' && options.method === 'GET') return json({ offset: 0 });
      if (url === '/api/uploads/f1') return json({ offset: 5 });
      if (url.endsWith('/publish')) return json({ ...owned, published: true });
      return json([]);
    }),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText(t('upload.choose')), {
    target: { files: [new File(['hello'], 'hello.txt', { type: 'text/plain', lastModified: 1 })] },
  });
  fireEvent.change(await screen.findByLabelText(new RegExp(t('share.description'))), {
    target: { value: 'Bilder från helgen' },
  });
  fireEvent.change(screen.getByLabelText(new RegExp(t('share.customLink'))), {
    target: { value: 'HELGEN' },
  });
  fireEvent.click(screen.getByRole('button', { name: t('share.createLink') }));
  await waitFor(() => expect(posted).not.toBeNull());
  expect(posted!.description).toBe('Bilder från helgen');
  expect(posted!.slug).toBe('helgen');
  expect(posted!.title).toBe('hello.txt');
});

it('reports a taken custom link in the active language', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options: RequestInit) => {
      if (url === '/api/shares' && options.method === 'POST')
        return json({ error: 'taken', error_code: 'slug_taken' }, 409);
      return json([]);
    }),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText(t('upload.choose')), {
    target: { files: [new File(['hello'], 'hello.txt', { type: 'text/plain', lastModified: 1 })] },
  });
  fireEvent.change(await screen.findByLabelText(new RegExp(t('share.customLink'))), {
    target: { value: 'bilder' },
  });
  fireEvent.click(screen.getByRole('button', { name: t('share.createLink') }));
  expect(await screen.findByText(t('errors.slug_taken'))).toBeVisible();
  expect(catalogs.en['errors.slug_taken']).toBeDefined();
  expect(catalogs.en['errors.slug_taken']).not.toBe(catalogs.sv['errors.slug_taken']);
  expect(String(catalogs.en['errors.slug_taken'])).toMatch(/taken/i);
  expect(String(catalogs.sv['errors.slug_taken'])).toMatch(/upptaget/i);
  act(() => setLanguage('en'));
  expect(await screen.findByText(String(catalogs.en['errors.slug_taken']))).toBeVisible();
});

it('shows the description and public thumbnails on the public page', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/public/bilder-2026') return json({ ...owned, slug: 'bilder-2026' });
      return json({});
    }),
  );
  render(<PublicShare id="bilder-2026" />);
  expect(await screen.findByRole('heading', { name: 'Bilder' })).toBeVisible();
  const description = document.querySelector('.share-description')!;
  expect(description.textContent).toContain('Två rader');
  expect(description.className).toContain('share-description');
  const images = Array.from(document.querySelectorAll('.public-files img'));
  expect(images).toHaveLength(1);
  expect(images[0]).toHaveAttribute(
    'src',
    '/api/public/bilder-2026/files/' + imageFile.id + '/thumbnail',
  );
  expect(images[0]).toHaveAttribute('loading', 'lazy');
});

it('follows the file when a later refresh reports a thumbnail for a row that is already mounted', async () => {
  const plain = { ...textFile, name: 'anteckning.txt' };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/shares' ? json([{ ...owned, files: [plain] }]) : json({}),
    ),
  );
  const first = render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.click(screen.getByRole('button', { name: new RegExp(t('workspace.shares')) }));
  let card = (await screen.findByRole('heading', { name: 'Bilder' })).closest('article')!;
  expect(card.querySelector('img')).toBeNull();
  // The same row now reports a raster file with a generated thumbnail.
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/shares'
        ? json([{ ...owned, files: [{ ...plain, name: 'anteckning.png', has_thumb: true }] }])
        : json({}),
    ),
  );
  first.rerender(<Workspace me={me} reload={vi.fn(async () => {})} key="refresh" />);
  fireEvent.click(await screen.findByRole('button', { name: new RegExp(t('workspace.shares')) }));
  card = (await screen.findByRole('heading', { name: 'Bilder' })).closest('article')!;
  await waitFor(() => expect(card.querySelectorAll('img')).toHaveLength(1));
});

it('switches from icon to thumbnail when the same mounted row is told a thumbnail exists', async () => {
  // No remount and no key change: only the props move.
  const file = { ...textFile, name: 'anteckning.txt' };
  const view = render(<FileThumb shareId={owned.id} file={file} owner />);
  expect(view.container.querySelector('img')).toBeNull();
  expect(view.container.querySelector('span.file-icon')).not.toBeNull();
  view.rerender(
    <FileThumb
      shareId={owned.id}
      file={{ ...file, name: 'anteckning.png', has_thumb: true }}
      owner
    />,
  );
  await waitFor(() => expect(view.container.querySelectorAll('img')).toHaveLength(1));
  expect(view.container.querySelector('img')).toHaveAttribute(
    'src',
    '/api/shares/' + owned.id + '/files/' + file.id + '/thumbnail',
  );
});

it('previews a selected image from the browser itself before it is uploaded', async () => {
  const created: string[] = [];
  const revoked: string[] = [];
  vi.stubGlobal('URL', {
    ...URL,
    createObjectURL: (file: File) => {
      created.push(file.name);
      return 'blob:local-' + file.name;
    },
    revokeObjectURL: (url: string) => {
      revoked.push(url);
    },
  });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json([])),
  );
  render(<Workspace me={me} reload={vi.fn(async () => {})} />);
  fireEvent.change(screen.getByLabelText(t('upload.choose')), {
    target: {
      files: [
        new File(['x'], 'screenshot.jpg.png', { type: 'image/png' }),
        new File(['y'], 'note.txt', { type: 'text/plain' }),
      ],
    },
  });
  const rows = document.querySelectorAll('.selected-files .file-row');
  expect(rows).toHaveLength(2);
  const image = rows[0].querySelector('img')!;
  expect(image).toHaveAttribute('src', 'blob:local-screenshot.jpg.png');
  expect(image).toHaveAttribute('loading', 'lazy');
  // A non-raster file keeps the plain icon and never becomes an object URL.
  expect(rows[1].querySelector('img')).toBeNull();
  expect(rows[1].querySelector('span.file-icon')).not.toBeNull();
  expect(created).toEqual(['screenshot.jpg.png']);
  // Removing the row releases the preview URL.
  fireEvent.click(rows[0].querySelector('button')!);
  await waitFor(() => expect(revoked).toEqual(['blob:local-screenshot.jpg.png']));
});
