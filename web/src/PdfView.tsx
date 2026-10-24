import { useEffect, useRef, useState } from 'react';
import { getDocument, GlobalWorkerOptions, type PDFPageProxy } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import { LoaderCircle } from 'lucide-react';
import { LocalizedError } from './i18n';
import { t, useLanguage, message, formatNumber } from './i18n';
import Zoomable from './Zoom';

GlobalWorkerOptions.workerSrc = workerUrl;

const maxBytes = 100 * 1024 * 1024;
const maxPages = 40;
// Limits for a page drawn sharper while zoomed in, to keep memory in check.
const maxCanvasWidth = 4096;
const maxCanvasPixels = 16_000_000;

interface Page {
  page: PDFPageProxy;
  canvas: HTMLCanvasElement;
  // Scale the canvas was drawn at, and the scale used when not zoomed.
  scale: number;
  fitted: number;
  busy: boolean;
}

function drawn(page: PDFPageProxy, scale: number, number: number) {
  const viewport = page.getViewport({ scale });
  const canvas = document.createElement('canvas');
  canvas.width = Math.floor(viewport.width);
  canvas.height = Math.floor(viewport.height);
  canvas.style.width = '100%';
  canvas.dataset.page = String(number);
  canvas.setAttribute('aria-label', t('preview.page', { number: formatNumber(number) }));
  return { canvas, task: page.render({ canvas, viewport }) };
}

// Renders a PDF to canvases with pdf.js (scripts in the document never run)
// instead of handing it to the browser's PDF plugin. Pages in view are drawn
// again at a higher resolution when zoomed in.
export default function PdfView({ url, failed }: { url: string; failed: (e: Error) => void }) {
  useLanguage();
  const pages = useRef<HTMLDivElement>(null);
  const resized = useRef(() => {});
  const [total, setTotal] = useState(0);
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    let destroy = () => {};
    const list: Page[] = [];
    const visible = new Set<number>();
    let timer = 0;
    const observer =
      typeof IntersectionObserver === 'undefined'
        ? null
        : new IntersectionObserver((entries) => {
            for (const entry of entries) {
              const index = Number((entry.target as HTMLElement).dataset.page) - 1;
              if (entry.isIntersecting) visible.add(index);
              else visible.delete(index);
            }
            schedule();
          });

    function schedule() {
      clearTimeout(timer);
      timer = window.setTimeout(sharpen, 250);
    }
    resized.current = schedule;

    function wanted(item: Page, index: number) {
      if (!visible.has(index)) return item.fitted;
      const base = item.page.getViewport({ scale: 1 });
      const ratio = window.devicePixelRatio || 1;
      const width = Math.min(item.canvas.clientWidth * ratio, maxCanvasWidth);
      const limit = Math.sqrt(maxCanvasPixels / (base.width * base.height));
      return Math.max(item.fitted, Math.min(width / base.width, limit));
    }

    async function sharpen() {
      for (const [index, item] of list.entries()) {
        if (cancelled) return;
        const scale = wanted(item, index);
        if (item.busy || Math.abs(scale - item.scale) / scale < 0.15) continue;
        item.busy = true;
        try {
          const next = drawn(item.page, scale, index + 1);
          await next.task.promise;
          if (cancelled) return;
          observer?.unobserve(item.canvas);
          item.canvas.replaceWith(next.canvas);
          item.canvas.width = 0;
          item.canvas = next.canvas;
          item.scale = scale;
          observer?.observe(next.canvas);
        } catch {
          // The page stays as it was drawn before.
        } finally {
          item.busy = false;
        }
      }
    }

    async function load() {
      const response = await fetch(url, {
        credentials: 'same-origin',
        cache: 'no-store',
        headers: { 'X-MagiSend': '1' },
        signal: controller.signal,
      });
      const type = (response.headers.get('Content-Type') || '').split(';')[0].trim();
      const length = Number(response.headers.get('Content-Length') || 0);
      if (!response.ok || type !== 'application/pdf' || length > maxBytes)
        throw new LocalizedError(message('errors.preview_unsupported'));
      const data = new Uint8Array(await response.arrayBuffer());
      if (data.byteLength > maxBytes)
        throw new LocalizedError(message('errors.preview_unsupported'));
      const task = getDocument({ data, enableXfa: false });
      destroy = () => void task.destroy();
      const pdf = await task.promise;
      if (cancelled) return;
      setTotal(pdf.numPages);
      const width = Math.min(pages.current?.clientWidth || 800, 1200);
      for (let number = 1; number <= Math.min(pdf.numPages, maxPages); number++) {
        const page = await pdf.getPage(number);
        if (cancelled || !pages.current) return;
        const base = page.getViewport({ scale: 1 });
        const scale = (width / base.width) * (window.devicePixelRatio || 1);
        const { canvas, task } = drawn(page, scale, number);
        pages.current.append(canvas);
        list.push({ page, canvas, scale, fitted: scale, busy: false });
        observer?.observe(canvas);
        await task.promise;
        if (number === 1) setBusy(false);
      }
      setBusy(false);
      schedule();
    }
    load().catch((e) => {
      if (!cancelled) {
        setBusy(false);
        failed(e instanceof Error ? e : new LocalizedError(message('errors.preview_unsupported')));
      }
    });
    return () => {
      cancelled = true;
      clearTimeout(timer);
      observer?.disconnect();
      resized.current = () => {};
      controller.abort();
      destroy();
    };
  }, [url]);
  return (
    <div className="pdf-view">
      {busy && (
        <p role="status">
          <LoaderCircle className="spin" size={18} /> {t('preview.loading')}
        </p>
      )}
      <Zoomable wheel="ctrl" className="pdf-zoom" onResize={() => resized.current()}>
        <div className="pdf-pages" ref={pages} />
      </Zoomable>
      {total > maxPages && (
        <p className="muted">
          {t('preview.firstPages', { shown: formatNumber(maxPages), total: formatNumber(total) })}
        </p>
      )}
    </div>
  );
}
