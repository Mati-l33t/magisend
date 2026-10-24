import { test, expect } from '@playwright/test';
// Real HTTP/backend + browser on a disposable server: a token created in the
// interface uploads like ShareX does, and the returned link works.
test.use({ locale: 'en-GB', actionTimeout: 10000 });

test('API token uploads and returns a working share link', async ({ page, browser, baseURL }) => {
  const credentials = { username: 'tokenowner', password: 'disposable token passphrase' };
  const headers = { 'X-MagiSend': '1' };
  expect((await page.request.post('/api/setup', { data: credentials, headers })).status()).toBe(
    201,
  );
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  await page.goto('/send');
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'API tokens' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('New token').fill('ShareX');
  await dialog.getByRole('button', { name: 'Create token' }).click();
  const token = await dialog.getByLabel('Token', { exact: true }).inputValue();
  expect(token).toMatch(/^ms_[a-f0-9]{48}$/);
  const download = page.waitForEvent('download');
  await dialog.getByRole('button', { name: 'Download ShareX config' }).click();
  expect((await download).suggestedFilename()).toBe('MagiSend.sxcu');

  // Like ShareX: multipart form, bearer token, no browser headers.
  const form = new FormData();
  form.append('file', new Blob(['screenshot pixels'], { type: 'image/png' }), 'shot.png');
  const response = await fetch(baseURL + '/api/v1/upload?days=1', {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + token },
    body: form,
  });
  expect(response.status).toBe(201);
  const { url } = await response.json();
  const guest = await browser.newContext({ locale: 'en-GB' });
  const visitor = await guest.newPage();
  await visitor.goto(url);
  await expect(visitor.getByRole('heading', { name: 'shot.png' })).toBeVisible();
  await guest.close();

  // The list shows that the token was used.
  await dialog.getByRole('button', { name: 'Close' }).click();
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'API tokens' }).click();
  await expect(page.getByRole('dialog').getByText(/last used/)).toBeVisible();
});
