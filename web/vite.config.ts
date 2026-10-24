import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
export default defineConfig({
  plugins: [
    react(),
    {
      name: 'english-metadata',
      transformIndexHtml(html) {
        const en = JSON.parse(
          readFileSync(new URL('./src/locales/en.json', import.meta.url), 'utf8'),
        ) as Record<string, string>;
        const escape = (text: string) =>
          text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');
        return html
          .replaceAll('__MAGISEND_TITLE__', escape(en['metadata.title']))
          .replaceAll('__MAGISEND_DESCRIPTION__', escape(en['metadata.description']));
      },
    },
  ],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    exclude: ['node_modules/**', 'e2e/**', 'scripts/**'],
  },
});
