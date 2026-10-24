import { useEffect, useState } from 'react';
import { ArchiveRestore, Trash2 } from 'lucide-react';
import { api, formatBytes } from './api';
import { formatDate, formatNumber, message, t, useLanguage, useMessage } from './i18n';

interface Unclaimed {
  id: string;
  location: string;
  path: string;
  name: string;
  owner?: string;
  folder?: string;
  size: number;
  set_aside: number;
  known: boolean;
}
interface MissingFile {
  id: string;
  name: string;
  owner: string;
  size: number;
  location: string;
  library: boolean;
}
interface MissingState {
  count: number;
  library: number;
  files: MissingFile[];
  wrong: string[];
  unreachable: string[];
}
interface UnclaimedState {
  count: number;
  size: number;
  days: number;
  files: Unclaimed[];
}

const shown = 10;

// Administration → Storage: stored files the database does not know (after an
// older database was restored, for example), kept aside for a while so they
// can be put back. Hidden while there are none.
export function UnclaimedPanel({ changed }: { changed?: () => void }) {
  useLanguage();
  const [state, setState] = useState<UnclaimedState | null>(null);
  const [missing, setMissing] = useState<MissingState | null>(null);
  const [all, setAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [note, setNote] = useMessage();
  async function load() {
    const next = await api<UnclaimedState>('/api/admin/storage/unclaimed');
    setState(next && Array.isArray(next.files) ? next : null);
    const gone = await api<MissingState>('/api/admin/storage/missing').catch(() => null);
    setMissing(
      gone && Array.isArray(gone.files)
        ? { ...gone, wrong: gone.wrong ?? [], unreachable: gone.unreachable ?? [] }
        : null,
    );
  }
  useEffect(() => {
    load().catch(() => setState(null));
  }, []);
  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError('');
    setNote('');
    try {
      await fn();
      await load();
      changed?.();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  const lost =
    missing && (missing.count > 0 || missing.wrong.length > 0 || missing.unreachable.length > 0)
      ? missing
      : null;
  if (!state || (state.count === 0 && !note && !lost)) return null;
  const list = all ? state.files : state.files.slice(0, shown);
  return (
    <section className="panel admin-wide unclaimed-files">
      <h2>
        <ArchiveRestore size={18} /> {t('unclaimed.title')}
      </h2>
      {note && (
        <p role="status" className="success-note">
          {note}
        </p>
      )}
      {state.count > 0 && (
        <>
          <p className="muted">
            {t('unclaimed.intro', {
              count: formatNumber(state.count),
              size: formatBytes(state.size),
              days: formatNumber(state.days),
            })}
          </p>
          <ul className="token-list">
            {list.map((f) => (
              <li key={f.location + f.id}>
                <div>
                  <strong>{f.known && f.name ? f.name : t('unclaimed.unnamed')}</strong>
                  <small>
                    {[
                      f.known && f.owner ? t('unclaimed.owner', { name: f.owner }) : '',
                      f.known && f.folder ? f.folder : '',
                      formatBytes(f.size),
                      f.path,
                      t('unclaimed.setAside', { date: formatDate(f.set_aside * 1000, true) }),
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </small>
                </div>
              </li>
            ))}
          </ul>
          {state.files.length > shown && (
            <button type="button" className="quiet small" onClick={() => setAll(!all)}>
              {all
                ? t('audit.less')
                : t('unclaimed.more', { count: formatNumber(state.count - shown) })}
            </button>
          )}
          <p className="muted">{t('unclaimed.restoreHint')}</p>
        </>
      )}
      {lost && (
        <>
          <h3 className="backup-heading">{t('missing.title')}</h3>
          {lost.count > 0 && (
            <p className="muted">{t('missing.intro', { count: formatNumber(lost.count) })}</p>
          )}
          {lost.count > lost.library && (
            <p className="muted">
              {t('missing.inLinks', { count: formatNumber(lost.count - lost.library) })}
            </p>
          )}
          {lost.unreachable.length > 0 && (
            <p className="muted">
              {t('missing.unreachable', { locations: lost.unreachable.join(', ') })}
            </p>
          )}
          {lost.wrong.length > 0 && (
            <p className="muted">{t('missing.wrong', { locations: lost.wrong.join(', ') })}</p>
          )}
          <ul className="token-list">
            {lost.files.slice(0, shown).map((f) => (
              <li key={f.id}>
                <div>
                  <strong>{f.name}</strong>
                  <small>
                    {[
                      t('unclaimed.owner', { name: f.owner }),
                      formatBytes(f.size),
                      f.library ? '' : t('missing.link'),
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </small>
                </div>
              </li>
            ))}
          </ul>
          {lost.library > 0 && lost.wrong.length === 0 && (
            <div className="setting-actions">
              <button
                type="button"
                className="secondary danger"
                disabled={busy}
                onClick={() => {
                  if (
                    window.confirm(
                      t('missing.removeConfirm', { count: formatNumber(lost.library) }),
                    )
                  )
                    void run(async () => {
                      const done = await api<{ removed: number }>(
                        '/api/admin/storage/missing',
                        'DELETE',
                        {},
                      );
                      setNote(t('missing.removed', { count: formatNumber(done.removed) }));
                    });
                }}
              >
                <Trash2 size={16} />
                {t('missing.remove')}
              </button>
            </div>
          )}
        </>
      )}
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {state.count > 0 && (
        <div className="setting-actions">
          <button
            type="button"
            className="secondary danger"
            disabled={busy}
            onClick={() => {
              if (
                window.confirm(t('unclaimed.deleteConfirm', { count: formatNumber(state.count) }))
              )
                void run(async () => {
                  await api('/api/admin/storage/unclaimed', 'DELETE', {});
                });
            }}
          >
            <Trash2 size={16} />
            {t('unclaimed.delete')}
          </button>
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() =>
              void run(async () => {
                const done = await api<{ restored: number; failed: number }>(
                  '/api/admin/storage/unclaimed/restore',
                  'POST',
                  {},
                );
                setNote(
                  message(done.failed ? 'unclaimed.restoredSome' : 'unclaimed.restored', {
                    count: formatNumber(done.restored),
                    failed: formatNumber(done.failed),
                  }),
                );
              })
            }
          >
            <ArchiveRestore size={16} />
            {t('unclaimed.restore')}
          </button>
        </div>
      )}
    </section>
  );
}
