import { useEffect, useState } from 'react';
import { LoaderCircle, RotateCcw, Trash2 } from 'lucide-react';
import { api, formatBytes, type Folder } from './api';
import FileKindIcon from './fileKind';
import { folderLabel } from './folderTree';
import { formatDate, formatNumber, t, useLanguage, useMessage } from './i18n';
import { Modal } from './Modal';
import { usePaged, ShowMore } from './Paging';

export interface TrashedFile {
  id: string;
  name: string;
  size: number;
  folder_id: string;
  deleted: number;
  purge_after: number;
  deleted_by?: string;
}

export async function loadTrash() {
  const list = await api<TrashedFile[]>('/api/trash');
  return Array.isArray(list) ? list : [];
}

// Deleted files wait here for 30 days. Restoring puts a file back in its
// folder and in the links that shared it.
export default function Trash({
  folders,
  editable,
  changed,
}: {
  folders: Folder[];
  editable: boolean;
  changed: () => Promise<void>;
}) {
  useLanguage();
  const [list, setList] = useState<TrashedFile[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [forever, setForever] = useState<TrashedFile | 'all' | null>(null);
  const paged = usePaged(list ?? [], 'trash');
  async function refresh() {
    setList(await loadTrash());
  }
  useEffect(() => {
    refresh().catch((e) => setError(e as Error));
  }, []);
  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    setError('');
    try {
      await fn();
    } catch (e) {
      setError(e as Error);
    } finally {
      await Promise.all([refresh(), changed()]).catch(() => {});
      setBusy(false);
    }
  }
  const place = (f: TrashedFile) =>
    f.folder_id && folders.some((x) => x.id === f.folder_id)
      ? folderLabel(folders, f.folder_id)
      : t('library.unfiled');
  return (
    <>
      <div className="library-bar">
        <h2>{t('trash.title')}</h2>
        {editable && !!list?.length && (
          <div className="row-actions">
            <button
              className="quiet small danger"
              disabled={busy}
              onClick={() => setForever('all')}
            >
              <Trash2 size={15} />
              {t('trash.empty')}
            </button>
          </div>
        )}
      </div>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {!list ? (
        <p role="status" className="muted">
          <LoaderCircle className="spin" size={16} /> {t('preview.loading')}
        </p>
      ) : list.length === 0 ? (
        <div className="library-empty">
          <Trash2 size={28} />
          <strong>{t('trash.nothing')}</strong>
          <span>{t('trash.hint')}</span>
        </div>
      ) : (
        <div className="library-list">
          {paged.shown.map((f) => (
            <div className="file-row library-row" key={f.id} data-file-id={f.id}>
              <FileKindIcon name={f.name} />
              <div className="file-name">
                <strong>{f.name}</strong>
                <small>
                  {[
                    formatBytes(f.size),
                    place(f),
                    f.deleted_by
                      ? t('trash.deletedBy', {
                          date: formatDate(f.deleted * 1000),
                          name: f.deleted_by,
                        })
                      : t('trash.deletedOn', { date: formatDate(f.deleted * 1000) }),
                    t('trash.goneOn', { date: formatDate(f.purge_after * 1000) }),
                  ].join(' · ')}
                </small>
              </div>
              {editable && (
                <div className="row-actions">
                  <button
                    className="quiet small"
                    disabled={busy}
                    aria-label={t('trash.restoreNamed', { name: f.name })}
                    onClick={() =>
                      void act(() => api('/api/trash/' + f.id + '/restore', 'POST', {}))
                    }
                  >
                    <RotateCcw size={15} />
                    <span className="trash-label">{t('trash.restore')}</span>
                  </button>
                  <button
                    className="icon-button danger"
                    disabled={busy}
                    aria-label={t('trash.deleteNamed', { name: f.name })}
                    title={t('trash.deleteForever')}
                    onClick={() => setForever(f)}
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
      <ShowMore paged={paged} />
      <p className="library-foot">{t('trash.hint')}</p>
      {forever && (
        <Modal
          title={forever === 'all' ? t('trash.emptyTitle') : t('trash.deleteForever')}
          close={() => !busy && setForever(null)}
        >
          <p>
            {forever === 'all'
              ? list?.length === 1
                ? t('trash.emptyWarningOne')
                : t('trash.emptyWarning', { count: formatNumber(list?.length ?? 0) })
              : t('trash.deleteWarning', { name: forever.name })}
          </p>
          <div className="dialog-actions">
            <button className="secondary" disabled={busy} onClick={() => setForever(null)}>
              {t('common.cancel')}
            </button>
            <button
              className="primary danger-solid"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await api(
                    forever === 'all' ? '/api/trash' : '/api/trash/' + forever.id,
                    'DELETE',
                    {},
                  );
                  setForever(null);
                })
              }
            >
              {busy ? <LoaderCircle className="spin" size={16} /> : <Trash2 size={16} />}
              {forever === 'all' ? t('trash.empty') : t('trash.deleteForever')}
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
