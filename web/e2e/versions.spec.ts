import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
import JSZip from 'jszip';

const headers = { 'X-MagiSend': '1' };

test('a new version replaces a file and keeps its link and history', async ({ page }) => {
  const credentials = { username: 'versions', password: 'disposable version passphrase' };
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
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  await page.goto('/library');
  await page
    .getByLabel('Upload private files')
    .setInputFiles({ name: 'plan.txt', mimeType: 'text/plain', buffer: Buffer.from('first') });
  const row = page.locator('[data-file-id]').filter({ hasText: 'plan.txt' });
  await expect(row).toBeVisible();
  const id = await row.getAttribute('data-file-id');
  const link = await (
    await page.request.post('/api/library/shares', { data: { files: [id] }, headers })
  ).json();

  await page.getByRole('button', { name: 'More actions for plan.txt' }).click();
  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: 'Upload a new version' }).click();
  await (
    await chooser
  ).setFiles({ name: 'plan-v2.txt', mimeType: 'text/plain', buffer: Buffer.from('second!') });
  await expect(page.getByRole('status')).toContainText('plan.txt now has the new version');
  await expect(page.locator('[data-file-id]')).toHaveCount(1);
  const fetched = await page.request.get(`/api/public/${link.id}/files/${id}`);
  expect(await fetched.text()).toBe('second!');

  await page.getByRole('button', { name: 'More actions for plan.txt' }).click();
  await page.getByRole('button', { name: 'Version history' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.locator('li')).toHaveCount(2);
  await expect(dialog.locator('li').first()).toContainText('plan-v2.txt');
  await expect(dialog.locator('li').first()).toContainText('Current');
  await dialog.getByRole('button', { name: 'Restore version 1' }).click();
  await expect(dialog.locator('li')).toHaveCount(3);
  expect(await (await page.request.get(`/api/public/${link.id}/files/${id}`)).text()).toBe('first');
});

test('a ZIP in the library unpacks into a new folder', async ({ page }) => {
  const credentials = { username: 'versions', password: 'disposable version passphrase' };
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  const zip = new JSZip();
  zip.file('readme.txt', 'hello');
  zip.file('photos/a.txt', 'a');
  zip.file('.DS_Store', 'x');
  await page.goto('/library');
  await page.getByLabel('Upload private files').setInputFiles({
    name: 'Trip.zip',
    mimeType: 'application/zip',
    buffer: await zip.generateAsync({ type: 'nodebuffer' }),
  });
  await page.getByRole('button', { name: 'More actions for Trip.zip' }).click();
  await page.getByRole('button', { name: 'Extract here' }).click();
  await expect(page.getByRole('status')).toContainText('Unpacked 2 files into “Trip”');
  await expect(page.getByRole('status')).toContainText('1 item was left out');
  await expect(page).toHaveURL(/\/library\/[0-9a-f]{48}$/);
  await expect(page.locator('[data-file-id]').filter({ hasText: 'readme.txt' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'photos', exact: true }).first()).toBeVisible();
});
