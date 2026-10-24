import { it, expect } from 'vitest';
import { responseData } from './api';
import { setLanguage } from './i18n';
import contract from '../backend-errors-contract.json';

it('localizes every real backend error code in English and Swedish', async () => {
  for (const language of ['en', 'sv']) {
    setLanguage(language);
    for (const [code, messages] of Object.entries(contract)) {
      const response = {
        ok: false,
        status: 400,
        json: async () => ({ error: messages[0], error_code: code }),
      } as Response;
      await expect(responseData(response), code + ' ' + language).rejects.toThrow(
        messages[language === 'en' ? 0 : 1],
      );
    }
  }
});
