import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import {
  ArrowDownToLine,
  Check,
  Copy,
  LoaderCircle,
  Maximize2,
  Minimize2,
  WrapText,
  X,
  ChevronLeft,
  ChevronRight,
  Pause,
  Play,
} from 'lucide-react';
import { copyText } from './clipboard';
import { responseData, type SharedFile } from './api';
import { t, useLanguage, useMessage, message, LocalizedError, formatNumber } from './i18n';
import Zoomable from './Zoom';

const PdfView = lazy(() => import('./PdfView'));
const DocxView = lazy(() => import('./DocxView'));
const SheetView = lazy(() => import('./SheetView'));
const Markdown = lazy(() => import('./Markdown'));
const CodeView = lazy(() => import('./CodeView'));
const maxBytes = 20 * 1024 * 1024;
// Large phone photos (up to 200 MP) are JPEGs the server sends as they are.
const maxImageBytes = 64 * 1024 * 1024;

// Which viewer a file name suggests; the server checks the actual content.
export type PreviewKind = 'image' | 'pdf' | 'video' | 'audio' | 'text' | 'document' | 'sheet';
const textExtensions = new Set(
  'txt md markdown json yaml yml toml csv log ini conf env xml html htm go py rs c h cc cpp hpp cs java kt swift rb php lua css js jsx mjs cjs ts tsx sql sh bash zsh ps1 diff patch'.split(
    ' ',
  ),
);
export function previewKind(name: string): PreviewKind | null {
  const ext = name.toLowerCase().split('.').pop() || '';
  if (name.includes('.') && textExtensions.has(ext)) return 'text';
  if (['png', 'jpg', 'jpeg', 'gif', 'webp'].includes(ext)) return 'image';
  if (ext === 'pdf') return 'pdf';
  if (ext === 'docx') return 'document';
  if (ext === 'xlsx') return 'sheet';
  if (['mp4', 'm4v', 'mov', 'webm'].includes(ext)) return 'video';
  if (['mp3', 'm4a', 'aac', 'ogg', 'oga', 'opus', 'wav', 'flac'].includes(ext)) return 'audio';
  return null;
}
const rasterTypes = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);
function unsupported() {
  return new LocalizedError(message('errors.preview_unsupported'));
}
async function previewBlob(response: Response, type: string) {
  const limit = type === 'image/jpeg' ? maxImageBytes : maxBytes;
  const length = response.headers.get('Content-Length');
  if (length !== null && Number(length) > limit) {
    await response.body?.cancel();
    throw unsupported();
  }
  if (!response.body) {
    const blob = await response.blob();
    if (blob.size > limit) throw unsupported();
    return blob.slice(0, blob.size, type);
  }
  const reader = response.body.getReader();
  const chunks: BlobPart[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) {
        await reader.cancel();
        throw unsupported();
      }
      chunks.push(new Uint8Array(value));
    }
  } finally {
    reader.releaseLock();
  }
  return new Blob(chunks, { type });
}

export default function FilePreview({
  shareId,
  file,
  owner = false,
  library = false,
  close,
  files,
  show,
}: {
  shareId: string;
  file: SharedFile;
  owner?: boolean;
  // A file of the signed-in user's own library.
  library?: boolean;
  close: () => void;
  // The files around this one, for moving between them like a gallery.
  files?: SharedFile[];
  show?: (file: SharedFile) => void;
}) {
  useLanguage();
  const around = files && show ? files.filter((f) => previewKind(f.name)) : [];
  const index = around.findIndex((f) => f.id === file.id);
  const browsing = index >= 0 && around.length > 1;
  const step = (by: number) => {
    if (browsing) show!(around[(index + by + around.length) % around.length]);
  };
  // A slideshow moves through the pictures every few seconds.
  const pictures = around.filter((f) => previewKind(f.name) === 'image');
  const [playing, setPlaying] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const objectURL = useRef<string | null>(null);
  const [source, setSource] = useState('');
  const [text, setText] = useState<string | null>(null);
  const markdown = /\.(md|markdown)$/i.test(file.name);
  const [formatted, setFormatted] = useState(true);
  // Prose wraps; code and logs keep their lines.
  const [wrap, setWrap] = useState(/\.(txt|md|markdown)$/i.test(file.name));
  const [copied, setCopied] = useState(false);
  const extension = file.name.toLowerCase().split('.').pop() || '';
  const [busy, setBusy] = useState(true);
  const [natural, setNatural] = useState<{ width: number; height: number } | null>(null);
  const [full, setFull] = useState(false);
  const [error, setError] = useMessage();
  const base = library
    ? '/api/library/files/' + encodeURIComponent(file.id)
    : '/api/' +
      (owner ? 'shares' : 'public') +
      '/' +
      encodeURIComponent(shareId) +
      '/files/' +
      encodeURIComponent(file.id);
  const download = library ? base + '/download' : base;
  const kind = previewKind(file.name) || 'image';
  useEffect(() => {
    if (dialog.current?.showModal) dialog.current.showModal();
    else dialog.current?.setAttribute('open', '');
    // Leaving the browser's full screen (Esc) also leaves the full-size view.
    const left = () => {
      if (!document.fullscreenElement) setFull(false);
    };
    document.addEventListener('fullscreenchange', left);
    return () => {
      document.removeEventListener('fullscreenchange', left);
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => {});
    };
  }, []);
  // Fills the window, and the whole screen where the browser allows it
  // (phones without full screen support keep the window-sized view).
  async function toggleFull() {
    if (full) {
      setFull(false);
      if (document.fullscreenElement) await document.exitFullscreen().catch(() => {});
      return;
    }
    setFull(true);
    await dialog.current?.requestFullscreen?.().catch(() => {});
  }
  useEffect(() => {
    setFormatted(true);
    setWrap(/\.(txt|md|markdown)$/i.test(file.name));
    setCopied(false);
  }, [file.id]);
  // ← and → move to the previous and next file, unless a player or a field
  // has the keys.
  useEffect(() => {
    if (!browsing) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.ctrlKey || event.metaKey || event.altKey) return;
      if (
        (event.target as HTMLElement | null)?.closest(
          'input, textarea, video, audio, [contenteditable="true"]',
        )
      )
        return;
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        setPlaying(false);
        step(event.key === 'ArrowLeft' ? -1 : 1);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });
  useEffect(() => {
    if (!playing) return;
    if (kind !== 'image' || pictures.length < 2) {
      setPlaying(false);
      return;
    }
    // The next picture waits until this one has loaded.
    if (busy) return;
    const timer = window.setTimeout(() => {
      const at = pictures.findIndex((f) => f.id === file.id);
      show!(pictures[(at + 1) % pictures.length]);
    }, 5000);
    return () => window.clearTimeout(timer);
  }, [playing, file.id, busy]);
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    let url: string | null = null;
    const textual = kind === 'text';
    setSource('');
    setText(null);
    setNatural(null);
    setError('');
    setBusy(kind === 'image' || textual);
    if (kind !== 'image' && !textual) return;
    async function load() {
      try {
        const response = await fetch(base + (textual ? '/media' : '/preview'), {
          method: 'GET',
          credentials: 'same-origin',
          redirect: 'error',
          cache: 'no-store',
          headers: { 'X-MagiSend': '1' },
          signal: controller.signal,
        });
        if (!active) return;
        if (response.status === 415) {
          await response.body?.cancel();
          throw unsupported();
        }
        if (!response.ok) {
          await responseData(response);
          return;
        }
        const type = (response.headers.get('Content-Type') || '')
          .split(';')[0]
          .trim()
          .toLowerCase();
        if (textual ? type !== 'text/plain' : !rasterTypes.has(type)) {
          await response.body?.cancel();
          throw unsupported();
        }
        const blob = await previewBlob(response, type);
        if (!active || controller.signal.aborted) return;
        if (textual) {
          const content = await blob.text();
          if (active) setText(content);
          return;
        }
        url = URL.createObjectURL(blob);
        objectURL.current = url;
        setSource(url);
      } catch (e) {
        if (active && !controller.signal.aborted) setError(e as Error);
      } finally {
        if (active) setBusy(false);
      }
    }
    void load();
    return () => {
      active = false;
      controller.abort();
      if (url && objectURL.current === url) {
        URL.revokeObjectURL(url);
        objectURL.current = null;
      }
    };
  }, [base]);
  function rejectImage() {
    if (objectURL.current) {
      URL.revokeObjectURL(objectURL.current);
      objectURL.current = null;
    }
    setSource('');
    setError(message('errors.preview_unsupported'));
  }
  return (
    <dialog
      ref={dialog}
      className={'dialog file-preview' + (full ? ' is-full' : '')}
      aria-label={t('preview.title', { name: file.name })}
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
    >
      <div className="dialog-heading">
        <h2>
          {t('preview.title', { name: file.name })}
          {browsing && (
            <small className="preview-count">
              {t('preview.position', {
                at: formatNumber(index + 1),
                count: formatNumber(around.length),
              })}
            </small>
          )}
        </h2>
        <div className="preview-tools">
          {pictures.length > 1 && kind === 'image' && (
            <button
              className="icon-button"
              aria-label={t(playing ? 'preview.pause' : 'preview.slideshow')}
              title={t(playing ? 'preview.pause' : 'preview.slideshow')}
              aria-pressed={playing}
              onClick={() => setPlaying(!playing)}
            >
              {playing ? <Pause size={18} /> : <Play size={18} />}
            </button>
          )}
          {markdown && text !== null && (
            <div role="radiogroup" aria-label={t('preview.textView')} className="segmented">
              {[true, false].map((option) => (
                <label
                  key={String(option)}
                  className={formatted === option ? 'checked inherit' : ''}
                >
                  <input
                    type="radio"
                    name="text-view"
                    checked={formatted === option}
                    onChange={() => setFormatted(option)}
                  />
                  {t(option ? 'preview.formatted' : 'preview.plain')}
                </label>
              ))}
            </div>
          )}
          {text !== null && !(markdown && formatted) && (
            <button
              className="icon-button"
              aria-label={t('code.wrap')}
              title={t('code.wrap')}
              aria-pressed={wrap}
              onClick={() => setWrap(!wrap)}
            >
              <WrapText size={18} />
            </button>
          )}
          {text !== null && (
            <button
              className="icon-button"
              aria-label={t(copied ? 'code.copied' : 'code.copyAll')}
              title={t(copied ? 'code.copied' : 'code.copyAll')}
              onClick={() =>
                void copyText(text).then(
                  () => {
                    setCopied(true);
                    window.setTimeout(() => setCopied(false), 1500);
                  },
                  () => setError(message('code.copyFailed')),
                )
              }
            >
              {copied ? <Check size={18} /> : <Copy size={18} />}
            </button>
          )}
          {kind !== 'audio' && (
            <button
              className="icon-button"
              aria-label={t(full ? 'preview.exitFullscreen' : 'preview.fullscreen')}
              title={t(full ? 'preview.exitFullscreen' : 'preview.fullscreen')}
              aria-pressed={full}
              onClick={() => void toggleFull()}
            >
              {full ? <Minimize2 size={18} /> : <Maximize2 size={18} />}
            </button>
          )}
          <button className="icon-button" aria-label={t('common.close')} onClick={close}>
            <X size={18} />
          </button>
        </div>
      </div>
      {busy && (
        <p role="status">
          <LoaderCircle className="spin" size={18} /> {t('preview.loading')}
        </p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {source && (
        <Zoomable wheel="plain" natural={natural ?? undefined}>
          <img
            className="preview-image"
            src={source}
            alt={file.name}
            draggable={false}
            style={natural ? undefined : { opacity: 0 }}
            onError={rejectImage}
            onLoad={(event) => {
              const image = event.currentTarget;
              if (image.naturalWidth * image.naturalHeight > 16000000) rejectImage();
              else setNatural({ width: image.naturalWidth, height: image.naturalHeight });
            }}
          />
        </Zoomable>
      )}
      {text !== null &&
        (markdown && formatted ? (
          <Suspense fallback={<pre className="preview-text">{text}</pre>}>
            <div className="preview-text preview-markdown">
              <Markdown text={text} />
            </div>
          </Suspense>
        ) : (
          <Suspense fallback={<pre className="preview-text">{text}</pre>}>
            <div className="preview-text preview-code">
              <CodeView text={text} language={markdown ? 'markdown' : extension} wrap={wrap} />
            </div>
          </Suspense>
        ))}
      {kind === 'pdf' && !error && (
        <Suspense
          fallback={
            <p role="status">
              <LoaderCircle className="spin" size={18} /> {t('preview.loading')}
            </p>
          }
        >
          <PdfView url={base + '/media'} failed={(e) => setError(e)} />
        </Suspense>
      )}
      {(kind === 'document' || kind === 'sheet') && !error && (
        <Suspense
          fallback={
            <p role="status">
              <LoaderCircle className="spin" size={18} /> {t('preview.loading')}
            </p>
          }
        >
          {kind === 'document' ? (
            <DocxView url={base + '/media'} failed={(e) => setError(e)} />
          ) : (
            <SheetView url={base + '/media'} failed={(e) => setError(e)} />
          )}
        </Suspense>
      )}
      {kind === 'video' && !error && (
        <video
          className="preview-media"
          controls
          preload="metadata"
          src={base + '/media'}
          aria-label={file.name}
          onError={() => setError(message('errors.preview_unsupported'))}
        />
      )}
      {kind === 'audio' && !error && (
        <audio
          className="preview-audio"
          controls
          preload="metadata"
          src={base + '/media'}
          aria-label={file.name}
          onError={() => setError(message('errors.preview_unsupported'))}
        />
      )}
      {browsing && (
        <>
          <button
            className="icon-button preview-step previous"
            aria-label={t('preview.previous')}
            title={t('preview.previous')}
            onClick={() => {
              setPlaying(false);
              step(-1);
            }}
          >
            <ChevronLeft size={22} />
          </button>
          <button
            className="icon-button preview-step next"
            aria-label={t('preview.next')}
            title={t('preview.next')}
            onClick={() => {
              setPlaying(false);
              step(1);
            }}
          >
            <ChevronRight size={22} />
          </button>
        </>
      )}
      <a
        className="secondary"
        href={download}
        aria-label={t('public.downloadNamed', { name: file.name })}
      >
        <ArrowDownToLine size={17} />
        {t('public.download')}
      </a>
    </dialog>
  );
}
