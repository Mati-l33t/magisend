import { test, expect, type Page } from '@playwright/test';

// Renders every page with a stubbed API (read-only, no accounts or data) and
// checks that each viewport and theme fits without horizontal scrolling.
// Pages render in the default look here, not the original one the flows use.
test.use({ storageState: { cookies: [], origins: [] } });

const user = { id: 'u1', username: 'layout', role: 'admin', quota: 1073741824, used: 52428800 };
const policy = {
  registration_open: false,
  default_quota: 1073741824,
  max_file: 1073741824,
  max_share: 2147483648,
  default_days: 7,
  storage_budget: 4294967296,
};
const caps = {
  folders: true,
  upload: true,
  share: true,
  edit_own: true,
  grant_edit: false,
  edit_shared: false,
  add_shared: false,
  delete_shared: false,
};
const files = [
  {
    id: 'f1',
    name: 'quarterly-report-final-version.txt',
    size: 2048,
    offset: 2048,
    backend: 'local',
    has_thumb: false,
  },
  {
    id: 'f2',
    name: 'photo.png',
    size: 1048576,
    offset: 1048576,
    backend: 'local',
    has_thumb: false,
  },
];
const share = {
  id: 'a'.repeat(48),
  title: 'Project handover',
  description: 'Everything needed for the next phase.',
  slug: '',
  expires: 0,
  created: 1700000000,
  published: true,
  protected: true,
  files,
};

async function stub(page: Page, signedIn: boolean) {
  await page.route('**/api/**', async (route) => {
    expect(route.request().method()).toBe('GET');
    const path = new URL(route.request().url()).pathname;
    const json: Record<string, unknown> = {
      '/api/status': { setup_required: false, registration_open: true },
      '/api/me': { user, policy },
      '/api/shares': [share],
      '/api/folders': [{ id: 'd1', name: 'Projects' }],
      '/api/library/files': files.map((f, i) => ({
        ...f,
        folder_id: i ? '' : 'd1',
        share_id: 's1',
      })),
      '/api/capabilities': caps,
      '/api/admin/users': [user, { ...user, id: 'u2', username: 'alex', role: 'user', used: 0 }],
      '/api/admin/capabilities': { defaults: caps, overrides: { u2: { upload: false } } },
      '/api/admin/storage': {
        backend: 'local',
        reserved_bytes: 52428800,
        capacity_bytes: 4294967296,
        disk_free_bytes: 8589934592,
      },
      ['/api/public/' + share.id]: share,
      ['/api/public/' + share.id + '/permissions']: {
        authenticated: false,
        edit: false,
        add: false,
        delete: false,
      },
    };
    if (path === '/api/me' && !signedIn)
      return route.fulfill({ status: 401, json: { error: 'Authentication required' } });
    if (path in json) return route.fulfill({ json: json[path] });
    return route.fulfill({ status: 404, json: { error: 'Not found', error_code: 'not_found' } });
  });
}

async function fits(page: Page, label: string) {
  await page.evaluate(() => document.fonts.ready);
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow, label + ' scrolls horizontally').toBeLessThanOrEqual(0);
}

for (const width of [320, 390, 768, 1024, 1440])
  for (const theme of ['dark', 'light'])
    test(`every page fits at ${width}px in ${theme}`, async ({ page }) => {
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((value) => {
        localStorage.setItem('magisend-theme', value);
        localStorage.setItem('magisend-language', 'en');
      }, theme);

      await stub(page, false);
      await page.goto('/');
      await expect(page.getByRole('heading', { name: 'Welcome back.' })).toBeVisible();
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
      await fits(page, 'login');

      await page.goto('/s/' + share.id);
      await expect(page.getByRole('heading', { name: share.title })).toBeVisible();
      await fits(page, 'public share');

      await page.unrouteAll();
      await stub(page, true);
      await page.goto('/');
      for (const [tab, heading] of [
        ['File library', 'File library'],
        ['My shares', 'Your shares.'],
        ['Upload requests', 'Upload requests'],
        ['Send files', 'From you. To anyone.'],
        // Last: on wide screens Administration replaces the workspace menu.
        ['Administration', 'Administration.'],
      ]) {
        await page.getByRole('button', { name: new RegExp('^' + tab) }).click();
        await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible();
        await fits(page, tab);
      }
      expect(errors).toEqual([]);
    });

test('search finds pages, links and files; the Studio menu expands', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'studio');
  });
  await stub(page, true);
  await page.goto('/send');

  // The menu starts as an icon rail and remembers when it is expanded.
  const toggle = page.getByRole('button', { name: 'Expand menu' });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(page.locator('.app-layout')).toHaveAttribute('data-rail', 'open');
  await page.reload();
  await expect(page.getByRole('button', { name: 'Collapse menu' })).toBeVisible();

  await page.keyboard.press('Control+k');
  const search = page.getByRole('combobox', { name: 'Search' });
  await expect(search).toBeFocused();
  await search.fill('upload req');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/requests$/);

  await page.getByRole('button', { name: 'Search' }).click();
  await search.fill('photo');
  await expect(page.getByRole('option', { name: /photo\.png/ })).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/library\/unfiled$/);
  await expect(page.locator('[data-file-id="f2"]')).toHaveClass(/just-found/);

  await page.keyboard.press('Control+k');
  await search.fill('handover');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/shares$/);
  await expect(page.getByRole('dialog')).toBeVisible();
});

test('select all picks every file and delete labels follow the count', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(() => localStorage.setItem('magisend-language', 'en'));
  await stub(page, true);
  await page.goto('/library');

  await page.getByRole('button', { name: 'Select all 2' }).click();
  const bar = page.getByRole('region', { name: 'Selected files' });
  await expect(bar.getByText('2 selected')).toBeVisible();
  await expect(bar.getByRole('button', { name: /Select all/ })).toHaveCount(0);
  await bar.getByRole('button', { name: 'Delete 2 files' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete 2 files' });
  await expect(dialog.getByText('The 2 selected files')).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  await page.getByLabel('Select photo.png').uncheck();
  await expect(bar.getByRole('button', { name: 'Delete file', exact: true })).toBeVisible();
  await expect(bar.getByRole('button', { name: 'Select all 2' })).toBeVisible();
});

test('a file menu in the grid opens above the tiles around it', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  await stub(page, true);
  await page.goto('/library');
  await page.locator('.view-switch button').first().click();
  await page.locator('.file-tile').first().locator('.menu > button').click();
  // A click lands only when nothing covers or clips the item.
  await page.getByRole('button', { name: 'Move to folder', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
});

test('file menus stay on a phone screen in both grid columns', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => {
    localStorage.setItem('magisend-language', 'en');
    localStorage.setItem('magisend-look', 'aurora');
  });
  await stub(page, true);
  await page.goto('/library');
  await page.locator('.view-switch button').first().click();
  const tiles = page.locator('.file-tile');
  for (const tile of [tiles.first(), tiles.nth(1)]) {
    await tile.locator('.menu > button').click();
    await expect(page.locator('.floating-menu')).toBeVisible();
    const box = (await page.locator('.floating-menu').boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
    await page.keyboard.press('Escape');
  }
});
