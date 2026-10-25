import { test, expect } from '@playwright/test';
import sharp from 'sharp';
import { mkdir } from 'node:fs/promises';

// Real browser regressions for 0.3.1: inline thumbnails, share description and a
// custom link name, against a disposable instance with a real backend.
test('custom link, description and inline thumbnails work for owner and visitor', async ({
  page,
  browser,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push('owner: ' + error.message));
  await mkdir('test-results/share-details', { recursive: true });
  const stamp = Date.now();
  const savedTitle = 'Sparad ' + stamp;
  const linkedTitle = 'Länkad ' + stamp;
  const plainTitle = 'Utan bild ' + stamp;
  const savedSlug = 'sparad-' + stamp;
  const linkedSlug = 'lankad-' + stamp;
  const plainSlug = 'utan-bild-' + stamp;
  const png = await sharp({
    create: { width: 400, height: 200, channels: 4, background: { r: 255, g: 95, b: 0, alpha: 1 } },
  })
    .png()
    .toBuffer();
  const unsafe = Buffer.from(
    '<svg xmlns="http://www.w3.org/2000/svg"><script>window.__MAGISEND_PREVIEW_EXECUTED__=true</script></svg>',
  );

  await page.goto('/');
  await page.getByLabel('Användarnamn', { exact: true }).fill('qaadmin');
  await page.getByLabel('Lösenord', { exact: true }).fill('magisend-e2e-only-password');
  await page.getByRole('button', { name: 'Logga in', exact: true }).click();
  await expect(page.locator('.dropzone')).toBeVisible();

  // A custom link that is already taken must be refused in the active language.
  await page
    .getByLabel('Välj filer', { exact: true })
    .setInputFiles([{ name: 'holiday.png', mimeType: 'image/png', buffer: png }]);
  // The selected image is previewed from the browser before anything is uploaded.
  const queued = page.locator('.selected-files img');
  await expect(queued).toHaveCount(1);
  expect(await queued.evaluate((image: HTMLImageElement) => image.getAttribute('src'))).toMatch(
    /^blob:/,
  );
  await expect
    .poll(async () => await queued.evaluate((image: HTMLImageElement) => image.naturalWidth))
    .toBeGreaterThan(0);
  await page.getByLabel('Namn på delningen').fill(savedTitle);
  await page.getByLabel('Beskrivning').fill('Bilder från helgen');
  await page.getByLabel('Egen länk').fill(savedSlug);
  await page.getByRole('button', { name: 'Skapa delningslänk' }).click();
  await expect(page.getByRole('heading', { name: 'Redo att delas.' })).toBeVisible();
  await page.getByRole('button', { name: 'Stäng', exact: true }).click();

  await page
    .getByLabel('Välj filer', { exact: true })
    .setInputFiles([{ name: 'other.png', mimeType: 'image/png', buffer: png }]);
  await page.getByLabel('Egen länk').fill(savedSlug);
  await page.getByRole('button', { name: 'Skapa delningslänk' }).click();
  await expect(page.getByText(/upptaget|already taken/i)).toBeVisible();
  expect(
    (await (await page.request.get('/api/shares')).json()).filter((s: any) => s.slug === savedSlug),
  ).toHaveLength(1);
  await page.getByLabel('Egen länk').fill(linkedSlug);
  await page.getByLabel('Namn på delningen').fill(linkedTitle);
  await page.getByLabel('Beskrivning').fill('Andra omgången');
  await page.getByRole('button', { name: 'Skapa delningslänk' }).click();
  await expect(page.getByRole('heading', { name: 'Redo att delas.' })).toBeVisible();

  // The ready dialog shows the custom link, the description and a real thumbnail.
  const dialog = page.getByRole('dialog');
  await expect(page.getByLabel('Delningslänk')).toHaveValue(new RegExp('/s/' + linkedSlug + '$'));
  // The picture itself is shown large where the done mark used to be.
  const dialogImage = dialog.locator('.ready-image img');
  await expect(dialogImage).toHaveCount(1);
  await expect(dialogImage).toHaveAttribute('src', /^blob:/);
  await expect
    .poll(async () => await dialogImage.evaluate((image: HTMLImageElement) => image.naturalWidth))
    .toBeGreaterThan(0);
  await expect(dialog.locator('.share-description')).toHaveText('Andra omgången');
  await page.screenshot({ path: 'test-results/share-details/ready-dialog.png', fullPage: true });
  await page.getByRole('button', { name: 'Stäng', exact: true }).click();

  // My shares: description inline, and the image row carries a loaded thumbnail.
  await page.getByRole('button', { name: 'Mina delningar' }).click();
  const first = page
    .locator('article')
    .filter({ has: page.getByRole('heading', { name: savedTitle, exact: true }) });
  await expect(first.locator('.share-description')).toHaveText('Bilder från helgen');
  const cardThumb = first.locator('.owner-preview-files img');
  await expect(cardThumb).toHaveCount(1);
  await expect
    .poll(async () => await cardThumb.evaluate((image: HTMLImageElement) => image.naturalWidth))
    .toBeGreaterThan(0);
  await expect
    .poll(async () => await cardThumb.evaluate((image: HTMLImageElement) => image.naturalHeight))
    .toBeLessThanOrEqual(256);
  await page.screenshot({
    path: 'test-results/share-details/my-shares-thumbnails.png',
    fullPage: true,
  });

  // The visitor side: custom link opens the share, the id link keeps working.
  const shares = await (await page.request.get('/api/shares')).json();
  const share = shares.find((s: any) => s.slug === linkedSlug);
  const image = share.files.find((f: any) => f.name === 'other.png');
  expect(share.description).toBe('Andra omgången');
  const anonymous = await browser.newContext({
    baseURL: new URL(page.url()).origin,
    locale: 'sv-SE',
    viewport: { width: 390, height: 844 },
  });
  const visitor = await anonymous.newPage();
  visitor.on('pageerror', (error) => errors.push('visitor: ' + error.message));
  await visitor.goto('/s/' + linkedSlug);
  await expect(visitor.getByRole('heading', { name: linkedTitle, exact: true })).toBeVisible();
  await expect(visitor.locator('.share-description')).toHaveText('Andra omgången');
  const publicThumb = visitor.locator('.public-files img');
  await expect(publicThumb).toHaveCount(1);
  await expect
    .poll(async () => await publicThumb.evaluate((image: HTMLImageElement) => image.naturalWidth))
    .toBeGreaterThan(0);
  // The visitor's own link is the ref used for its assets, so the password grant
  // stays scoped to the exact link it unlocked.
  expect(await publicThumb.evaluate((image: HTMLImageElement) => image.getAttribute('src'))).toBe(
    '/api/public/' + linkedSlug + '/files/' + image.id + '/thumbnail',
  );
  await visitor.screenshot({
    path: 'test-results/share-details/public-custom-link.png',
    fullPage: true,
  });
  // The filename stays the single control; it still opens the full, validated preview.
  await visitor.getByRole('button', { name: 'other.png', exact: true }).click();
  const preview = visitor.getByRole('dialog');
  await expect(preview.getByRole('img')).toBeVisible();
  expect(
    await preview.getByRole('img').evaluate((element: HTMLImageElement) => element.naturalWidth),
  ).toBe(400);
  await preview.getByRole('button', { name: 'Stäng', exact: true }).click();
  // The generated id link must keep working unchanged.
  await visitor.goto('/s/' + share.id);
  await expect(visitor.getByRole('heading', { name: linkedTitle, exact: true })).toBeVisible();
  // Every public surface accepts the custom link, not only the page data.
  expect(
    (await visitor.request.get('/api/public/' + linkedSlug + '/files/' + image.id)).status(),
  ).toBe(200);
  expect((await visitor.request.get('/api/public/' + linkedSlug + '/zip')).status()).toBe(200);
  // An SVG never opens in the viewer and its scripts never run.
  await page.getByRole('button', { name: 'Skicka filer', exact: true }).click();
  await page
    .getByLabel('Välj filer', { exact: true })
    .setInputFiles([{ name: 'unsafe.svg', mimeType: 'image/svg+xml', buffer: unsafe }]);
  await page.getByLabel('Namn på delningen').fill(plainTitle);
  await page.getByLabel('Egen länk').fill(plainSlug);
  await page.getByRole('button', { name: 'Skapa delningslänk' }).click();
  await expect(page.getByRole('heading', { name: 'Redo att delas.' })).toBeVisible();
  await expect(page.locator('.ready-image img, .result-thumbs img')).toHaveCount(0);
  await page.getByRole('button', { name: 'Stäng', exact: true }).click();
  const unanswered = await visitor.goto('/s/' + plainSlug);
  expect(unanswered?.status(), 'page for ' + plainSlug).toBe(200);
  await expect(visitor.getByRole('button', { name: 'unsafe.svg', exact: true })).toBeVisible();
  // A picture drawn by the owner's browser may show; it is always the server's
  // re-encoded PNG or JPEG, never the SVG itself.
  for (const source of await visitor
    .locator('.public-files img')
    .evaluateAll((images) => images.map((i) => (i as HTMLImageElement).src)))
    expect(source).toMatch(/\/thumbnail$/);
  const shared = (await (await page.request.get('/api/shares')).json()).find(
    (s: any) => s.slug === plainSlug,
  );
  const picture = await visitor.request.get(
    '/api/public/' + plainSlug + '/files/' + shared.files[0].id + '/thumbnail',
  );
  if (picture.status() === 200)
    expect(picture.headers()['content-type']).toMatch(/^image\/(png|jpeg)$/);
  // The SVG itself is shown only as a PNG drawn from it.
  await visitor.getByRole('button', { name: 'unsafe.svg', exact: true }).click();
  const shown = visitor.getByRole('dialog').getByRole('img');
  await expect(shown).toBeVisible();
  // Opened on its own, as "Open image in new tab" does, it is a PNG.
  const [opened] = await Promise.all([
    visitor.waitForEvent('popup'),
    shown.evaluate((element) => window.open((element as HTMLImageElement).src)),
  ]);
  await opened.waitForLoadState();
  expect(await opened.evaluate(() => document.contentType)).toBe('image/png');
  expect(await opened.evaluate(() => Object.hasOwn(window, '__MAGISEND_PREVIEW_EXECUTED__'))).toBe(
    false,
  );
  await opened.close();
  expect(await visitor.evaluate(() => Object.hasOwn(window, '__MAGISEND_PREVIEW_EXECUTED__'))).toBe(
    false,
  );
  await anonymous.close();
  expect(errors).toEqual([]);
});
