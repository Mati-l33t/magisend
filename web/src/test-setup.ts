import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, beforeEach, vi } from 'vitest';
import { initializeLanguage } from './i18n';
import { applyLook } from './look';
beforeEach(() => {
  vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['sv-SE']);
  vi.spyOn(navigator, 'language', 'get').mockReturnValue('sv-SE');
  initializeLanguage();
  // These tests cover the screens of the original look.
  applyLook('classic');
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  // Views have their own addresses; every test starts on the start page.
  history.replaceState(null, '', '/');
});
