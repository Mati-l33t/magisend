import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
// Real HTTP/backend + browser on a disposable server: an owner creates an
// upload link, a signed-out visitor uploads through it, the owner receives.
test.use({ locale: 'en-GB', actionTimeout: 10000 });

test('upload request delivers a visitor upload into the owner library', async ({
  page,
  browser,
}) => {
  const credentials = { username: 'requester', password: 'disposable request passphrase' };
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

  await page.goto('/requests');
  await page.getByRole('button', { name: 'New upload request' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Name', { exact: true }).fill('Wedding photos');
  await dialog.getByLabel(/Message to uploaders/).fill('Your best shots, please');
  await dialog.getByLabel(/^Password/).fill('guests-only-password');
  await dialog.getByRole('button', { name: 'Create upload link' }).click();
  const link = await page.getByRole('dialog').getByLabel('Upload link').inputValue();
  expect(link).toMatch(/\/r\/[a-f0-9]{48}$/);
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).last().click();
  await expect(page.getByText('Wedding photos', { exact: true })).toBeVisible();

  const guest = await browser.newContext({ locale: 'en-GB' });
  const visitor = await guest.newPage();
  await visitor.goto(link);
  await expect(visitor.getByRole('heading', { name: 'Wedding photos' })).toBeVisible();
  await visitor.getByLabel('Password').fill('wrong-password-here');
  await visitor.getByRole('button', { name: 'Open share' }).click();
  await expect(visitor.getByRole('alert')).toBeVisible();
  await visitor.getByLabel('Password').fill('guests-only-password');
  await visitor.getByRole('button', { name: 'Open share' }).click();
  await expect(visitor.getByText('Your best shots, please')).toBeVisible();
  await visitor.getByLabel('Choose files').setInputFiles([
    { name: 'first-dance.txt', mimeType: 'text/plain', buffer: Buffer.from('a lovely dance') },
    { name: 'cake.txt', mimeType: 'text/plain', buffer: Buffer.from('cake!') },
  ]);
  await visitor.getByLabel(/Your name/).fill('Anna');
  await visitor.getByRole('button', { name: 'Send 2 files' }).click();
  await expect(visitor.getByText('Done. 2 files delivered to requester.')).toBeVisible();
  // The visitor never sees anything of the owner's library.
  expect(await (await visitor.request.get('/api/library/files')).status()).toBe(401);
  await guest.close();

  await page.reload();
  await expect(page.getByText('2 files · 19 B')).toBeVisible();
  // The files themselves land in the library.
  await page.goto('/library');
  await expect(page.getByText('first-dance.txt', { exact: true })).toBeVisible();
  await expect(page.getByText(/from Anna/).first()).toBeVisible();
  const download = page.waitForEvent('download');
  await page.getByRole('link', { name: 'Download first-dance.txt' }).click();
  expect(await (await download).failure()).toBeNull();
});
