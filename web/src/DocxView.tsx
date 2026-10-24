import { useEffect, useRef, useState } from 'react';
import { LoaderCircle } from 'lucide-react';
import { t, useLanguage, LocalizedError } from './i18n';
import { officeBytes, openArchive, unsupported } from './officeFile';

const docxType = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';

// Shown around the pages: the document's own look stays inside them.
const frame = `
:host { display: block; }
.docx-wrapper { background: transparent !important; padding: 16px !important; }
.docx-wrapper > section.docx { margin-bottom: 16px !important; box-shadow: 0 1px 6px rgb(0 0 0 / 0.25) !important; }
a[href] { cursor: pointer; }
`;

// Renders a Word document with docx-preview into a shadow root, so its styles
// cannot touch the app and the app's cannot change it. The page's security
// policy forbids <style> elements, so the document's styles are collected and
// applied as a constructed stylesheet instead. Embedded HTML and fonts are left
// out, links open in a new tab, and nothing in the document can run.
export default function DocxView({ url, failed }: { url: string; failed: (e: Error) => void }) {
  useLanguage();
  const host = useRef<HTMLDivElement>(null);
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    async function load() {
      const data = await officeBytes(url, docxType, controller.signal);
      await openArchive(data);
      const { renderAsync } = await import('docx-preview');
      if (cancelled || !host.current) return;
      const shadow = host.current.shadowRoot ?? host.current.attachShadow({ mode: 'open' });
      const body = document.createElement('div');
      const styles = document.createElement('div');
      await renderAsync(data, body, styles, {
        ignoreFonts: true,
        renderAltChunks: false,
        renderComments: false,
        renderChanges: false,
        experimental: false,
        useBase64URL: false,
      });
      if (cancelled) return;
      const sheet = new CSSStyleSheet();
      sheet.replaceSync(
        [...styles.querySelectorAll('style')].map((s) => s.textContent).join('\n') + frame,
      );
      shadow.adoptedStyleSheets = [sheet];
      for (const link of body.querySelectorAll('a')) {
        const href = link.getAttribute('href') || '';
        if (/^(https?:|mailto:)/i.test(href)) {
          link.target = '_blank';
          link.rel = 'noopener noreferrer';
        } else link.removeAttribute('href');
      }
      shadow.replaceChildren(body);
      if (!body.querySelector('section')) throw unsupported();
      // Pages keep their real width; a narrow screen shows them smaller.
      const page = body.querySelector('section')!.offsetWidth + 32;
      const room = host.current.clientWidth;
      if (page > room) host.current.style.zoom = String(Math.max(0.3, room / page));
    }
    load()
      .catch((e) => {
        if (!cancelled) failed(e instanceof LocalizedError ? e : unsupported());
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [url]);
  return (
    <div className="office-view">
      {busy && (
        <p role="status">
          <LoaderCircle className="spin" size={18} /> {t('preview.loading')}
        </p>
      )}
      <div className="docx-host" ref={host} />
    </div>
  );
}
