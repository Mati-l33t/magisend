import { test, expect } from '@playwright/test';
import { setupCode } from './setup';
import { choose } from './choose';

const headers = { 'X-MagiSend': '1' };

test('notes are written, titled, ticked off and shared', async ({ page }) => {
  const credentials = { username: 'noter', password: 'disposable notes passphrase' };
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
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  await page.goto('/library');
  await page.getByRole('button', { name: 'New note' }).first().click();
  const editor = page.locator('dialog.note-editor');
  await editor.getByLabel('Note', { exact: true }).fill('# Groceries\n- [ ] milk\n- [ ] bread');
  await expect(editor.getByRole('status')).toHaveText('Saved');
  await editor.getByRole('button', { name: 'Close' }).click();
  await expect(editor).toHaveCount(0);

  // The title came from the first line; the list shows the opening.
  await page
    .locator('.folder-list')
    .getByRole('button', { name: /^Notes/ })
    .click();
  await expect(page).toHaveURL(/\/library\/notes$/);
  const card = page.locator('.note-card').filter({ hasText: 'Groceries' });
  await expect(card.locator('.note-line.task.open')).toHaveText(['milk', 'bread']);
  await expect(card.locator('.note-where')).toHaveText('Unfiled');
  await card.locator('.note-open').click();
  await editor.locator('[data-task-line="1"]').click();
  await expect(editor.getByRole('status')).toHaveText('Saved');
  await editor.getByRole('radio', { name: 'Write' }).check();
  await expect(editor.getByLabel('Note', { exact: true })).toHaveValue(
    '# Groceries\n- [x] milk\n- [ ] bread',
  );
  await editor.getByLabel('Title').fill('Weekly shop');
  await expect(editor.getByRole('status')).toHaveText('Saved');
  await editor.getByRole('button', { name: 'Close' }).click();
  const shop = page.locator('.note-card').filter({ hasText: 'Weekly shop' });
  await expect(shop.locator('.note-line.task.done')).toHaveText(['milk']);

  // Moved to a folder from the notes list.
  expect(
    (await page.request.post('/api/folders', { data: { name: 'Home' }, headers })).status(),
  ).toBe(201);
  await page.reload();
  await shop.getByRole('button', { name: 'More actions for Weekly shop.md' }).click();
  await page.getByRole('button', { name: 'Move to folder', exact: true }).click();
  await choose(page.getByRole('dialog'), 'Move Weekly shop.md', 'Home');
  await page.getByRole('dialog').getByRole('button', { name: 'Move', exact: true }).click();
  await expect(shop.locator('.note-where')).toHaveText('Home');

  // A link to the note opens as the note.
  const notes = await (await page.request.get('/api/library/notes', { headers })).json();
  expect(notes[0].name).toBe('Weekly shop.md');
  const link = await (
    await page.request.post('/api/library/shares', { data: { files: [notes[0].id] }, headers })
  ).json();
  await page.goto('/s/' + link.id);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Weekly shop');
  await expect(page.locator('.public-note [data-task-line="1"]')).toBeChecked();
  await expect(page.locator('.public-note [data-task-line="1"]')).toBeDisabled();

  // Changed elsewhere meanwhile: closing with unsaved text keeps the editor
  // open until the text is discarded.
  await page.goto('/library/notes');
  await shop.locator('.note-open').click();
  await editor.getByRole('radio', { name: 'Write' }).check();
  const state = await (
    await page.request.get('/api/library/files/' + notes[0].id + '/text', { headers })
  ).json();
  expect(
    (
      await page.request.put('/api/library/notes/' + notes[0].id, {
        data: {
          version: state.version,
          title: 'Weekly shop',
          text: 'from elsewhere',
          language: 'en',
        },
        headers,
      })
    ).status(),
  ).toBe(200);
  await editor.getByLabel('Note', { exact: true }).fill('written here');
  // A second click while the first close saves does not close past it.
  await editor.getByRole('button', { name: 'Close' }).dblclick();
  await expect(editor.getByRole('status')).toHaveText(/Changed somewhere else/);
  await expect(editor.getByText(/could not be saved/)).toBeVisible();
  await expect(editor).toHaveCount(1);
  await expect(editor.getByLabel('Note', { exact: true })).toHaveValue('written here');
  // Escape tries the save again, and the text stays.
  const retried = page.waitForResponse(
    (r) => r.url().includes('/api/library/notes/') && r.request().method() === 'PUT',
  );
  await page.keyboard.press('Escape');
  expect((await retried).status()).toBe(409);
  await expect(editor.getByText(/could not be saved/)).toBeVisible();
  await expect(editor.getByLabel('Note', { exact: true })).toHaveValue('written here');
  await editor.getByRole('button', { name: 'Discard and close' }).click();
  await expect(editor).toHaveCount(0);
});
