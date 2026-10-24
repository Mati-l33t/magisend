import { lazy, Suspense, useEffect, useState } from 'react';
import { Clock, LoaderCircle } from 'lucide-react';
import { type Share, type SharedFile } from './api';
import { formatDate, t, useLanguage, useMessage, message } from './i18n';
import { noteTitle } from './NoteEditor';

const Markdown = lazy(() => import('./Markdown'));

// A link that holds one note opens as the note itself, ready to read.
export default function PublicNote({
  share,
  file,
  version,
  actions,
}: {
  share: Share;
  file: SharedFile;
  // Changes after an edit, so the text is read again.
  version: number;
  // The same download, edit and delete buttons a file in the list has.
  actions: React.ReactNode;
}) {
  useLanguage();
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useMessage();
  const base =
    '/api/public/' + encodeURIComponent(share.id) + '/files/' + encodeURIComponent(file.id);
  useEffect(() => {
    const controller = new AbortController();
    fetch(base + '/media', {
      credentials: 'same-origin',
      cache: 'no-store',
      headers: { 'X-MagiSend': '1' },
      signal: controller.signal,
    })
      .then(async (response) => {
        const type = (response.headers.get('Content-Type') || '').split(';')[0].trim();
        if (!response.ok || type !== 'text/plain') throw new Error('unsupported');
        setText(await response.text());
      })
      .catch(() => !controller.signal.aborted && setError(message('errors.preview_unsupported')));
    return () => controller.abort();
  }, [base, version]);
  return (
    <article className="panel public-note">
      <header className="public-note-head">
        <div>
          <h1>{noteTitle(file.name)}</h1>
          <p className="muted">
            <Clock size={14} />{' '}
            {share.expires === 0
              ? t('share.neverExpires')
              : t('share.expires', { date: formatDate(share.expires * 1000) })}
          </p>
        </div>
        {actions}
      </header>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {text === null && !error ? (
        <p role="status" className="muted">
          <LoaderCircle className="spin" size={16} /> {t('preview.loading')}
        </p>
      ) : (
        text !== null && (
          <div className="preview-markdown public-note-text">
            <Suspense fallback={<pre className="preview-text">{text}</pre>}>
              <Markdown text={text} />
            </Suspense>
          </div>
        )
      )}
    </article>
  );
}
