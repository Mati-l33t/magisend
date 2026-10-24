import { test, expect } from '@playwright/test';
// Real HTTP/backend + browser on a disposable server: the administration page
// jumps between sections, finds a setting by name and offers a way back up.
test.use({ locale: 'en-GB' });

test('admin sections, settings search and back to top', async ({ page }) => {
  const credentials = { username: 'keeper', password: 'disposable layout passphrase' };
  const headers = { 'X-MagiSend': '1' };
  expect((await page.request.post('/api/setup', { data: credentials, headers })).status()).toBe(
    201,
  );
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/admin');
  const nav = page.getByRole('navigation', { name: 'Sections' });
  await nav.getByRole('link', { name: 'Branding' }).click();
  await expect(page.getByLabel('Name', { exact: true })).toBeInViewport();
  await expect(nav.getByRole('link', { name: 'Branding' })).toHaveAttribute('aria-current', 'true');

  await page.getByRole('button', { name: 'Back to top' }).click();
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(0);
  await expect(page.getByRole('button', { name: 'Back to top' })).toBeHidden();

  const search = page.getByLabel('Search settings');
  await search.fill('largest single file');
  await expect(page.getByLabel('Max file size (GiB)')).toBeVisible();
  await expect(page.getByLabel('Default validity (days)')).toBeHidden();
  await expect(page.locator('#admin-branding')).toBeHidden();
  await expect(nav.getByRole('link')).toHaveText(['Limits']);
  await search.fill('nothing like this');
  await expect(page.getByText('No settings match your search.')).toBeVisible();
  await search.press('Escape');
  await expect(page.getByLabel('Default validity (days)')).toBeVisible();
  await expect(nav.getByRole('link')).toHaveCount(12);
});
