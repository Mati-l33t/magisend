import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
import { createHmac } from 'node:crypto';
// Real HTTP/backend + browser on a disposable server.
test.use({ locale: 'en-GB', actionTimeout: 10000 });

function base32(text: string) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of text.replace(/\s/g, '')) bits += alphabet.indexOf(c).toString(2).padStart(5, '0');
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(bytes);
}
function code(secret: string, step: number) {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(step));
  const sum = createHmac('sha1', base32(secret)).update(counter).digest();
  const offset = sum[sum.length - 1] & 15;
  return String((sum.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).padStart(6, '0');
}

test('two-step login can be turned on and is required at sign-in', async ({ page }) => {
  const credentials = { username: 'secured', password: 'disposable two step passphrase' };
  const headers = { 'X-MagiSend': '1' };
  expect(
    (
      await page.request.post('/api/setup', {
        data: { ...credentials, setup_code: setupCode },
        headers,
      })
    ).status(),
  ).toBe(201);
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill(credentials.username);
  await page.getByLabel('Password', { exact: true }).fill(credentials.password);
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'Two-step login' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Current password').fill(credentials.password);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await expect(
    dialog.getByRole('img', { name: 'QR code for the authenticator app' }),
  ).toBeVisible();
  await dialog.getByText("Can't scan? Enter this key instead").click();
  const secret = (await dialog.locator('.secret').textContent())!;
  const step = Math.floor(Date.now() / 30000);
  await dialog.getByLabel('6-digit code').fill(code(secret, step));
  await dialog.getByRole('button', { name: 'Turn on' }).click();
  await expect(dialog.getByText('Two-step login is on.')).toBeVisible();
  const recovery = (await dialog.locator('.recovery-codes li').first().textContent())!;
  await dialog.getByRole('button', { name: 'I saved them' }).click();
  await expect(dialog.getByText('10 unused recovery codes left.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Close' }).click();

  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'Log out' }).click();
  await page.getByLabel('Username', { exact: true }).fill(credentials.username);
  await page.getByLabel('Password', { exact: true }).fill(credentials.password);
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  const field = page.getByLabel('Authentication code');
  await expect(field).toBeVisible();
  await field.fill('000000');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('wrong or has already been used');
  await field.fill(recovery);
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'From you. To anyone.' })).toBeVisible();
});
