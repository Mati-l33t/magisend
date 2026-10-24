import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
// Real HTTP/backend + browser on a disposable server: a Markdown file opens as
// a formatted note, and the help explains the page that is on screen.
test.use({ locale: 'en-GB' });

test('Markdown preview, ten-at-a-time lists and help that follows the page', async ({ page }) => {
  const credentials = { username: 'keeper', password: 'disposable helper passphrase' };
  const headers = { 'X-MagiSend': '1' };
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
  const notes = '# Notes\n\n<b>not bold</b> åäö\n\n```js\nconst a = 1;\n```\n';
  await page
    .getByLabel('Upload private files')
    .setInputFiles([{ name: 'notes.md', mimeType: 'text/markdown', buffer: Buffer.from(notes) }]);
  await page.getByRole('button', { name: 'notes.md', exact: true }).click();
  // Formatted by default; HTML in the file stays text.
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('heading', { name: 'Notes' })).toBeVisible();
  await expect(dialog.locator('.markdown-body b')).toHaveCount(0);
  await expect(dialog.locator('.markdown-body')).toContainText('<b>not bold</b>');
  await expect(dialog.locator('.markdown-body .code-block .hljs-keyword')).toHaveText('const');
  await expect(dialog.getByRole('button', { name: 'Copy', exact: true })).toHaveCount(1);
  // In the library a Markdown file opens as a note: Write shows the text itself.
  await dialog.getByRole('radio', { name: 'Write' }).check();
  await expect(dialog.getByLabel('Note', { exact: true })).toHaveValue(notes);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);

  // Code: coloured, one row per line, wrapping switchable.
  const script = 'def hello():\n    return "hi"  # greet\n';
  await page
    .getByLabel('Upload private files')
    .setInputFiles([{ name: 'hello.py', mimeType: 'text/x-python', buffer: Buffer.from(script) }]);
  await page.getByRole('button', { name: 'hello.py', exact: true }).click();
  await expect(dialog.locator('.code-line')).toHaveCount(2);
  await expect(dialog.locator('.hljs-keyword').first()).toHaveText('def');
  await expect(dialog.locator('.hljs-comment')).toHaveText('# greet');
  const wrap = dialog.getByRole('button', { name: 'Wrap long lines' });
  await expect(wrap).toHaveAttribute('aria-pressed', 'false');
  await wrap.click();
  await expect(dialog.locator('.code-view.wrap')).toHaveCount(1);
  await page.keyboard.press('Escape');

  // Long lists show ten at a time.
  await page.getByLabel('Upload private files').setInputFiles(
    Array.from({ length: 11 }, (_, i) => ({
      name: 'file-' + i + '.txt',
      mimeType: 'text/plain',
      buffer: Buffer.from('file ' + i),
    })),
  );
  await expect(page.getByRole('button', { name: 'Show 3 more' })).toBeVisible({ timeout: 20000 });
  await expect(page.locator('.library-row')).toHaveCount(10);
  await page.getByRole('button', { name: 'Show 3 more' }).click();
  await expect(page.locator('.library-row')).toHaveCount(13);
  await expect(page.getByRole('button', { name: 'Back to top' }).first()).toBeVisible();

  await page.getByRole('button', { name: 'Help for this page' }).click();
  const help = page.getByRole('dialog', { name: 'File library' });
  await expect(help.getByText('Folders and stars')).toBeVisible();
  await page.keyboard.press('Escape');
  await page.goto('/admin');
  await page.getByRole('button', { name: 'Help for this page' }).click();
  await expect(page.getByRole('dialog', { name: 'Administration' })).toBeVisible();
});
