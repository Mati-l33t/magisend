import { test, expect, type Page } from '@playwright/test';
import { setupCode } from './setup';
import { crc32, deflateSync } from 'node:zlib';
// Real browser on a disposable server: previews zoom with the wheel, the
// buttons, a double click and a two-finger pinch, and open in full screen.
test.use({ locale: 'en-GB', actionTimeout: 15000 });

const credentials = { username: 'zoomer', password: 'disposable zoom passphrase' };
const headers = { 'X-MagiSend': '1' };
const shots = process.env.ZOOM_SHOTS;

function pdf(pages: number) {
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    `<< /Type /Pages /Kids [${Array.from({ length: pages }, (_, i) => `${4 + i * 2} 0 R`).join(' ')}] /Count ${pages} >>`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
  for (let i = 0; i < pages; i++) {
    const text = `BT /F1 18 Tf 40 520 Td (Page ${i + 1} of the zoom test) Tj ET`;
    objects.push(
      `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 595] /Contents ${5 + i * 2} 0 R /Resources << /Font << /F1 3 0 R >> >> >>`,
      `<< /Length ${text.length} >>\nstream\n${text}\nendstream`,
    );
  }
  let body = '%PDF-1.4\n';
  const offsets: number[] = [];
  objects.forEach((object, i) => {
    offsets.push(body.length);
    body += `${i + 1} 0 obj\n${object}\nendobj\n`;
  });
  const xref = body.length;
  body += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const offset of offsets) body += String(offset).padStart(10, '0') + ' 00000 n \n';
  body += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(body, 'latin1');
}

function png(width: number, height: number) {
  const row = width * 3 + 1;
  const raw = Buffer.alloc(row * height);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const at = y * row + 1 + x * 3;
      const check = ((x >> 5) + (y >> 5)) % 2 ? 230 : 40;
      raw[at] = check;
      raw[at + 1] = Math.round((x / width) * 255);
      raw[at + 2] = Math.round((y / height) * 255);
    }
  const chunk = (type: string, data: Buffer) => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, 'latin1'), data]);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc32(body));
    return Buffer.concat([length, body, sum]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

async function level(page: Page) {
  const text = await page.getByRole('button', { name: 'Fit to screen' }).innerText();
  return Number(text.replace(/[^0-9]/g, ''));
}

test('zoom and full screen in file previews', async ({ page, browser, baseURL }) => {
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
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/library');
  await page.getByLabel('Upload private files').setInputFiles([
    { name: 'pages.pdf', mimeType: 'application/pdf', buffer: pdf(3) },
    { name: 'photo.png', mimeType: 'image/png', buffer: png(1600, 1000) },
  ]);

  // PDF: the wheel scrolls the pages; Ctrl + wheel zooms and redraws sharper.
  await page.getByRole('button', { name: 'pages.pdf', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Preview: pages.pdf' });
  const canvases = dialog.locator('.pdf-pages canvas');
  await expect(canvases).toHaveCount(3);
  const area = dialog.locator('.zoom-area');
  const first = await canvases.first().evaluate((c: HTMLCanvasElement) => c.width);
  const box = (await area.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 3);
  await page.mouse.wheel(0, 300);
  await expect.poll(() => area.evaluate((a) => a.scrollTop)).toBeGreaterThan(0);
  expect(await level(page)).toBe(100);
  await area.evaluate((a) => (a.scrollTop = 0));
  await page.keyboard.down('Control');
  await page.mouse.wheel(0, -400);
  await page.keyboard.up('Control');
  await expect.poll(() => level(page)).toBeGreaterThan(150);
  expect(await area.evaluate((a) => a.scrollWidth > a.clientWidth)).toBe(true);
  await expect
    .poll(() => canvases.first().evaluate((c: HTMLCanvasElement) => c.width))
    .toBeGreaterThan(first * 1.4);
  if (shots) await page.screenshot({ path: shots + '/pdf-zoomed.png' });
  await dialog.getByRole('button', { name: 'Zoom out' }).click();
  await dialog.getByRole('button', { name: 'Fit to screen' }).click();
  expect(await level(page)).toBe(100);

  // Full screen fills the window and goes back on the same button.
  await dialog.getByRole('button', { name: 'Full screen' }).click();
  await expect(dialog).toHaveClass(/is-full/);
  await expect
    .poll(async () => Math.round((await dialog.boundingBox())!.width))
    .toBeGreaterThanOrEqual(1270);
  if (shots) await page.screenshot({ path: shots + '/pdf-full.png' });
  await dialog.getByRole('button', { name: 'Exit full screen' }).click();
  await expect(dialog).not.toHaveClass(/is-full/);
  await dialog.getByRole('button', { name: 'Close' }).click();

  // Image: the wheel zooms at the pointer and a double click goes back.
  await page.getByRole('button', { name: 'photo.png', exact: true }).click();
  const photo = page.getByRole('dialog', { name: 'Preview: photo.png' });
  const image = photo.getByRole('img', { name: 'photo.png' });
  await expect.poll(() => image.evaluate((i: HTMLImageElement) => i.style.opacity)).toBe('');
  const fitted = (await image.boundingBox())!.width;
  const imageArea = (await photo.locator('.zoom-area').boundingBox())!;
  expect(fitted).toBeLessThanOrEqual(imageArea.width);
  await page.mouse.move(imageArea.x + imageArea.width / 2, imageArea.y + imageArea.height / 2);
  await page.mouse.wheel(0, -300);
  await expect.poll(() => level(page)).toBeGreaterThan(150);
  expect((await image.boundingBox())!.width).toBeGreaterThan(fitted * 1.5);
  if (shots) await page.screenshot({ path: shots + '/image-zoomed.png' });
  await image.dblclick();
  await expect.poll(() => level(page)).toBe(100);
  await page.keyboard.press('+');
  await expect.poll(() => level(page)).toBe(125);
  await page.keyboard.press('0');
  await expect.poll(() => level(page)).toBe(100);

  // A phone: two fingers spread apart zoom in.
  const phone = await browser.newContext({
    baseURL,
    locale: 'en-GB',
    viewport: { width: 412, height: 860 },
    hasTouch: true,
    isMobile: true,
  });
  const mobile = await phone.newPage();
  expect((await mobile.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  await mobile.goto('/library');
  await mobile.getByRole('button', { name: 'photo.png', exact: true }).click();
  const small = mobile.getByRole('dialog', { name: 'Preview: photo.png' });
  await expect
    .poll(() =>
      small
        .getByRole('img', { name: 'photo.png' })
        .evaluate((i: HTMLImageElement) => i.style.opacity),
    )
    .toBe('');
  if (shots) await mobile.screenshot({ path: shots + '/phone-fit.png' });
  const target = (await small.locator('.zoom-area').boundingBox())!;
  const cx = target.x + target.width / 2;
  const cy = target.y + target.height / 2;
  const cdp = await phone.newCDPSession(mobile);
  const touch = (type: 'touchStart' | 'touchMove' | 'touchEnd', spread: number) =>
    cdp.send('Input.dispatchTouchEvent', {
      type,
      touchPoints:
        type === 'touchEnd'
          ? []
          : [
              { x: cx - spread, y: cy, id: 1 },
              { x: cx + spread, y: cy, id: 2 },
            ],
    });
  await touch('touchStart', 30);
  for (let spread = 40; spread <= 120; spread += 10) await touch('touchMove', spread);
  await touch('touchEnd', 0);
  await expect.poll(() => level(mobile)).toBeGreaterThan(250);
  if (shots) await mobile.screenshot({ path: shots + '/phone-pinched.png' });
  await small.getByRole('button', { name: 'Full screen' }).click();
  await expect(small).toHaveClass(/is-full/);
  if (shots) await mobile.screenshot({ path: shots + '/phone-full.png' });
  await phone.close();
});
