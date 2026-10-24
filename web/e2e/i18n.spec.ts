import { test, expect } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { choose } from './choose';

test('English browser, Swedish live settings and persisted choice on mobile', async ({
  browser,
}) => {
  const context = await browser.newContext({
    locale: 'en-US',
    viewport: { width: 390, height: 844 },
  });
  const page = await context.newPage();
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto('/');
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await choose(page, 'Language', 'Svenska');
  await expect(page.locator('html')).toHaveAttribute('lang', 'sv');
  await page.getByRole('button', { name: 'Stäng', exact: true }).click();
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang', 'sv');
  await page.getByRole('button', { name: 'Inställningar', exact: true }).click();
  await expect(page.getByRole('combobox', { name: 'Språk', exact: true })).toHaveText('Svenska');
  await choose(page, 'Språk', 'Automatiskt');
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page.getByRole('combobox', { name: 'Language', exact: true })).toHaveText(
    'Automatic',
  );
  await choose(page, 'Language', 'English');
  await expect(page.getByRole('combobox', { name: 'Language', exact: true })).toHaveText('English');
  await choose(page, 'Language', 'Automatic');
  await expect(page.getByRole('combobox', { name: 'Language', exact: true })).toHaveText(
    'Automatic',
  );
  await mkdir('test-results/screens', { recursive: true });
  await page.screenshot({
    path: 'test-results/screens/language-settings-mobile.png',
    fullPage: true,
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(errors).toEqual([]);
  await context.close();
});

for (const [name, languages, single, expected] of [
  ['unsupported browser', ['de-DE'], 'de-DE', 'en'],
  ['missing browser language', [], undefined, 'en'],
  ['first supported regional preference', ['fr-FR', 'sv-SE', 'en-US'], 'fr-FR', 'sv'],
] as const) {
  test(name + ' uses ' + expected, async ({ browser }) => {
    const context = await browser.newContext();
    await context.addInitScript(
      ({ languages, single }) => {
        Object.defineProperty(navigator, 'languages', { get: () => languages });
        Object.defineProperty(navigator, 'language', { get: () => single });
      },
      { languages, single },
    );
    const page = await context.newPage();
    await page.goto('/');
    await expect(page.locator('html')).toHaveAttribute('lang', expected);
    await expect(
      page.getByRole('button', {
        name: expected === 'sv' ? 'Inställningar' : 'Settings',
        exact: true,
      }),
    ).toBeVisible();
    await context.close();
  });
}

test('language settings remain available on public error pages', async ({ browser }) => {
  const context = await browser.newContext({ locale: 'en-US' });
  const page = await context.newPage();
  await page.goto('/s/' + 'f'.repeat(48));
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await choose(page, 'Language', 'Svenska');
  await expect(page.locator('html')).toHaveAttribute('lang', 'sv');
  await context.close();
});

test('a real login error changes language without resubmitting credentials', async ({
  browser,
}) => {
  const context = await browser.newContext({ locale: 'en-US' });
  const page = await context.newPage();
  let logins = 0;
  page.on('request', (r) => {
    if (r.url().endsWith('/api/login')) logins++;
  });
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('qaadmin');
  await page.getByLabel('Password', { exact: true }).fill('incorrect-password');
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveText('Incorrect username or password');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await choose(page, 'Language', 'Svenska');
  await expect(page.getByRole('alert')).toHaveText('Fel användarnamn eller lösenord');
  expect(logins).toBe(1);
  await context.close();
});
