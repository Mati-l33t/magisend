import { test, expect } from '@playwright/test';
import { mkdtemp, mkdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { docx, xlsx } from './office';
// Real HTTP/backend + browser on a disposable server: PDFs render through
// pdf.js under the app's Content-Security-Policy and audio plays inline.
test.use({ locale: 'en-GB', actionTimeout: 15000 });

function pdf(text: string) {
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    `<< /Length ${44 + text.length} >>\nstream\nBT /F1 24 Tf 40 100 Td (${text}) Tj ET\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
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

function wav() {
  const rate = 8000;
  const samples = rate / 2;
  const buffer = Buffer.alloc(44 + samples * 2);
  buffer.write('RIFF', 0);
  buffer.writeUInt32LE(36 + samples * 2, 4);
  buffer.write('WAVEfmt ', 8);
  buffer.writeUInt32LE(16, 16);
  buffer.writeUInt16LE(1, 20);
  buffer.writeUInt16LE(1, 22);
  buffer.writeUInt32LE(rate, 24);
  buffer.writeUInt32LE(rate * 2, 28);
  buffer.writeUInt16LE(2, 32);
  buffer.writeUInt16LE(16, 34);
  buffer.write('data', 36);
  buffer.writeUInt32LE(samples * 2, 40);
  for (let i = 0; i < samples; i++)
    buffer.writeInt16LE(Math.round(8000 * Math.sin((i * 2 * Math.PI * 440) / rate)), 44 + i * 2);
  return buffer;
}

test('PDF and audio previews in the library', async ({ page }) => {
  const credentials = { username: 'viewer', password: 'disposable preview passphrase' };
  const headers = { 'X-MagiSend': '1' };
  expect((await page.request.post('/api/setup', { data: credentials, headers })).status()).toBe(
    201,
  );
  expect((await page.request.post('/api/login', { data: credentials, headers })).status()).toBe(
    200,
  );
  const violations: string[] = [];
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) violations.push(m.text());
  });
  await page.goto('/library');
  await page.getByLabel('Upload private files').setInputFiles([
    { name: 'report.pdf', mimeType: 'application/pdf', buffer: pdf('Quarterly report') },
    { name: 'tone.wav', mimeType: 'audio/wav', buffer: wav() },
    {
      name: 'logo.svg',
      mimeType: 'image/svg+xml',
      buffer: Buffer.from(
        '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="#c00"/></svg>',
      ),
    },
    { name: 'notes.docx', mimeType: 'application/octet-stream', buffer: Buffer.from('not a doc') },
  ]);
  // The browser draws pictures for the PDF and the SVG; other files show the
  // icon of their kind.
  const row = (name: string) => page.locator('[data-file-id]').filter({ hasText: name });
  for (const name of ['report.pdf', 'logo.svg'])
    await expect
      .poll(() =>
        row(name)
          .locator('.file-thumb img')
          .evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth)
          .catch(() => 0),
      )
      .toBeGreaterThan(0);
  await expect(row('tone.wav').locator('.file-icon.kind-audio')).toBeVisible();
  await expect(row('notes.docx').locator('.file-icon.kind-doc')).toBeVisible();
  const stored = await (await page.request.get('/api/library/files')).json();
  expect(
    stored
      .filter((f: { has_thumb: boolean }) => f.has_thumb)
      .map((f: { name: string }) => f.name)
      .sort(),
  ).toEqual(['logo.svg', 'report.pdf']);
  await page.getByRole('button', { name: 'report.pdf', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Preview: report.pdf' });
  const canvas = dialog.locator('.pdf-pages canvas');
  await expect(canvas).toHaveCount(1);
  // The page was actually painted: not every pixel is white.
  const painted = await canvas.evaluate((c: HTMLCanvasElement) => {
    const data = c.getContext('2d')!.getImageData(0, 0, c.width, c.height).data;
    for (let i = 0; i < data.length; i += 4) if (data[i] < 128) return true;
    return false;
  });
  expect(painted).toBe(true);
  await dialog.getByRole('button', { name: 'Close' }).click();

  await page.getByRole('button', { name: 'tone.wav', exact: true }).click();
  const audio = page.getByRole('dialog').locator('audio');
  await expect
    .poll(() => audio.evaluate((a: HTMLAudioElement) => Math.round(a.duration * 10) / 10))
    .toBe(0.5);
  await page.getByRole('dialog').getByRole('button', { name: 'Close' }).click();

  // Word and Excel files open in the viewer, read in the browser.
  await page.getByLabel('Upload private files').setInputFiles([
    {
      name: 'letter.docx',
      mimeType: 'application/octet-stream',
      buffer: await docx('Dear reader', 'Plain words in a Word file.', 'https://example.com/'),
    },
    { name: 'budget.xlsx', mimeType: 'application/octet-stream', buffer: await xlsx() },
  ]);
  await page.getByRole('button', { name: 'letter.docx', exact: true }).click();
  const letter = page.getByRole('dialog', { name: 'Preview: letter.docx' });
  await expect(letter.getByText('Dear reader')).toBeVisible();
  await expect(letter.getByText('Plain words in a Word file.')).toBeVisible();
  // The document's own Heading 1 style applies: blue and bold.
  await expect(letter.getByText('Dear reader')).toHaveCSS('color', 'rgb(46, 116, 181)');
  await expect(letter.getByText('Dear reader')).toHaveCSS('font-weight', '700');
  await expect(letter.getByRole('cell', { name: 'Sales' })).toBeVisible();
  const link = letter.getByRole('link', { name: 'a link' });
  await expect(link).toHaveAttribute('target', '_blank');
  await expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  // The document's styles stay inside it.
  expect(
    await letter
      .locator('.docx-host')
      .evaluate((host) => host.shadowRoot!.adoptedStyleSheets.length),
  ).toBe(1);
  await letter.getByRole('button', { name: 'Close' }).click();

  await page.getByRole('button', { name: 'budget.xlsx', exact: true }).click();
  const budget = page.getByRole('dialog', { name: 'Preview: budget.xlsx' });
  await expect(budget.getByRole('tab', { name: 'Budget' })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(budget.getByRole('tab', { name: 'Secret' })).toHaveCount(0);
  await expect(budget.getByRole('cell', { name: 'Rent (May)' })).toBeVisible();
  await expect(budget.getByRole('cell', { name: '1,234.50' }).first()).toBeVisible();
  await expect(budget.getByRole('cell', { name: 'Merged note' })).toHaveAttribute('colspan', '3');
  await budget.getByRole('tab', { name: 'Notes' }).click();
  await expect(budget.getByRole('cell', { name: 'Second sheet' })).toBeVisible();
  expect(violations).toEqual([]);
  await budget.getByRole('button', { name: 'Close' }).click();

  // A chosen folder keeps its subfolders.
  const root = await mkdtemp(join(tmpdir(), 'magisend-folder-'));
  const trip = join(root, 'Trip');
  await mkdir(join(trip, 'day2'), { recursive: true });
  await writeFile(join(trip, 'map.txt'), 'route');
  await writeFile(join(trip, 'day2', 'map.txt'), 'second route');
  await writeFile(join(trip, '.DS_Store'), 'clutter');
  await page.getByLabel('Upload folder').setInputFiles(trip);
  const bar = page.locator('.library-bar h2');
  await expect(bar).toHaveText('Trip');
  await expect(page.getByText('map.txt', { exact: true })).toBeVisible();
  await expect(page.getByText('.DS_Store')).toHaveCount(0);
  const tripPath = new URL(page.url()).pathname;
  expect(tripPath).toMatch(/^\/library\/[a-f0-9]+$/);
  await page.locator('.folder-row').getByRole('button', { name: 'day2' }).click();
  await expect(bar).toHaveText('Trip/day2');
  await expect(page.getByText('map.txt', { exact: true })).toBeVisible();
  await bar.getByRole('button', { name: 'Trip' }).click();
  expect(new URL(page.url()).pathname).toBe(tripPath);

  // Another folder chosen while Trip is open becomes a folder inside Trip.
  const extra = join(root, 'Extra');
  await mkdir(extra);
  await writeFile(join(extra, 'notes.txt'), 'more');
  await page.getByLabel('Upload folder').setInputFiles(extra);
  await expect(bar).toHaveText('Trip/Extra');
  await expect(page.getByText('notes.txt', { exact: true })).toBeVisible();

  // The recipient of a shared folder opens its subfolders the same way.
  const created = await page.request.post('/api/library/shares', {
    data: { title: 'Trip', folder_id: tripPath.split('/').pop(), days: 1 },
    headers: { 'X-MagiSend': '1' },
  });
  expect(created.status()).toBe(201);
  await page.goto('/s/' + (await created.json()).id);
  const crumbs = page.locator('.public-crumbs');
  await expect(crumbs).toHaveText('Trip');
  await expect(page.getByText('map.txt', { exact: true })).toHaveCount(1);
  await expect(page.getByText('notes.txt', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Extra', exact: true }).click();
  await expect(crumbs).toHaveText('Trip/Extra');
  await expect(page.getByText('notes.txt', { exact: true })).toBeVisible();
  await expect(page.getByText('map.txt', { exact: true })).toHaveCount(0);
  await crumbs.getByRole('button', { name: 'Trip' }).click();
  await page.getByRole('button', { name: 'day2', exact: true }).click();
  await expect(crumbs).toHaveText('Trip/day2');
  await expect(page.getByText('map.txt', { exact: true })).toBeVisible();
});
