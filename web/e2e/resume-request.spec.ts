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

test('an upload link continues an upload after the tab was closed', async ({ page, browser }) => {
  const credentials = { username: 'collector', password: 'disposable collect passphrase' };
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
  const request = await (
    await page.request.post('/api/requests', { data: { title: 'Raw footage', days: 7 }, headers })
  ).json();

  // Without a service worker every upload request passes through page.route.
  const guest = await browser.newContext({ locale: 'en-GB', serviceWorkers: 'block' });
  const first = await guest.newPage();
  await first.goto('/r/' + request.id);
  const hold = await holdSecondPiece(first, '**/api/requests/*/uploads/*/files/*');
  await first.getByLabel('Choose files').setInputFiles([big]);
  await first.getByRole('button', { name: 'Send 1 file' }).click();
  await expect.poll(hold.isHeld).toBe(true);
  await first.close({ runBeforeUnload: false });

  const second = await guest.newPage();
  await second.goto('/r/' + request.id);
  await expect(second.getByText(/Your last upload did not finish: 1 file /)).toBeVisible();
  const chooser = second.waitForEvent('filechooser');
  await second.getByRole('button', { name: 'Choose the files' }).click();
  await (await chooser).setFiles([big]);
  await expect(second.getByText(/delivered to collector/)).toBeVisible();
  await guest.close();

  const files = await (await page.request.get('/api/library/files')).json();
  expect(files.map((f: { name: string; size: number }) => [f.name, f.size])).toEqual([
    ['holiday-video.bin', statSync(big).size],
  ]);
});
