import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Workspace from './Workspace';
import PublicShare from './PublicShare';
import type { Me, Share } from './api';
import { formatDate, setLanguage, t } from './i18n';
import { choose } from './test-select';

const me: Me = {
  user: { id: 'owner', username: 'mati', role: 'user', quota: 1073741824, used: 0 },
  policy: {
    registration_open: false,
    default_quota: 1073741824,
    max_file: 1073741824,
    max_share: 2147483648,
    default_days: 7,
  },
};
const permanent: Share = {
  id: 'a'.repeat(48),
  title: 'Saved delivery',
  description: '',
  slug: '',
  expires: 0,
  created: 1800000000,
  published: true,
  protected: true,
  files: [
    {
      id: 'b'.repeat(48),
      name: 'holiday.png',
      size: 3,
      offset: 3,
      backend: 'local',
      has_thumb: false,
    },
  ],
};
const dated: Share = {
  ...permanent,
  id: 'd'.repeat(48),
  title: 'Dated delivery',
  expires: 4102444800,
};
// A binary response fixture, not JSON or a filename-based image URL. JSDOM does not decode images.
const imageBytes = new Uint8Array([
  137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0,
  0, 31, 21, 196, 137, 0, 0, 0, 11, 73, 68, 65, 84, 120, 156, 99, 96, 0, 2, 0, 0, 5, 0, 1, 165, 246,
  69, 64, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130,
]);
const json = (data: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  headers: new Headers({ 'Content-Type': 'application/json' }),
  text: async () => JSON.stringify(data),
  json: async () => data,
});
const imageResponse = () => ({
  ok: true,
  status: 200,
  headers: new Headers({ 'Content-Type': 'image/png' }),
  blob: async () => new Blob([imageBytes], { type: 'image/png' }),
});
const copyName = () => t('share.copyNamed', { title: permanent.title });
const never = {
  en: /never expires|permanent/i,
  sv: /upphör aldrig|går aldrig ut|löper aldrig ut|utan (?:utgång|tidsgräns)|permanent/i,
};
const unsupported = {
  en: /not supported|unsupported|cannot be previewed|can't (?:be )?preview|preview (?:is )?(?:unavailable|not available)|no preview/i,
  sv: /stöds inte|kan inte förhandsvisas|förhandsvisning.*(?:inte|saknas)|ingen förhandsvisning/i,
};

async function myShares() {
  // The navigation button includes a count; exact names would fail when that count changes.
  fireEvent.click(screen.getByRole('button', { name: new RegExp(t('workspace.shares')) }));
  return (await screen.findByRole('heading', { name: permanent.title })).closest('article')!;
}
function expectNotEpochOrExpired(element: HTMLElement) {
  expect(element).not.toHaveTextContent(/1970/);
  expect(element).not.toHaveTextContent(t('share.expired'));
}
async function readBytes(blob: Blob) {
  return new Promise<Uint8Array>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(new Uint8Array(reader.result as ArrayBuffer));
    reader.onerror = () => reject(reader.error);
    reader.readAsArrayBuffer(blob);
  });
}
function objectURLs() {
  const create = vi.fn((_blob: Blob) => 'blob:magisend-preview');
  const revoke = vi.fn((_url: string) => {});
  // Preserve URL construction while supplying JSDOM's missing methods.
  vi.stubGlobal(
    'URL',
    class extends URL {
      static createObjectURL = create;
      static revokeObjectURL = revoke;
    },
  );
  return { create, revoke };
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

for (const language of ['en', 'sv'] as const) {
  describe(`permanent shares (${language})`, () => {
    it('selects Never expires and uploads with explicit permanent:true and days:0', async () => {
      setLanguage(language);
      const draft = {
        ...permanent,
        published: false,
        files: [{ ...permanent.files[0], offset: 0 }],
      };
      const fetcher = vi.fn(async (path: string, options: RequestInit = {}) => {
        if (path === '/api/shares') return json(options.method === 'POST' ? draft : []);
        if (path === '/api/uploads/' + draft.files[0].id)
          return json(options.method === 'PATCH' ? { offset: 3 } : draft.files[0]);
        if (path === '/api/shares/' + draft.id + '/publish') return json(permanent);
        throw new Error('Unexpected request: ' + path);
      });
      vi.stubGlobal('fetch', fetcher);
      render(<Workspace me={me} reload={async () => {}} />);
      fireEvent.change(screen.getByLabelText(t('upload.choose')), {
        target: {
          files: [new File(['png'], 'holiday.png', { type: 'image/png', lastModified: 123 })],
        },
      });
      choose(t('share.validity'), never[language]);
      fireEvent.click(screen.getByRole('button', { name: t('share.createLink') }));
      await waitFor(() => {
        const call = fetcher.mock.calls.find(
          ([path, options]) => path === '/api/shares' && options?.method === 'POST',
        );
        expect(call).toBeDefined();
        expect(JSON.parse(String(call![1]?.body))).toMatchObject({
          permanent: true,
          days: 0,
          files: [{ name: 'holiday.png', size: 3 }],
        });
      });
      expect(await screen.findByRole('dialog', { name: t('share.ready') })).toBeVisible();
      expect(fetcher).toHaveBeenCalledWith(
        '/api/uploads/' + draft.files[0].id,
        expect.objectContaining({ method: 'PATCH' }),
      );
    });

    it('keeps the ordinary default validity non-permanent', async () => {
      setLanguage(language);
      const draft = { ...dated, published: false };
      const fetcher = vi.fn(async (path: string, options: RequestInit = {}) => {
        if (path === '/api/shares') return json(options.method === 'POST' ? draft : []);
        if (path === '/api/uploads/' + draft.files[0].id) return json(draft.files[0]);
        if (path === '/api/shares/' + draft.id + '/publish') return json(dated);
        throw new Error('Unexpected request: ' + path);
      });
      vi.stubGlobal('fetch', fetcher);
      render(<Workspace me={me} reload={async () => {}} />);
      expect(screen.getByRole('combobox', { name: t('share.validity') })).toHaveTextContent(
        '7 ' + t('share.days'),
      );
      fireEvent.change(screen.getByLabelText(t('upload.choose')), {
        target: { files: [new File(['png'], 'holiday.png')] },
      });
      fireEvent.click(screen.getByRole('button', { name: t('share.createLink') }));
      await screen.findByRole('dialog', { name: t('share.ready') });
      const call = fetcher.mock.calls.find(
        ([path, options]) => path === '/api/shares' && options?.method === 'POST',
      )!;
      const body = JSON.parse(String(call[1]?.body));
      expect(body.days).toBe(7);
      expect(body.permanent === undefined || body.permanent === false).toBe(true);
    });

    it('shows expires:0 as localized never, not epoch/expired, in My shares and its link modal', async () => {
      setLanguage(language);
      vi.stubGlobal(
        'fetch',
        vi.fn(async (path: string) => {
          if (path === '/api/shares') return json([permanent, dated]);
          throw new Error('Unexpected request: ' + path);
        }),
      );
      render(<Workspace me={me} reload={async () => {}} />);
      const card = await myShares();
      expect(within(card).getByText(never[language])).toBeVisible();
      expectNotEpochOrExpired(card);
      const datedCard = screen.getByRole('heading', { name: dated.title }).closest('article')!;
      expect(datedCard).toHaveTextContent(formatDate(dated.expires * 1000));
      expect(within(datedCard).queryByText(never[language])).not.toBeInTheDocument();
      fireEvent.click(within(card).getByRole('button', { name: copyName() }));
      const dialog = await screen.findByRole('dialog', { name: t('share.ready') });
      expect(within(dialog).getByText(never[language])).toBeVisible();
      expectNotEpochOrExpired(dialog);
      const other = language === 'en' ? 'sv' : 'en';
      act(() => setLanguage(other));
      expect(within(dialog).getByText(never[other])).toBeVisible();
      expectNotEpochOrExpired(dialog);
    });

    it('shows localized never on a public permanent share and switches language live', async () => {
      setLanguage(language);
      vi.stubGlobal(
        'fetch',
        vi.fn(async (path: string) => {
          if (path === '/api/public/' + permanent.id) return json(permanent);
          throw new Error('Unexpected request: ' + path);
        }),
      );
      render(<PublicShare id={permanent.id} />);
      await screen.findByRole('heading', { name: permanent.title });
      expect(screen.getByText(never[language])).toBeVisible();
      expectNotEpochOrExpired(screen.getByRole('main'));
      const other = language === 'en' ? 'sv' : 'en';
      act(() => setLanguage(other));
      expect(screen.getByText(never[other])).toBeVisible();
      expectNotEpochOrExpired(screen.getByRole('main'));
    });
  });

  for (const access of ['owner', 'public'] as const) {
    describe(`${access} filename preview (${language})`, () => {
      const base = '/api/' + (access === 'owner' ? 'shares' : 'public') + '/' + permanent.id;
      const download = base + '/files/' + permanent.files[0].id;
      const preview = download + '/preview';
      async function openFilename() {
        if (access === 'owner') {
          render(<Workspace me={me} reload={async () => {}} />);
          await myShares();
        } else {
          render(<PublicShare id={permanent.id} />);
          await screen.findByRole('heading', { name: permanent.title });
        }
        // Require a real accessible filename control, not a static <strong> or a direct modal import.
        fireEvent.click(await screen.findByRole('button', { name: /holiday\.png/i }));
        return screen.findByRole('dialog');
      }
      function route(previewResponse: () => unknown) {
        const fetcher = vi.fn(async (path: string, _options: RequestInit = {}) => {
          if (path === '/api/shares' && access === 'owner') return json([permanent]);
          if (path === base && access === 'public') return json(permanent);
          if (path === preview) return previewResponse();
          throw new Error('Unexpected request: ' + path);
        });
        vi.stubGlobal('fetch', fetcher);
        return fetcher;
      }
      function downloadIn(dialog: HTMLElement) {
        const link = within(dialog).getByRole('link', { name: /download|ladda ner/i });
        expect(link).toHaveAttribute('href', download);
        return link;
      }

      it('opens from the filename, fetches image bytes from the exact API and revokes its URL after close', async () => {
        setLanguage(language);
        const urls = objectURLs();
        const fetcher = route(imageResponse);
        const dialog = await openFilename();
        expect(dialog).toBeVisible();
        downloadIn(dialog);
        const image = await within(dialog).findByRole('img');
        expect(image).toHaveAttribute('src', 'blob:magisend-preview');
        expect(fetcher).toHaveBeenCalledWith(
          preview,
          expect.objectContaining({ credentials: 'same-origin' }),
        );
        const request = fetcher.mock.calls.find(([path]) => path === preview)!;
        expect(request[1]?.method ?? 'GET').toBe('GET');
        expect(urls.create).toHaveBeenCalledTimes(1);
        const blob = urls.create.mock.calls[0][0];
        expect(blob).toBeInstanceOf(Blob);
        expect(blob.type).toBe('image/png');
        expect(await readBytes(blob)).toEqual(imageBytes);
        expect(urls.revoke).not.toHaveBeenCalled();
        fireEvent.click(within(dialog).getByRole('button', { name: t('common.close') }));
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        await waitFor(() => expect(urls.revoke).toHaveBeenCalledWith('blob:magisend-preview'));
      });

      it('keeps an explicit download and localized unsupported fallback on a 415 response', async () => {
        setLanguage(language);
        const urls = objectURLs();
        route(() =>
          json(
            { error_code: 'preview_unsupported', error: 'BACKEND_FALLBACK_MUST_NOT_BE_SHOWN' },
            415,
          ),
        );
        const dialog = await openFilename();
        expect(await within(dialog).findByText(unsupported[language])).toBeVisible();
        downloadIn(dialog);
        expect(dialog).not.toHaveTextContent('BACKEND_FALLBACK_MUST_NOT_BE_SHOWN');
        expect(within(dialog).queryByRole('img')).not.toBeInTheDocument();
        expect(urls.create).not.toHaveBeenCalled();
        const other = language === 'en' ? 'sv' : 'en';
        act(() => setLanguage(other));
        expect(within(dialog).getByText(unsupported[other])).toBeVisible();
        downloadIn(dialog);
      });

      it('revokes the image object URL when the containing page unmounts', async () => {
        setLanguage(language);
        const urls = objectURLs();
        route(imageResponse);
        const dialog = await openFilename();
        await within(dialog).findByRole('img');
        cleanup();
        await waitFor(() => expect(urls.revoke).toHaveBeenCalledWith('blob:magisend-preview'));
      });

      it('aborts an in-flight preview when the user closes the dialog', async () => {
        setLanguage(language);
        const urls = objectURLs();
        const fetcher = route(() => new Promise<never>(() => {}));
        const dialog = await openFilename();
        await waitFor(() =>
          expect(fetcher.mock.calls.some(([path]) => path === preview)).toBe(true),
        );
        const signal = fetcher.mock.calls.find(([path]) => path === preview)![1]?.signal;
        expect(signal).toBeInstanceOf(AbortSignal);
        expect(signal!.aborted).toBe(false);
        fireEvent.click(within(dialog).getByRole('button', { name: t('common.close') }));
        await waitFor(() => expect(signal!.aborted).toBe(true));
        expect(urls.create).not.toHaveBeenCalled();
      });
    });
  }
}
