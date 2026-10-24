import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { choose } from './choose';
// Real HTTP/backend + browser. Each test owns its accounts/folder/files and can
// run alone with --grep. Never run this config against production or preview.
const password = 'recipient-test-passphrase';
test.use({ locale: 'en-GB', actionTimeout: 10000, extraHTTPHeaders: { 'X-MagiSend': '1' } });
const ownerName = 'qaowner',
  ownerPassword = 'qa-disposable-passphrase';
let owner: APIRequestContext;
let sequence = 0;
const unique = () => `u${Date.now()}${++sequence}`;
async function json(api: APIRequestContext, path: string, data?: unknown, method = 'GET') {
  const response = await api.fetch(path, { method, ...(data === undefined ? {} : { data }) });
  expect(response.ok(), `${method} ${path}: ${response.status()} ${await response.text()}`).toBe(
    true,
  );
  return response.json();
}
async function account() {
  const username = unique();
  await json(owner, '/api/admin/users', { username, password, quota: 1073741824 }, 'POST');
  const users = await json(owner, '/api/admin/users');
  return users.find((u: { username: string }) => u.username === username) as {
    id: string;
    username: string;
  };
}
async function scenario(permissions = { edit: false, add: false, delete: false }) {
  const recipient = await account();
  await json(
    owner,
    '/api/admin/capabilities/users/' + recipient.id,
    { edit_shared: true, add_shared: true, delete_shared: true },
    'PUT',
  );
  const folder = await json(owner, '/api/folders', { name: unique() }, 'POST');
  const upload = await json(
    owner,
    '/api/library/uploads',
    { folder_id: folder.id, files: [{ name: 'notes.md', size: 3 }] },
    'POST',
  );
  const file = upload.files[0];
  const chunk = await owner.patch('/api/uploads/' + file.id, {
    headers: { 'Upload-Offset': '0', 'Content-Type': 'application/octet-stream' },
    data: Buffer.from('one'),
  });
  expect(chunk.status()).toBe(200);
  await json(owner, '/api/shares/' + upload.id + '/publish', {}, 'POST');
  const share = await json(
    owner,
    '/api/library/shares',
    { folder_id: folder.id, recipients: [recipient.username], ...permissions },
    'POST',
  );
  return { recipient, folder, file, share };
}
async function signInOnLink(page: Page, id: string, username: string) {
  await page.goto('/s/' + id);
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Log in', exact: true })).toHaveCount(0);
  expect((await json(page.request, '/api/public/' + id + '/permissions')).authenticated).toBe(true);
}
async function ownerPage(page: Page) {
  await json(page.request, '/api/login', { username: ownerName, password: ownerPassword }, 'POST');
  await page.goto('/');
  await page.getByRole('button', { name: 'File library', exact: true }).click();
}
test.beforeAll(async ({ playwright, baseURL }) => {
  if (baseURL !== 'http://127.0.0.1:18087') throw Error('Disposable QA origin required');
  owner = await playwright.request.newContext({ baseURL, extraHTTPHeaders: { 'X-MagiSend': '1' } });
  if ((await json(owner, '/api/status')).setup_required)
    await json(owner, '/api/setup', { username: ownerName, password: ownerPassword }, 'POST');
  await json(owner, '/api/login', { username: ownerName, password: ownerPassword }, 'POST');
  const users = await json(owner, '/api/admin/users');
  const admin = users.find((u: { username: string }) => u.username === ownerName);
  await json(
    owner,
    '/api/admin/capabilities/users/' + admin.id,
    { edit_own: true, grant_edit: true },
    'PUT',
  );
  await mkdir('test-results/screens', { recursive: true });
});
test.afterAll(async () => {
  await owner?.dispose();
});
test.beforeEach(async ({ page }) => {
  page.setDefaultTimeout(10000);
});

test('private browser upload, selection versus live membership, rename and move', async ({
  page,
}) => {
  const s = await scenario();
  await ownerPage(page);
  await page
    .getByRole('navigation', { name: 'Folders' })
    .getByRole('button', { name: s.folder.name, exact: true })
    .click();
  await page
    .getByLabel('Upload private files', { exact: true })
    .setInputFiles({ name: 'extra.txt', mimeType: 'text/plain', buffer: Buffer.from('two') });
  await expect(page.getByText('extra.txt', { exact: true })).toBeVisible();
  const files = await json(owner, '/api/library/files');
  const extra = files.find(
    (f: { name: string; folder_id: string }) =>
      f.name === 'extra.txt' && f.folder_id === s.folder.id,
  );
  expect((await page.request.get('/api/public/' + extra.share_id)).status()).toBe(404);
  expect(
    (await json(owner, '/api/shares')).some((sh: { id: string }) => sh.id === extra.share_id),
  ).toBe(false);
  const selection = await json(owner, '/api/library/shares', { files: [s.file.id] }, 'POST');
  const row = page
    .locator('.library-row')
    .filter({ has: page.getByText('notes.md', { exact: true }) });
  await row.getByRole('button', { name: 'More actions for notes.md', exact: true }).click();
  await page.getByRole('button', { name: 'Rename file', exact: true }).click();
  await page.getByRole('dialog').getByLabel('File name', { exact: true }).fill('renamed.md');
  await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('button', { name: 'More actions for renamed.md', exact: true }).click();
  await page.getByRole('button', { name: 'Move to folder', exact: true }).click();
  await choose(page.getByRole('dialog'), 'Move renamed.md', 'Unfiled');
  await page.getByRole('dialog').getByRole('button', { name: 'Move', exact: true }).click();
  await expect(page.getByText('renamed.md', { exact: true })).toHaveCount(0);
  const live = await json(owner, '/api/public/' + s.share.id);
  expect(live.files.map((f: { name: string }) => f.name)).toEqual(['extra.txt']);
  const fixed = await json(owner, '/api/public/' + selection.id);
  expect(fixed.files[0]).toMatchObject({ id: s.file.id, name: 'renamed.md' });
});

test('recipient signs in at shared URL, edits and restores; outsider and anonymous writes denied', async ({
  page,
  browser,
  baseURL,
}) => {
  const s = await scenario({ edit: true, add: false, delete: false });
  const path = '/api/public/' + s.share.id + '/files/' + s.file.id;
  expect(
    (await page.request.put(path + '/text', { data: { version: 1, text: 'intrusion' } })).status(),
  ).toBe(401);
  await signInOnLink(page, s.share.id, s.recipient.username);
  await page.getByRole('button', { name: 'Edit notes.md', exact: true }).click();
  await expect(page.getByRole('dialog').getByLabel('Plain text')).toHaveValue('one');
  await page.getByRole('dialog').getByLabel('Plain text').fill('two');
  await page.getByRole('dialog').getByRole('button', { name: 'Save text', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(await json(page.request, path + '/text')).toMatchObject({ text: 'two', version: 2 });
  await page.getByRole('button', { name: 'Edit notes.md', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.locator('summary').click();
  const original = dialog.locator('.storage-detail').filter({ hasText: /^Version 1 ·/ });
  await original.getByRole('button', { name: 'Restore', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(await json(page.request, path + '/text')).toMatchObject({ text: 'one', version: 3 });
  const outsider = await account();
  const ctx = await browser.newContext({
    baseURL,
    extraHTTPHeaders: { 'X-MagiSend': '1' },
    locale: 'en-GB',
  });
  try {
    const other = await ctx.newPage();
    await signInOnLink(other, s.share.id, outsider.username);
    expect(
      (
        await other.request.put(path + '/text', { data: { version: 3, text: 'intrusion' } })
      ).status(),
    ).toBe(403);
    expect((await json(other.request, path + '/text')).text).toBe('one');
  } finally {
    await ctx.close();
  }
});

test('recipient actually adds and deletes files, with separate link opt-ins', async ({ page }) => {
  const s = await scenario({ edit: false, add: true, delete: false });
  await signInOnLink(page, s.share.id, s.recipient.username);
  await expect(
    page.getByRole('button', { name: 'Permanently delete notes.md', exact: true }),
  ).toHaveCount(0);
  expect(
    (await page.request.delete('/api/public/' + s.share.id + '/files/' + s.file.id)).status(),
  ).toBe(403);
  await page.locator('input[type=file]').setInputFiles({
    name: 'recipient.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('added by recipient'),
  });
  await expect(page.getByRole('button', { name: 'recipient.txt', exact: true })).toBeVisible();
  const added = (await json(owner, '/api/public/' + s.share.id)).files.find(
    (f: { name: string }) => f.name === 'recipient.txt',
  );
  expect(added).toBeTruthy();
  await json(owner, '/api/library/shares/' + s.share.id, { add: false, delete: true }, 'PATCH');
  await page.reload();
  await expect(page.getByRole('button', { name: /Add shared file/ })).toHaveCount(0);
  expect(
    (
      await page.request.post('/api/public/' + s.share.id + '/files?name=denied.txt', {
        data: Buffer.from('no'),
      })
    ).status(),
  ).toBe(403);
  await page.getByRole('button', { name: 'Permanently delete recipient.txt', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete share', exact: true }).click();
  await expect(page.getByRole('button', { name: 'recipient.txt', exact: true })).toHaveCount(0);
  expect((await owner.get('/api/library/files/' + added.id + '/download')).status()).toBe(404);
  expect(
    (await json(owner, '/api/public/' + s.share.id)).files.map((f: { id: string }) => f.id),
  ).toEqual([s.file.id]);
});

test('existing recipient session loses write access immediately on per-user revocation', async ({
  page,
}) => {
  const s = await scenario({ edit: true, add: true, delete: true });
  await signInOnLink(page, s.share.id, s.recipient.username);
  const endpoint = '/api/public/' + s.share.id + '/files/' + s.file.id;
  await json(
    owner,
    '/api/admin/capabilities/users/' + s.recipient.id,
    { edit_shared: null, add_shared: null, delete_shared: null },
    'PUT',
  );
  expect(
    (
      await page.request.put(endpoint + '/text', { data: { version: 1, text: 'revoked' } })
    ).status(),
  ).toBe(403);
  expect(
    (
      await page.request.post('/api/public/' + s.share.id + '/files?name=revoked.txt', {
        data: Buffer.from('no'),
      })
    ).status(),
  ).toBe(403);
  expect((await page.request.delete(endpoint)).status()).toBe(403);
  expect(await json(page.request, endpoint + '/text')).toMatchObject({ version: 1, text: 'one' });
  await page.reload();
  await expect(page.getByRole('button', { name: 'Edit notes.md', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Add shared file/ })).toHaveCount(0);
  await expect(
    page.getByRole('button', { name: 'Permanently delete notes.md', exact: true }),
  ).toHaveCount(0);
});

test('invalid recipient creates no link; retry uses one atomic POST and no PATCH', async ({
  page,
}) => {
  const s = await scenario();
  await ownerPage(page);
  await page
    .getByRole('navigation', { name: 'Folders' })
    .getByRole('button', { name: s.folder.name, exact: true })
    .click();
  await page.getByLabel('Select notes.md', { exact: true }).check();
  await page.getByRole('button', { name: 'Share selected files', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel('Allow text edits')).not.toBeChecked();
  await dialog.getByLabel('Recipient usernames').fill('missing-' + unique());
  await dialog.getByLabel('Allow text edits').check();
  const before = (await json(owner, '/api/shares')).length;
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(dialog.getByRole('alert')).toBeVisible();
  expect((await json(owner, '/api/shares')).length).toBe(before);
  await dialog.getByLabel('Recipient usernames').fill(s.recipient.username);
  const writes: string[] = [];
  page.on('request', (r) => {
    if (r.method() !== 'GET' && r.url().includes('/api/library/shares')) writes.push(r.method());
  });
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(dialog.getByRole('textbox', { name: 'Share link' })).toHaveValue(/\/s\//);
  expect(writes).toEqual(['POST']);
  expect((await json(owner, '/api/shares')).length).toBe(before + 1);
});

test('all eight admin overrides autosave; real app dark/light themes fit desktop and mobile', async ({
  page,
}) => {
  const s = { recipient: await account() };
  await ownerPage(page);
  await page.getByRole('button', { name: 'Administration', exact: true }).click();
  await page
    .getByRole('button', { name: 'Permissions for ' + s.recipient.username, exact: true })
    .click();
  const names = [
    'Create folders',
    'Upload files',
    'Create share links',
    'Edit own files',
    'Grant edit rights',
    'Edit shared text',
    'Add files to shared',
    'Delete shared files',
  ];
  const keys = [
    'folders',
    'upload',
    'share',
    'edit_own',
    'grant_edit',
    'edit_shared',
    'add_shared',
    'delete_shared',
  ];
  for (let i = 0; i < names.length; i++) {
    const group = page.getByRole('radiogroup', {
      name: names[i] + ': ' + s.recipient.username,
      exact: true,
    });
    const wanted = (await group.getByRole('radio', { name: 'Allow', exact: true }).isChecked())
      ? 'off'
      : 'on';
    const radio = group.getByRole('radio', {
      name: wanted === 'on' ? 'Allow' : 'Deny',
      exact: true,
    });
    await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().endsWith('/api/admin/capabilities/users/' + s.recipient.id) &&
          r.request().method() === 'PUT' &&
          r.status() === 200,
      ),
      radio.check(),
    ]);
    await expect(radio).toBeEnabled();
    await expect(radio).toBeChecked();
    expect((await json(owner, '/api/admin/capabilities')).overrides[s.recipient.id][keys[i]]).toBe(
      wanted === 'on',
    );
  }
  await expect(
    page.getByRole('button', { name: 'Save capabilities: ' + s.recipient.username, exact: true }),
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const theme of ['dark', 'light']) {
      if ((await page.locator('html').getAttribute('data-theme')) !== theme)
        await page
          .getByRole('button', {
            name: theme === 'light' ? 'Switch to light theme' : 'Switch to dark theme',
            exact: true,
          })
          .click();
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: `test-results/screens/admin-${width}-${theme}.png`,
        fullPage: true,
      });
    }
  }
  await page.evaluate(() => localStorage.setItem('magisend-language', 'sv'));
  await page.reload();
  await page.getByRole('button', { name: 'Administration', exact: true }).click();
  await page
    .getByRole('button', { name: 'Behörigheter för ' + s.recipient.username, exact: true })
    .click();
  await expect(
    page.getByRole('radiogroup', {
      name: 'Redigera delad text: ' + s.recipient.username,
      exact: true,
    }),
  ).toBeVisible();
});
