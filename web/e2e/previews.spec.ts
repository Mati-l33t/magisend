import { test, expect } from '@playwright/test';
import sharp from 'sharp';
import { mkdir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { choose } from './choose';

test('permanent image share has owner/public previews and a safe fallback', async ({
  page,
  browser,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const verificationTitle = 'Permanent preview ' + Date.now();
  await mkdir('test-results/previews', { recursive: true });
  await page.goto('/');
  await page.getByLabel('Användarnamn', { exact: true }).fill('qaadmin');
  await page.getByLabel('Lösenord', { exact: true }).fill('magisend-e2e-only-password');
  await page.getByRole('button', { name: 'Logga in', exact: true }).click();
  await expect(page.locator('.dropzone')).toBeVisible();
  const png = await sharp({
    create: { width: 320, height: 180, channels: 4, background: { r: 255, g: 95, b: 0, alpha: 1 } },
  })
    .png()
    .toBuffer();
  const unsafe = Buffer.from(
    '<svg xmlns="http://www.w3.org/2000/svg"><script>window.__MAGISEND_PREVIEW_EXECUTED__=true</script></svg>',
  );
  await page.getByLabel('Välj filer', { exact: true }).setInputFiles([
    { name: 'holiday.png', mimeType: 'image/png', buffer: png },
    { name: 'unsafe.svg', mimeType: 'image/svg+xml', buffer: unsafe },
  ]);
  await page.getByLabel('Namn på delningen').fill(verificationTitle);
  await choose(page, 'Giltighetstid', 'Upphör aldrig');
  await page.getByLabel('Lösenord (valfritt)').fill('private-preview-password');
  await page.getByRole('button', { name: 'Skapa delningslänk' }).click();
  await expect(page.getByRole('heading', { name: 'Redo att delas.' })).toBeVisible();
  const shareUrl = await page.getByLabel('Delningslänk').inputValue();
  const shares = await (await page.request.get('/api/shares')).json();
  const share = shares.find((s: any) => s.title === verificationTitle);
  expect(share.expires).toBe(0);
  expect(share.published).toBe(true);
  const image = share.files.find((f: any) => f.name === 'holiday.png');
  await page.getByRole('button', { name: 'Stäng', exact: true }).click();
  await page.getByRole('button', { name: 'Mina delningar' }).click();
  const card = page
    .locator('article')
    .filter({ has: page.getByRole('heading', { name: verificationTitle, exact: true }) });
  await expect(card).not.toContainText('1970');
  await expect(card).not.toContainText('Utgången');
  await card.getByRole('button', { name: 'holiday.png', exact: true }).click();
  const ownerDialog = page.getByRole('dialog');
  await expect(ownerDialog.getByRole('img')).toBeVisible();
  expect(
    (await ownerDialog.boundingBox())!.width,
    'Desktop preview should use its wide viewer rather than inherit the 450px form cap',
  ).toBeGreaterThanOrEqual(700);
  expect(
    await ownerDialog
      .getByRole('img')
      .evaluate((element) => (element as HTMLImageElement).naturalWidth),
  ).toBe(320);
  const ownerDownload = await ownerDialog
    .getByRole('link', { name: /ladda ner/i })
    .getAttribute('href');
  expect(ownerDownload).toBe('/api/shares/' + share.id + '/files/' + image.id);
  const original = await page.request.get(ownerDownload!);
  expect(original.ok()).toBe(true);
  expect(
    createHash('sha256')
      .update(await original.body())
      .digest('hex'),
  ).toBe(createHash('sha256').update(png).digest('hex'));
  await page.screenshot({
    path: 'test-results/previews/owner-preview-desktop.png',
    fullPage: true,
  });
  await ownerDialog.getByRole('button', { name: 'Stäng', exact: true }).click();
  const anonymous = await browser.newContext({
    baseURL: new URL(page.url()).origin,
    locale: 'sv-SE',
    viewport: { width: 390, height: 844 },
  });
  const publicPage = await anonymous.newPage();
  publicPage.on('pageerror', (error) => errors.push(error.message));
  const publicPreview = '/api/public/' + share.id + '/files/' + image.id + '/preview';
  expect((await anonymous.request.get(publicPreview)).status()).toBe(401);
  await publicPage.goto(shareUrl);
  await expect(publicPage.getByRole('heading', { name: 'För rätt ögon.' })).toBeVisible();
  await publicPage.getByLabel('Delningens lösenord').fill('private-preview-password');
  await publicPage.getByRole('button', { name: 'Öppna delning' }).click();
  await expect(publicPage.getByRole('button', { name: 'holiday.png', exact: true })).toBeVisible();
  await expect(publicPage.locator('main')).not.toContainText('1970');
  await publicPage.getByRole('button', { name: 'Byt till ljust tema', exact: true }).click();
  await publicPage.getByRole('button', { name: 'holiday.png', exact: true }).click();
  const publicDialog = publicPage.getByRole('dialog');
  await expect(publicDialog.getByRole('img')).toBeVisible();
  expect(
    await publicDialog
      .getByRole('img')
      .evaluate((element) => (element as HTMLImageElement).naturalWidth),
  ).toBe(320);
  expect((await anonymous.request.get(publicPreview)).headers()['content-type']).toBe('image/png');
  expect(await publicPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
    true,
  );
  await publicPage.screenshot({
    path: 'test-results/previews/public-preview-mobile-light.png',
    fullPage: true,
  });
  await publicDialog.getByRole('button', { name: 'Stäng', exact: true }).click();
  // An SVG is shown as a PNG drawn from it, so its script runs nowhere, not
  // even when the shown picture is opened on its own.
  await publicPage.getByRole('button', { name: 'unsafe.svg', exact: true }).click();
  const fallback = publicPage.getByRole('dialog');
  await expect(fallback.getByRole('img')).toBeVisible();
  // Opened on its own, as "Open image in new tab" does, it is a PNG.
  const [opened] = await Promise.all([
    publicPage.waitForEvent('popup'),
    fallback.getByRole('img').evaluate((element) => window.open((element as HTMLImageElement).src)),
  ]);
  await opened.waitForLoadState();
  expect(await opened.evaluate(() => document.contentType)).toBe('image/png');
  expect(await opened.evaluate(() => Object.hasOwn(window, '__MAGISEND_PREVIEW_EXECUTED__'))).toBe(
    false,
  );
  await opened.close();
  await expect(fallback.getByRole('link', { name: /ladda ner/i })).toBeVisible();
  await expect(publicPage.locator('iframe,object,embed')).toHaveCount(0);
  expect(
    await publicPage.evaluate(() => Object.hasOwn(window, '__MAGISEND_PREVIEW_EXECUTED__')),
  ).toBe(false);
  await publicPage.screenshot({
    path: 'test-results/previews/unsupported-preview-mobile.png',
    fullPage: true,
  });
  await anonymous.close();
  expect(errors).toEqual([]);
});
