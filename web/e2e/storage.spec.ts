import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
import { mkdtemp, readdir, rename } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
// Real HTTP/backend + browser on a disposable server: a folder standing in for
// a mounted share is added, holds new files, goes offline without taking the
// app down, comes back, and its files are moved home again.
test.use({ locale: 'en-GB', actionTimeout: 15000 });

const shots = process.env.STORAGE_SHOTS;

test('storage locations: add, use, offline, move back, remove', async ({ page }) => {
  test.setTimeout(120000);
  const credentials = { username: 'keeper', password: 'disposable storage passphrase' };
  const headers = { 'X-MagiSend': '1' };
  expect(
    (
      await page.request.post('/api/setup', {
        data: { ...credentials, setup_code: setupCode },
        headers,
      })
    ).status(),
  ).toBe(201);
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  const share = await mkdtemp(join(tmpdir(), 'magisend-share-'));
  const files = join(share, 'magisend-files');
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/admin');
  const panel = page.locator('section.storage-locations');
  await panel.getByLabel('Add a folder').fill(share);
  await panel.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(panel.getByText('Folder added.')).toBeVisible();
  const row = panel.locator('li', { hasText: share });
  await row.getByRole('button', { name: 'Use for new files' }).click();
  await expect(row.getByText('New files')).toBeVisible();
  if (shots) await panel.screenshot({ path: shots + '/storage-1280.png' });

  await page.goto('/library');
  await page
    .getByLabel('Upload private files')
    .setInputFiles([
      { name: 'kept.txt', mimeType: 'text/plain', buffer: Buffer.from('kept on the share') },
    ]);
  await expect(page.getByRole('checkbox', { name: 'Select kept.txt' })).toBeVisible();
  await expect
    .poll(async () => (await readdir(files)).filter((n) => !n.startsWith('.')).length)
    .toBe(1);

  // The share goes away: the app keeps running and says what is wrong.
  const away = files + '-away';
  await rename(files, away);
  await page.goto('/admin');
  await expect
    .poll(
      async () => {
        await page.reload();
        return page.getByText(/Storage offline:/).count();
      },
      { timeout: 20000, intervals: [2000] },
    )
    .toBe(1);
  await expect(panel.locator('li', { hasText: share }).getByText('Offline')).toBeVisible();
  const download = await page.request.get('/api/library/files', { headers });
  expect(download.status()).toBe(200);

  // Back again, the files are moved home and the folder is removed.
  await rename(away, files);
  // Asks the server, not the page: an overview still loading also shows no warning.
  await expect
    .poll(
      async () => {
        const status = await (await page.request.get('/api/admin/storage', { headers })).json();
        return status.locations.find((l: { path: string }) => l.path === share)?.online;
      },
      { timeout: 20000, intervals: [1000] },
    )
    .toBe(true);
  await page.reload();
  await expect(page.getByText(/Storage offline:/)).toHaveCount(0);
  const home = panel.locator('li', { hasText: 'This server' });
  await home.getByRole('button', { name: 'Use for new files' }).click();
  await expect(home.getByText('New files')).toBeVisible();
  await home.getByRole('button', { name: 'Move all files here' }).click();
  await expect(panel.getByText('1 file moved and checked.')).toBeVisible({ timeout: 20000 });
  expect((await readdir(files)).filter((n) => !n.startsWith('.'))).toEqual([]);
  await panel.getByRole('button', { name: 'Remove ' + share }).click();
  await expect(panel.getByText('The folder was removed from the list.')).toBeVisible();

  await page.goto('/library');
  const link = await page.getByRole('link', { name: 'Download kept.txt' }).getAttribute('href');
  expect(await (await page.request.get(link!)).text()).toBe('kept on the share');

  await page.setViewportSize({ width: 412, height: 900 });
  await page.goto('/admin');
  await expect(panel).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(412);
  if (shots) await panel.screenshot({ path: shots + '/storage-412.png' });
});
