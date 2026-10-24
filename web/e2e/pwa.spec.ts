import { test, expect } from '@playwright/test';
import { setupCode } from './setup';

// Installable app: manifest, service worker, offline page and the share
// target that receives files from the phone's share sheet. Runs on its own
// disposable server (loopback counts as a secure context).
test.use({ locale: 'en-GB', extraHTTPHeaders: { 'X-MagiSend': '1' }, serviceWorkers: 'allow' });

test('installable app with offline page and share target', async ({ page, context }) => {
  const credentials = { username: 'pwaowner', password: 'disposable pwa passphrase' };
  expect(
    (
      await page.request.post('/api/setup', { data: { ...credentials, setup_code: setupCode } })
    ).status(),
  ).toBe(201);
  expect((await page.request.post('/api/login', { data: credentials })).status()).toBe(200);

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'From you. To anyone.' })).toBeVisible();
  await page.evaluate(() => navigator.serviceWorker.ready);
  const cdp = await context.newCDPSession(page);
  const manifest = await cdp.send('Page.getAppManifest');
  expect(manifest.errors).toEqual([]);
  expect(JSON.parse(manifest.data!).share_target.action).toBe('/share-target');
  const installability = await cdp.send('Page.getInstallabilityErrors');
  expect(installability.installabilityErrors).toEqual([]);

  // What the browser does when a user shares a file into the installed app.
  await page.reload();
  const redirected = await page.evaluate(async () => {
    const form = new FormData();
    form.append('files', new File(['shared from phone'], 'from-phone.txt', { type: 'text/plain' }));
    const response = await fetch('/share-target', { method: 'POST', body: form });
    return new URL(response.url).pathname + new URL(response.url).search;
  });
  expect(redirected).toBe('/?shared=1');
  await page.goto(redirected);
  await expect(page.getByText('from-phone.txt', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/send$/);

  await context.setOffline(true);
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'You are offline.' })).toBeVisible();
  await context.setOffline(false);
});
