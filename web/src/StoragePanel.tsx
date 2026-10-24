import { useEffect, useRef, useState } from 'react';
import { ArrowRightLeft, Check, FolderPlus, HardDrive, Square, Trash2 } from 'lucide-react';
import { api, formatBytes } from './api';
import { t, useLanguage, useMessage, message, formatNumber, getLanguage } from './i18n';

interface Location {
  id: string;
  path: string;
  online: boolean;
  active: boolean;
  free_bytes: number;
  total_bytes: number;
  files: number;
  bytes: number;
}
interface Move {
  running: boolean;
  target: string;
  files: number;
  done: number;
  bytes: number;
  done_bytes: number;
  failed: number;
  error: string;
  finished: number;
}
interface Status {
  locations: Location[];
  move: Move;
  uid: number;
  gid: number;
}
interface Candidate {
  path: string;
  type: string;
  free_bytes: number;
  total_bytes: number;
}

// Where file contents are kept: the data folder and any added folders, usually
// network shares mounted into the system. The database always stays local.
export function StoragePanel({ changed }: { changed?: () => void }) {
  useLanguage();
  const [status, setStatus] = useState<Status | null>(null);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [notice, setNotice] = useMessage();
  // The server's last finish time when this panel started a move; null when
  // no move started here is awaited.
  const watching = useRef<number | null>(null);
  async function load() {
    const data = await api<Status>('/api/admin/storage');
    if (Array.isArray(data?.locations)) setStatus(data);
  }
  useEffect(() => {
    load().catch((e) => setError(e as Error));
    api<Candidate[]>('/api/admin/storage/candidates').then(
      (list) => setCandidates(Array.isArray(list) ? list : []),
      () => {},
    );
  }, []);
  // Follows a running move closely, and otherwise notices shares that went
  // away or came back.
  const move = status?.move;
  useEffect(() => {
    const timer = setInterval(() => void load().catch(() => {}), move?.running ? 1500 : 15000);
    return () => clearInterval(timer);
  }, [move?.running]);
  useEffect(() => {
    if (!move) return;
    // A new finish time is reported even when the move ended before the start
    // request answered.
    if (watching.current !== null && !move.running && move.finished !== watching.current) {
      watching.current = null;
      if (move.error === 'storage_offline') setError(message('errors.storage_offline'));
      else if (move.error === 'stopped')
        setNotice(
          t('storage.moveStopped', {
            done: formatNumber(move.done),
            files: formatNumber(move.files),
          }),
        );
      else
        setNotice(
          t(move.done - move.failed === 1 ? 'storage.movedOne' : 'storage.moved', {
            count: formatNumber(move.done - move.failed),
          }),
        );
      if (move.failed > 0) setError(t('storage.moveFailed', { count: formatNumber(move.failed) }));
      changed?.();
    }
  }, [move?.running, move?.finished]);

  async function act(work: () => Promise<unknown>, done?: string) {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await work();
      await load();
      if (done) setNotice(done);
      changed?.();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  if (!status) return null;
  const added = new Set(status.locations.map((l) => l.path));
  const suggestions = candidates.filter(
    (c) => !added.has(c.path) && ![...added].some((p) => p.startsWith(c.path + '/')),
  );
  const target = status.locations.find((l) => l.id === status.move.target);
  const uid = 100000 + status.uid;
  const gid = 100000 + status.gid;
  return (
    <section className="panel admin-wide storage-locations">
      <h2>
        <HardDrive size={18} /> {t('storage.title')}
      </h2>
      <p className="muted panel-intro">{t('storage.intro')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notice && (
        <p className="success-note" role="status">
          {notice}
        </p>
      )}
      <ul className="token-list storage-list">
        {status.locations.map((l) => {
          const elsewhere = status.locations.some((o) => o.id !== l.id && o.files > 0);
          return (
            <li key={l.id} className={l.online ? undefined : 'offline'}>
              <div>
                <strong className={l.id === 'local' ? undefined : 'mono'}>
                  {l.id === 'local' ? t('storage.local') : l.path}
                </strong>
                <small>
                  {t(l.files === 1 ? 'storage.filesOne' : 'storage.files', {
                    count: formatNumber(l.files),
                    size: formatBytes(l.bytes),
                  })}
                  {' · '}
                  {l.online
                    ? t('storage.free', {
                        free: formatBytes(l.free_bytes),
                        total: formatBytes(l.total_bytes),
                      })
                    : t('storage.offlineNote')}
                </small>
              </div>
              <div className="storage-actions">
                {l.active && (
                  <span className="badge accent">
                    <Check size={12} /> {t('storage.active')}
                  </span>
                )}
                {!l.online && <span className="badge warn">{t('storage.offline')}</span>}
                {!l.active && l.online && (
                  <button
                    className="secondary small"
                    disabled={busy}
                    onClick={() =>
                      void act(
                        () => api('/api/admin/storage/active', 'PUT', { id: l.id }),
                        t('storage.activated'),
                      )
                    }
                  >
                    {t('storage.useForNew')}
                  </button>
                )}
                {l.online && elsewhere && !status.move.running && (
                  <button
                    className="secondary small"
                    disabled={busy}
                    onClick={() => {
                      watching.current = status.move.finished;
                      void act(async () => {
                        try {
                          await api('/api/admin/storage/move', 'POST', { to: l.id });
                        } catch (e) {
                          watching.current = null;
                          throw e;
                        }
                      });
                    }}
                  >
                    <ArrowRightLeft size={15} />
                    {t('storage.moveHere')}
                  </button>
                )}
                {l.id !== 'local' && !l.active && l.files === 0 && (
                  <button
                    className="icon-button quiet danger"
                    aria-label={t('storage.remove', { path: l.path })}
                    title={t('storage.remove', { path: l.path })}
                    disabled={busy || (status.move.running && status.move.target === l.id)}
                    onClick={() =>
                      void act(
                        () => api('/api/admin/storage/locations/' + l.id, 'DELETE', {}),
                        t('storage.removed'),
                      )
                    }
                  >
                    <Trash2 size={16} />
                  </button>
                )}
              </div>
            </li>
          );
        })}
      </ul>
      {status.move.running && (
        <div className="storage-move" role="status">
          <div className="quota-track">
            <span
              style={{
                width:
                  Math.min(100, (status.move.done_bytes / Math.max(1, status.move.bytes)) * 100) +
                  '%',
              }}
            />
          </div>
          <p>
            {t('storage.moving', {
              done: formatNumber(status.move.done),
              files: formatNumber(status.move.files),
              size: formatBytes(status.move.bytes),
              target: target?.id === 'local' ? t('storage.local') : (target?.path ?? ''),
            })}
          </p>
          <button
            className="secondary small"
            onClick={() => void act(() => api('/api/admin/storage/move', 'DELETE', {}))}
          >
            <Square size={14} />
            {t('storage.stop')}
          </button>
        </div>
      )}
      <form
        className="token-create storage-add"
        onSubmit={(e) => {
          e.preventDefault();
          void act(async () => {
            await api('/api/admin/storage/locations', 'POST', {
              path: path.trim(),
              language: getLanguage(),
            });
            setPath('');
          }, t('storage.added'));
        }}
      >
        <label>
          {t('storage.addLabel')}
          <input
            className="mono"
            value={path}
            placeholder="/mnt/share"
            autoCapitalize="off"
            spellCheck={false}
            onChange={(e) => setPath(e.target.value)}
          />
        </label>
        <button className="primary" disabled={busy || !path.trim()}>
          <FolderPlus size={16} />
          {t('storage.add')}
        </button>
      </form>
      {suggestions.length > 0 && (
        <div className="storage-candidates">
          <span className="muted">{t('storage.detected')}</span>
          {suggestions.map((c) => (
            <button
              type="button"
              key={c.path}
              className="secondary small"
              onClick={() => setPath(c.path)}
            >
              <span className="mono">{c.path}</span>
              <span className="muted">
                {c.type} · {t('storage.freeShort', { free: formatBytes(c.free_bytes) })}
              </span>
            </button>
          ))}
        </div>
      )}
      <details className="secret-details storage-help">
        <summary>{t('storage.howTo')}</summary>
        <p className="muted">{t('storage.howTo1')}</p>
        <code className="secret">
          {`//NAS-IP/share  /mnt/magisend-share  cifs  credentials=/root/.smb-nas,uid=${uid},gid=${gid},file_mode=0600,dir_mode=0700,vers=3.1.1,_netdev,nofail,x-systemd.automount  0 0`}
        </code>
        <p className="muted">{t('storage.howTo2')}</p>
        <code className="secret">pct set CTID -mp0 /mnt/magisend-share,mp=/mnt/share</code>
        <p className="muted">
          {t('storage.howTo3', { uid: String(status.uid), gid: String(status.gid) })}
        </p>
      </details>
    </section>
  );
}
