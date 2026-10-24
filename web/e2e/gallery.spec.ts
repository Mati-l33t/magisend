import { test, expect } from '@playwright/test';
import { setupCode } from './setup';

const headers = { 'X-MagiSend': '1' };
// A 1×1 PNG.
const png = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
);

test('the viewer moves between files and plays a slideshow of pictures', async ({ page }) => {
  const credentials = { username: 'gallery', password: 'disposable gallery passphrase' };
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
    localStorage.setItem('magisend-look', 'classic');
  });
  await page.goto('/library');
  await page.getByLabel('Upload private files').setInputFiles([
    { name: 'a.png', mimeType: 'image/png', buffer: png },
    { name: 'b.png', mimeType: 'image/png', buffer: png },
    { name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') },
    { name: 'data.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('x') },
  ]);
  await expect(page.locator('[data-file-id]')).toHaveCount(4);
  const ids = await page
    .locator('[data-file-id]')
    .evaluateAll((rows) => rows.map((r) => r.getAttribute('data-file-id')));
  const link = await (
    await page.request.post('/api/library/shares', { data: { files: ids }, headers })
  ).json();

  await page.goto('/s/' + link.id);
  await page.getByRole('button', { name: 'a.png' }).first().click();
  const viewer = page.getByRole('dialog');
  // data.bin cannot be shown, so it is not part of the round.
  await expect(viewer.locator('.preview-count')).toHaveText('1 of 3');
  await expect(viewer.getByRole('img', { name: 'a.png' })).toBeVisible();
  await page.keyboard.press('ArrowRight');
  await expect(viewer.getByRole('heading')).toContainText('b.png');
  await viewer.getByRole('button', { name: 'Next file' }).click();
  await expect(viewer.getByRole('heading')).toContainText('notes.txt');
  await expect(viewer.getByText('hello')).toBeVisible();
  await expect(viewer.getByRole('button', { name: 'Play slideshow' })).toHaveCount(0);
  await viewer.getByRole('button', { name: 'Next file' }).click();
  await expect(viewer.getByRole('heading')).toContainText('a.png');

  // The slideshow goes on to the next picture by itself.
  await viewer.getByRole('button', { name: 'Play slideshow' }).click();
  await expect(viewer.getByRole('heading')).toContainText('b.png', { timeout: 8000 });
  await viewer.getByRole('button', { name: 'Pause slideshow' }).click();
  await page.keyboard.press('Escape');
  await expect(viewer).toHaveCount(0);
});
