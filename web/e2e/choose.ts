import type { Locator, Page } from '@playwright/test';

// Picks an option in the app's own dropdown (Select.tsx).
export async function choose(scope: Page | Locator, name: string, option: string) {
  await scope.getByRole('combobox', { name, exact: true }).click();
  const page = 'page' in scope ? scope.page() : scope;
  await page.getByRole('option', { name: option, exact: true }).click();
}
