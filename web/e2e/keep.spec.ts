import { test, expect, type Page } from '@playwright/test';
import { choose } from './choose';

const headers = { 'X-MagiSend': '1' };

async function send(page: Page, name: string, keep: (page: Page) => Promise<void>) {
  await page.goto('/');
  await page
    .getByLabel('Choose files', { exact: true })
    .setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from('kept bytes') });
  await page.getByRole('button', { name: 'More options' }).click();
  await keep(page);
  const created = page.waitForResponse(
    (r) => r.url().endsWith('/api/shares') && r.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Create share link' }).click();
  const body = (await created).request().postDataJSON();
  await expect(page.getByText('Your link is ready')).toBeVisible();
  return body as { keep: boolean; folder_id: string; strip_location: boolean };
}

test('files sent on the Send page stay in the chosen library folder', async ({ page }) => {
  const credentials = { username: 'keeper', password: 'disposable keep passphrase' };
  expect((await page.request.post('/api/setup', { data: credentials, headers })).status()).toBe(
    201,
  );
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  const folder = await (
    await page.request.post('/api/folders', { data: { name: 'Sent' }, headers })
  ).json();
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });

  // On by default, filed into the folder picked here.
  const kept = await send(page, 'kept.txt', async (p) => {
    await expect(
      p.getByRole('switch', { name: 'Keep the files in my library after the link expires' }),
    ).toHaveAttribute('aria-checked', 'true');
    await choose(p, 'Library folder', 'Sent');
  });
  expect(kept).toMatchObject({ keep: true, folder_id: folder.id, strip_location: true });

  // Turned off: no folder, and the page says the files go with the link.
  const gone = await send(page, 'gone.txt', async (p) => {
    await p.getByRole('switch', { name: /Keep the files/ }).click();
    await expect(p.getByText('The files are deleted when the link expires')).toBeVisible();
    await expect(p.getByRole('combobox', { name: 'Library folder' })).toHaveCount(0);
  });
  expect(gone).toMatchObject({ keep: false, folder_id: '' });

  const files = await (await page.request.get('/api/library/files', { headers })).json();
  const where = Object.fromEntries(
    files.map((f: { name: string; folder_id: string }) => [f.name, f.folder_id]),
  );
  expect(where['kept.txt']).toBe(folder.id);
  expect(where['gone.txt']).toBe('');
});

test('the reminder link opens a dialog that keeps a link open longer', async ({ page }) => {
  const credentials = { username: 'keeper', password: 'disposable keep passphrase' };
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  const share = await (
    await page.request.post('/api/shares', {
      data: { title: 'Soon gone', days: 2, files: [{ name: 'a.txt', size: 1 }] },
      headers,
    })
  ).json();
  await page.request.patch(`/api/uploads/${share.files[0].id}`, {
    data: Buffer.from('a'),
    headers: {
      ...headers,
      'Upload-Offset': '0',
      'Content-Type': 'application/octet-stream',
    },
  });
  await page.request.post(`/api/shares/${share.id}/publish`, { data: {}, headers });

  await page.goto(`/shares?extend=${share.id}`);
  const dialog = page.getByRole('dialog', { name: 'Keep “Soon gone” open longer' });
  await expect(dialog).toBeVisible();
  await expect(page).toHaveURL(/\/shares$/);
  await choose(dialog, 'Open from today for', '30 days');
  await dialog.getByRole('button', { name: 'Keep open' }).click();
  await expect(dialog).toHaveCount(0);
  const shares = await (await page.request.get('/api/shares', { headers })).json();
  const updated = shares.find((s: { id: string }) => s.id === share.id);
  expect(updated.expires).toBeGreaterThan(Date.now() / 1000 + 29 * 86400);

  await page.goto('/shares?extend=gone-for-good');
  await expect(page.getByRole('alert')).toContainText('already expired');
});
