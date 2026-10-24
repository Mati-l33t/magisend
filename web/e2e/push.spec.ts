import { test, expect } from '@playwright/test';
import { choose } from './choose';

const headers = { 'X-MagiSend': '1' };

test('push channels are added, listed and removed; admins choose local addresses', async ({
  page,
}) => {
  const credentials = { username: 'pusher', password: 'disposable push passphrase' };
  expect((await page.request.post('/api/setup', { data: credentials, headers })).status()).toBe(
    201,
  );
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'Push notifications' }).click();
  const dialog = page.getByRole('dialog', { name: 'Push notifications' });
  await expect(dialog).toContainText('No channels yet.');
  await expect(dialog).toContainText('only sends to addresses on the internet');
  await choose(dialog, 'Service', 'Discord');
  await dialog.getByLabel('Address').fill('https://example.com/not-discord');
  await dialog.getByRole('button', { name: 'Add channel' }).click();
  await expect(dialog.getByRole('alert')).toContainText('Check the address');
  await dialog.getByLabel('Address').fill('https://discord.com/api/webhooks/1/abc');
  await dialog
    .getByLabel('a link is downloaded (at most once per link every 15 minutes)')
    .uncheck();
  await dialog.getByRole('button', { name: 'Add channel' }).click();
  await expect(dialog.locator('.push-list li')).toHaveCount(1);
  await expect(dialog.locator('.push-list li')).not.toContainText('downloaded');
  await dialog.getByRole('button', { name: 'Remove Discord' }).click();
  await expect(dialog).toContainText('No channels yet.');
  await dialog.getByRole('button', { name: 'Close' }).last().click();

  await page.goto('/admin');
  const local = page.getByLabel('Allow local network addresses');
  await expect(local).not.toBeChecked();
  await local.check();
  await expect(local).toBeChecked();
  const saved = await (await page.request.get('/api/admin/push', { headers })).json();
  expect(saved.allow_local).toBe(true);

  // A backup made from the same page appears in its list.
  const backups = page.locator('#admin-backups');
  await expect(backups.getByText('No backups yet.')).toBeVisible();
  await backups.getByRole('button', { name: 'Back up now' }).click();
  await expect(backups.getByRole('link', { name: /^Download magisend-backup-/ })).toBeVisible({
    timeout: 15000,
  });
  await expect(backups).toContainText('Backups (1)');
});
