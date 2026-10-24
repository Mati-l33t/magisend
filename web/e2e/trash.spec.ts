import { test, expect } from '@playwright/test';
import { setupCode } from './setup';

const headers = { 'X-MagiSend': '1' };

test('deleted files wait in the trash and can be restored', async ({ page }) => {
  const credentials = { username: 'binner', password: 'disposable trash passphrase' };
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
  await page.getByLabel('Upload private files').setInputFiles([
    { name: 'keep.txt', mimeType: 'text/plain', buffer: Buffer.from('keep') },
    { name: 'bin.txt', mimeType: 'text/plain', buffer: Buffer.from('bin') },
  ]);
  const row = (name: string) => page.locator('[data-file-id]').filter({ hasText: name });
  await expect(row('bin.txt')).toBeVisible();
  await page.getByRole('button', { name: 'More actions for bin.txt' }).click();
  await page.getByRole('button', { name: 'Delete file' }).click();
  await expect(page.getByRole('dialog')).toContainText('restored there for 30 days');
  await page.getByRole('dialog').getByRole('button', { name: 'Delete file' }).click();
  await expect(row('bin.txt')).toHaveCount(0);

  const trash = page.getByRole('button', { name: /^Trash/ });
  await expect(trash).toContainText('1');
  await trash.click();
  await expect(page).toHaveURL(/\/library\/trash$/);
  await expect(row('bin.txt')).toContainText('Unfiled');
  await page.getByRole('button', { name: 'Restore bin.txt' }).click();
  await expect(page.getByText('The trash is empty')).toBeVisible();
  await expect(trash).toContainText('0');

  await page.getByRole('button', { name: /^All files/ }).click();
  await expect(row('bin.txt')).toBeVisible();

  // Deleted again, then for good.
  await page.getByRole('button', { name: 'More actions for bin.txt' }).click();
  await page.getByRole('button', { name: 'Delete file' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete file' }).click();
  await trash.click();
  await page.getByRole('button', { name: 'Empty trash' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Empty trash' }).click();
  await expect(page.getByText('The trash is empty')).toBeVisible();
  const left = await (await page.request.get('/api/library/files', { headers })).json();
  expect(left.map((f: { name: string }) => f.name)).toEqual(['keep.txt']);
});
