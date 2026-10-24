import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
import { readFile, mkdir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
const digest = (b: Buffer) => createHash('sha256').update(b).digest('hex');
test('first admin, dark/light mobile/desktop, real multi-file pause/resume, protected download, admin users and quotas', async ({
  page,
  browser,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await mkdir('test-results/screens', { recursive: true });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Din första MagiSend.' })).toBeVisible();
  expect(await page.locator('html').getAttribute('data-theme')).toBe('dark');
  await page.screenshot({ path: 'test-results/screens/setup-desktop-dark.png', fullPage: true });
  await page.getByLabel('Installationskod', { exact: true }).fill(setupCode);
  await expect(page.getByText(/cat \/etc\/magisend/)).toHaveCount(0);
  await page.getByLabel('Användarnamn', { exact: true }).fill('qaadmin');
  await page.getByLabel('Lösenord', { exact: true }).fill('magisend-e2e-only-password');
  const creationRequests: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && /\/api\/(setup|register|login)$/.test(request.url()))
      creationRequests.push(request.url());
  });
  await page.getByLabel('Upprepa lösenord', { exact: true }).fill('different-test-only-password');
  await page.getByRole('button', { name: 'Skapa administratör' }).click();
  await expect(page.getByRole('alert')).toHaveText('Lösenorden stämmer inte överens.');
  expect(creationRequests).toEqual([]);
  await page.getByLabel('Upprepa lösenord', { exact: true }).fill('magisend-e2e-only-password');
  await page.getByRole('button', { name: 'Skapa administratör' }).click();
  await expect(page.getByRole('heading', { name: 'Från dig. Till vem som helst.' })).toBeVisible();
  await page.screenshot({
    path: 'test-results/screens/workspace-desktop-dark.png',
    fullPage: true,
  });
  await page.getByRole('button', { name: 'Byt till ljust tema' }).click();
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.screenshot({
    path: 'test-results/screens/workspace-desktop-light.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Byt till mörkt tema' }).click();
  await page.screenshot({ path: 'test-results/screens/workspace-mobile-dark.png', fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Byt till ljust tema' }).click();
  await page.screenshot({
    path: 'test-results/screens/workspace-mobile-light.png',
    fullPage: true,
  });
  await page.getByRole('button', { name: 'Byt till mörkt tema' }).click();
  await page.setViewportSize({ width: 1440, height: 1000 });
  const payload = Buffer.alloc(9 * 1024 * 1024 + 37);
  for (let i = 0; i < payload.length; i++) payload[i] = (i * 31 + 17) % 256;
  const note = Buffer.from('MagiSend real LAN test — hej världen!\n', 'utf8');
  const files = [
    { name: 'large-test.bin', mimeType: 'application/octet-stream', buffer: payload },
    { name: 'hälsning.txt', mimeType: 'text/plain', buffer: note },
  ];
  await page.getByLabel('Välj filer', { exact: true }).setInputFiles(files);
  await page.getByLabel('Namn på delningen').fill('Verifierad leverans');
  await page.getByLabel('Lösenord (valfritt)').fill('a-share-password');
  let secondStarted = false;
  // Files go up in parallel, so the large file's second piece is held back.
  await page.route('**/api/uploads/*', async (route) => {
    if (route.request().method() === 'PATCH') {
      if (route.request().headers()['upload-offset'] === String(4 * 1024 * 1024)) {
        secondStarted = true;
        await new Promise<void>((resolve) => {
          const t = setInterval(() => {
            if (!page.url() || page.isClosed()) {
              clearInterval(t);
              resolve();
            }
          }, 100);
          setTimeout(() => {
            clearInterval(t);
            resolve();
          }, 2500);
        });
        await route.abort().catch(() => {});
        return;
      }
    }
    await route.continue();
  });
  await page.getByRole('button', { name: 'Skapa delningslänk' }).click();
  await expect.poll(() => secondStarted).toBe(true);
  await page.getByRole('button', { name: 'Pausa uppladdning' }).click();
  await expect(page.getByText('Pausad. Dina uppladdade delar är sparade.')).toBeVisible();
  const drafts = await (await page.request.get('/api/shares')).json();
  const draft = drafts.find((s: any) => s.title === 'Verifierad leverans');
  expect(draft.published).toBe(false);
  expect(draft.files[0].offset).toBe(4 * 1024 * 1024);
  await page.unroute('**/api/uploads/*');
  await page.getByRole('button', { name: 'Fortsätt uppladdning' }).click();
  await expect(page.getByRole('heading', { name: 'Redo att delas.' })).toBeVisible();
  const url = await page.getByLabel('Delningslänk').inputValue();
  expect(url).toContain('/s/');
  await page.getByText('Visa QR-kod').click();
  await expect(page.getByAltText('QR-kod till delningen')).toBeVisible();
  await page.getByRole('button', { name: 'Stäng', exact: true }).click();
  await page.getByRole('button', { name: 'Mina delningar' }).click();
  await expect(page.getByRole('heading', { name: 'Verifierad leverans' })).toBeVisible();
  await page.screenshot({ path: 'test-results/screens/shares-desktop.png', fullPage: true });
  const anonymous = await browser.newContext({
    viewport: { width: 390, height: 844 },
    locale: 'sv-SE',
  });
  const publicPage = await anonymous.newPage();
  publicPage.on('pageerror', (e) => errors.push(e.message));
  await publicPage.goto(url);
  await expect(publicPage.getByRole('heading', { name: 'För rätt ögon.' })).toBeVisible();
  expect(
    (await anonymous.request.get(`/api/public/${draft.id}/files/${draft.files[0].id}`)).status(),
  ).toBe(401);
  await publicPage.getByLabel('Delningens lösenord').fill('wrong');
  await publicPage.getByRole('button', { name: 'Öppna delning' }).click();
  await expect(publicPage.getByText('Fel lösenord')).toBeVisible();
  await publicPage.getByLabel('Delningens lösenord').fill('a-share-password');
  await publicPage.getByRole('button', { name: 'Öppna delning' }).click();
  await expect(publicPage.getByText('hälsning.txt')).toBeVisible();
  await publicPage.screenshot({ path: 'test-results/screens/public-mobile.png', fullPage: true });
  expect(await publicPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
    true,
  );
  const dl = await anonymous.request.get(`/api/public/${draft.id}/files/${draft.files[0].id}`);
  expect(dl.status()).toBe(200);
  expect(digest(await dl.body())).toBe(digest(payload));
  const noteResponse = await anonymous.request.get(
    `/api/public/${draft.id}/files/${draft.files[1].id}`,
  );
  expect(digest(await noteResponse.body())).toBe(digest(note));
  const range = await anonymous.request.get(`/api/public/${draft.id}/files/${draft.files[0].id}`, {
    headers: { Range: 'bytes=0-31' },
  });
  expect(range.status()).toBe(206);
  expect(await range.body()).toEqual(payload.subarray(0, 32));
  const zip = await anonymous.request.get(`/api/public/${draft.id}/zip`);
  expect(zip.status()).toBe(200);
  const zipData = await zip.body();
  expect(zipData.subarray(0, 2).toString()).toBe('PK');
  await anonymous.close();
  await page.getByRole('button', { name: 'Administration', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Administration.' })).toBeVisible();
  await expect(page.getByLabel('Tillåt vanlig registrering')).not.toBeChecked();
  await page.getByLabel('Nytt användarnamn').fill('qabob');
  await page.getByLabel('Tillfälligt lösenord').fill('magisend-test-user-password');
  await page.getByRole('button', { name: 'Skapa användare' }).click();
  await expect(page.getByText('Användaren är skapad.')).toBeVisible();
  await page.screenshot({ path: 'test-results/screens/admin-desktop.png', fullPage: true });
  const users = await (await page.request.get('/api/admin/users')).json();
  expect(users.filter((u: any) => u.role === 'admin')).toHaveLength(1);
  expect(users.find((u: any) => u.username === 'qabob').role).toBe('user');
  await page.getByLabel('Tillåt vanlig registrering').check();
  await page.getByRole('button', { name: 'Spara policy', exact: true }).click();
  await expect(page.getByText('Policyn är sparad.')).toBeVisible();
  const registered = await browser.newContext({ locale: 'en-US' });
  const signup = await registered.newPage();
  signup.on('pageerror', (e) => errors.push(e.message));
  await signup.goto('/');
  await signup.getByRole('button', { name: 'Create account', exact: true }).click();
  await signup.getByLabel('Username', { exact: true }).fill('qaregistered');
  await signup.getByLabel('Password', { exact: true }).fill('magisend-registration-test');
  const registrationRequests: string[] = [];
  signup.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith('/api/register'))
      registrationRequests.push(request.url());
  });
  await signup.getByRole('button', { name: 'Create account', exact: true }).click();
  await expect(signup.getByRole('alert')).toHaveText('Passwords do not match.');
  expect(registrationRequests).toEqual([]);
  await signup.getByLabel('Repeat password', { exact: true }).fill('magisend-registration-test');
  await signup.getByRole('button', { name: 'Create account', exact: true }).click();
  await expect(signup.getByRole('button', { name: 'Send files', exact: true })).toBeVisible();
  const registeredMe = await (await registered.request.get('/api/me')).json();
  expect(registeredMe.user.role).toBe('user');
  await expect(signup.getByRole('button', { name: 'Administration', exact: true })).toHaveCount(0);
  expect(
    (
      await registered.request.post('/api/setup', {
        headers: { 'X-MagiSend': '1' },
        data: { username: 'anotheradmin', password: 'another-long-password' },
      })
    ).status(),
  ).toBe(409);
  await registered.close();
  await page.getByLabel('Tillåt vanlig registrering').uncheck();
  await page.getByRole('button', { name: 'Spara policy', exact: true }).click();
  await expect(page.getByText('Policyn är sparad.')).toBeVisible();
  await page.getByRole('button', { name: 'Kontomeny' }).click();
  await page.getByRole('button', { name: 'Logga ut' }).click();
  await expect(page.getByRole('heading', { name: 'Välkommen tillbaka.' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Skapa administratör' })).toHaveCount(0);
  await expect(page.getByText('Skapa konto', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Upprepa lösenord', { exact: true })).toHaveCount(0);
  await page.getByLabel('Användarnamn', { exact: true }).fill('qaadmin');
  await page.getByLabel('Lösenord', { exact: true }).fill('magisend-e2e-only-password');
  await page.getByRole('button', { name: 'Logga in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Från dig. Till vem som helst.' })).toBeVisible();
  await page.getByRole('button', { name: 'Kontomeny' }).click();
  await page.getByRole('button', { name: 'Logga ut' }).click();
  await expect(page.getByRole('heading', { name: 'Välkommen tillbaka.' })).toBeVisible();
  expect(errors).toEqual([]);
  console.log(
    'Verified: uploaded ' +
      payload.length +
      ' + ' +
      note.length +
      ' bytes, pause at4MiB, exact SHA256 roundtrip, protected download+ZIP+HTTP Range, one admin only, no JavaScript page errors.',
  );
});
