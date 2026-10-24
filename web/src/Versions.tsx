import { useEffect, useState } from 'react';
import { ArrowDownToLine, LoaderCircle, RotateCcw } from 'lucide-react';
import { api, formatBytes } from './api';
import { formatDate, t, useLanguage, useMessage } from './i18n';
import { Modal } from './Modal';

interface FileVersion {
  version: number;
  size: number;
  name: string;
  created: number;
  by: string;
  current: boolean;
}

// Every version a library file has had, newest first. Links always give the
// current one; an earlier one can be downloaded or made current again.
export default function Versions({
  file,
  editable,
  close,
  changed,
}: {
  file: { id: string; name: string };
  editable: boolean;
  close: () => void;
  changed: () => Promise<void>;
}) {
  useLanguage();
  const [list, setList] = useState<FileVersion[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const base = '/api/library/files/' + file.id + '/history';
  async function refresh() {
    const history = await api<FileVersion[]>(base);
    setList(Array.isArray(history) ? history : []);
  }
  useEffect(() => {
    refresh().catch((e) => setError(e as Error));
  }, [file.id]);
  async function restore(version: number) {
    setBusy(true);
    setError('');
    try {
      await api(base + '/' + version + '/restore', 'POST', {});
      await Promise.all([refresh(), changed()]);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t('library.historyTitle', { name: file.name })} close={() => !busy && close()}>
      <p className="muted">{t('library.historyHint')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {!list ? (
        <p role="status" className="muted">
          <LoaderCircle className="spin" size={16} /> {t('preview.loading')}
        </p>
      ) : (
        <ol className="version-list">
          {list.map((v) => (
            <li key={v.version} className={v.current ? 'current' : undefined}>
              <span className="version-number">{v.version}</span>
              <div className="file-name">
                <strong>
                  {v.name}
                  {v.current && <em className="version-badge">{t('library.versionCurrent')}</em>}
                </strong>
                <small>
                  {formatBytes(v.size)} ·{' '}
                  {v.by
                    ? t('library.versionBy', {
                        date: formatDate(v.created * 1000, true),
                        name: v.by,
                      })
                    : t('library.versionOriginal')}
                </small>
              </div>
              <div className="row-actions">
                <a
                  className="icon-button"
                  href={base + '/' + v.version}
                  aria-label={t('library.versionDownload', { version: String(v.version) })}
                  title={t('library.versionDownload', { version: String(v.version) })}
                >
                  <ArrowDownToLine size={16} />
                </a>
                {editable && !v.current && (
                  <button
                    className="quiet small"
                    disabled={busy}
                    aria-label={t('library.versionRestore', { version: String(v.version) })}
                    onClick={() => void restore(v.version)}
                  >
                    <RotateCcw size={15} />
                    {t('library.restoreShort')}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ol>
      )}
      <div className="dialog-actions">
        <button className="secondary" disabled={busy} onClick={close}>
          {t('common.close')}
        </button>
      </div>
    </Modal>
  );
}
