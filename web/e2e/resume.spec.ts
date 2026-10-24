import { test, expect, type Page } from '@playwright/test';
import { setupCode } from './setup';
import { mkdtempSync, readFileSync, writeFileSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
// Real HTTP/backend + browser on a disposable server: uploads survive a
// dropped connection on their own, and a paused or abandoned upload in the
// library or through an upload link continues where it stopped.
test.use({ locale: 'en-GB', actionTimeout: 15000 });
test.setTimeout(120000);

const headers = { 'X-MagiSend': '1' };
const piece = 4 * 1024 * 1024;
const dir = mkdtempSync(join(tmpdir(), 'magisend-resume-'));
function file(name: string, size: number) {
  const data = Buffer.alloc(size);
  for (let i = 0; i < size; i++) data[i] = (i * 7 + name.length) % 251;
  const path = join(dir, name);
  writeFileSync(path, data);
  return path;
}
const big = file('holiday-video.bin', 9 * 1024 * 1024 + 123);
const small = file('notes.txt', 2000);

// Holds back the piece that starts at 4 MiB until released.
async function holdSecondPiece(page: Page, pattern: string) {
  let release = () => {};
  let held = false;
  await page.route(pattern, async (route) => {
    if (
      route.request().method() === 'PATCH' &&
      route.request().headers()['upload-offset'] === String(piece) &&
      !held
    ) {
      held = true;
      await new Promise<void>((resolve) => (release = resolve));
      await route.abort().catch(() => {});
      return;
    }
    await route.continue().catch(() => {});
  });
  return { isHeld: () => held, release: () => release() };
}

test('library uploads retry a dropped piece, pause, and resume after a reload', async ({
  page,
}) => {
  const credentials = { username: 'resumer', password: 'disposable resume passphrase' };
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
  await page.goto('/library');

  // A dropped connection is retried without the user doing anything.
  // Two drops in a row keep the notice up for about three seconds.
  let dropped = 0;
  await page.route('**/api/uploads/*', async (route) => {
    if (route.request().method() === 'PATCH' && dropped < 2) {
      dropped++;
      return route.abort('connectionreset');
    }
    await route.continue();
  });
  await page.getByLabel('Upload private files').setInputFiles([small]);
  await expect(page.getByText('Connection lost. Trying again…')).toBeVisible();
  await expect(page.getByText('notes.txt', { exact: true })).toBeVisible();
  await page.unroute('**/api/uploads/*');

  // Pausing leaves an unfinished upload that survives a reload.
  const hold = await holdSecondPiece(page, '**/api/uploads/*');
  await page.getByLabel('Upload private files').setInputFiles([big]);
  await expect.poll(hold.isHeld).toBe(true);
  await expect(page.locator('.upload-speed')).toBeVisible();
  await page.getByRole('button', { name: 'Pause upload' }).click();
  hold.release();
  await expect(page.getByText('Paused. Your uploaded parts are saved.')).toBeVisible();
  await page.unroute('**/api/uploads/*');
  await page.reload();
  await expect(page.getByText(/1 file did not finish uploading/)).toBeVisible();
  const pending = await (await page.request.get('/api/library/uploads')).json();
  expect(pending[0].files[0].offset).toBe(piece);

  const chooser = page.waitForEvent('filechooser');
  await page.getByRole('button', { name: 'Choose the files' }).click();
  await (await chooser).setFiles([big]);
  await expect(page.getByText('holiday-video.bin', { exact: true })).toBeVisible();
  await expect(page.getByText(/did not finish uploading/)).toHaveCount(0);
  // The last piece may still be on its way when the banner goes.
  await expect
    .poll(async () => (await (await page.request.get('/api/library/uploads')).json()).length)
    .toBe(0);
  const files = await (await page.request.get('/api/library/files')).json();
  const video = files.find((f: { name: string }) => f.name === 'holiday-video.bin');
  expect(video.size).toBe(statSync(big).size);
  const download = await page.request.get('/api/library/files/' + video.id + '/download');
  expect(Buffer.compare(await download.body(), readFileSync(big))).toBe(0);
});
