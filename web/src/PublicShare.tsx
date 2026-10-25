import { ScanNote } from './ScanNote';
import { usePaged, ShowMore } from './Paging';
import { useState, useEffect, useRef } from 'react';
import {
  Lock,
  ArrowDownToLine,
  Clock,
  LoaderCircle,
  PackageOpen,
  ArrowUpRight,
  ShieldCheck,
  CloudUpload,
  Pencil,
  Trash2,
  Files,
  Folder as FolderIcon,
} from 'lucide-react';
import { useLook } from './look';
import { filesText } from './SendLayouts';
import { api, ApiError, responseData, formatBytes, type Share, type SharedFile } from './api';
import FilePreview from './FilePreview';
import { brandName } from './branding';
import FileThumb from './FileThumb';
import TextEditor from './TextEditor';
import PublicNote from './PublicNote';
import { Modal } from './Modal';
import { useFollow } from './follow';
import {
  t,
  getLanguage,
  useLanguage,
  formatDate,
  formatNumber,
  useMessage,
  message,
  LocalizedError,
} from './i18n';
interface Rights {
  authenticated?: boolean;
  edit: boolean;
  add: boolean;
  delete: boolean;
}
// Server rights drive every control: a visitor who is not a named recipient
// sees a read-only page plus a login form; rights are only a UI mirror, every
// write is re-verified server-side against current capabilities and membership.
// "in 6 days", "in 3 hours": how long the link still works.
function expiresIn(expires: number) {
  const seconds = Math.max(0, expires - Date.now() / 1000);
  const format = new Intl.RelativeTimeFormat(getLanguage(), { numeric: 'auto' });
  if (seconds >= 2 * 86400) return format.format(Math.floor(seconds / 86400), 'day');
  if (seconds >= 3600) return format.format(Math.floor(seconds / 3600), 'hour');
  return format.format(Math.max(1, Math.floor(seconds / 60)), 'minute');
}

export default function PublicShare({ id }: { id: string }) {
  const look = useLook();
  const [preview, setPreview] = useState<SharedFile | null>(null);
  const [editing, setEditing] = useState<SharedFile | null>(null);
  useLanguage();
  const [share, setShare] = useState<Share | null>(null);
  // A shared folder keeps its subfolders: `dir` is the one being looked at,
  // such as "Trip/day2", and '' for the top.
  const [dir, setDir] = useState('');
  const all = share?.files ?? [];
  const inDir = all.filter((f) => (f.folder ?? '') === dir);
  // A link that holds a single note shows the note itself.
  const note =
    share && all.length === 1 && !share.download_limit && /\.(md|markdown)$/i.test(all[0].name)
      ? all[0]
      : null;
  const [noteVersion, setNoteVersion] = useState(0);
  const subdirs = new Map<string, { count: number; size: number }>();
  const prefix = dir ? dir + '/' : '';
  for (const f of all) {
    if (!f.folder?.startsWith(prefix) || f.folder === dir) continue;
    const name = f.folder.slice(prefix.length).split('/')[0];
    const seen = subdirs.get(name) ?? { count: 0, size: 0 };
    subdirs.set(name, { count: seen.count + 1, size: seen.size + f.size });
  }
  const crumbs = dir ? dir.split('/') : [];
  // A shared folder can hold many files; show them a page at a time.
  const paged = usePaged(inDir, id + '/' + dir);
  // The page is not reloaded after a download, so show the new count right away.
  const countDown = (file: string) =>
    setShare(
      (current) =>
        current && {
          ...current,
          files: current.files.map((f) =>
            f.id === file && f.remaining ? { ...f, remaining: f.remaining - 1 } : f,
          ),
        },
    );
  const [locked, setLocked] = useState(false);
  const [error, setError] = useMessage();
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(true);
  const [terminal, setTerminal] = useState(false);
  const [rights, setRights] = useState<Rights | null>(null);
  const [credentials, setCredentials] = useState({ username: '', password: '' });
  // Asked for once the password was right for an account with two-step login.
  const [needCode, setNeedCode] = useState(false);
  const [loginCode, setLoginCode] = useState('');
  const [loginBusy, setLoginBusy] = useState(false);
  const [addBusy, setAddBusy] = useState(false);
  const [deleting, setDeleting] = useState<SharedFile | null>(null);
  const addPicker = useRef<HTMLInputElement>(null);
  async function load() {
    setBusy(true);
    setError('');
    setTerminal(false);
    setLocked(false);
    setShare(null);
    try {
      setShare(await api<Share>('/api/public/' + id));
      setLocked(false);
      setPassword('');
      setError('');
      try {
        setRights(await api<Rights>('/api/public/' + id + '/permissions'));
      } catch {
        setRights(null);
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setLocked(true);
        setError('');
      } else {
        setError(e as Error);
        setTerminal(e instanceof ApiError && (e.status === 404 || e.status === 410));
      }
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    setPassword('');
    setPreview(null);
    setEditing(null);
    setRights(null);
    setDeleting(null);
    setDir('');
    void load();
  }, [id]);
  async function refresh() {
    setRights(null);
    await load();
  }
  // While files wait for their virus scan, the page follows.
  // Only the share is fetched again, so nothing open on the page is reset.
  const scanning = !!share?.files.some((f) => f.scan === 'pending');
  useFollow(scanning, async () => setShare(await api<Share>('/api/public/' + id)));
  async function unlock(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api('/api/public/' + id + '/unlock', 'POST', { password });
      setPassword('');
      await load();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function signIn(e: React.FormEvent) {
    e.preventDefault();
    setLoginBusy(true);
    setError('');
    try {
      await api('/api/login', 'POST', needCode ? { ...credentials, code: loginCode } : credentials);
      setCredentials({ username: '', password: '' });
      setNeedCode(false);
      setLoginCode('');
      await refresh();
    } catch (err) {
      if (err instanceof ApiError && err.code === 'two_factor_required') {
        setNeedCode(true);
        return;
      }
      setError(err as Error);
    } finally {
      setLoginBusy(false);
    }
  }
  async function addFile(list: FileList | null) {
    const file = list?.[0];
    if (!file) return;
    setAddBusy(true);
    setError('');
    try {
      if (file.size > maxAdd) throw new LocalizedError(message('errors.chunk_too_large'));
      const buffer = await file.arrayBuffer();
      await responseData(
        await fetch('/api/public/' + id + '/files?name=' + encodeURIComponent(file.name), {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'X-MagiSend': '1', 'Content-Type': 'application/octet-stream' },
          body: buffer,
        }),
      );
      await refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setAddBusy(false);
      if (addPicker.current) addPicker.current.value = '';
    }
  }
  async function removeFile(file: SharedFile) {
    setBusy(true);
    setError('');
    try {
      await api('/api/public/' + id + '/files/' + file.id, 'DELETE', {});
      setDeleting(null);
      await refresh();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  }
  const maxAdd = 4 * 1024 * 1024;
  // Pictures and videos show as a gallery in the new looks.
  const gallery = look !== 'classic' && !!inDir.length && inDir.every((f) => f.has_thumb);
  // Download, edit and delete for one file; icons only in the gallery.
  const fileActions = (f: SharedFile, compact = false) => {
    const button = compact ? 'icon-button' : 'secondary';
    return (
      <div className={compact ? 'row-actions' : 'library-actions'}>
        {f.remaining === 0 || f.scan ? (
          <button
            className={button}
            disabled
            aria-label={t('public.downloadNamed', { name: f.name })}
          >
            <ArrowDownToLine size={17} />
            {!compact && <span>{t('public.download')}</span>}
          </button>
        ) : (
          <a
            className={button}
            href={'/api/public/' + id + '/files/' + f.id}
            aria-label={t('public.downloadNamed', { name: f.name })}
            onClick={() => countDown(f.id)}
          >
            <ArrowDownToLine size={17} />
            {!compact && <span>{t('public.download')}</span>}
          </a>
        )}
        {rights?.edit && (
          <button
            className={button}
            aria-label={t('library.editNamed', { name: f.name })}
            disabled={busy}
            onClick={() => setEditing(f)}
          >
            <Pencil size={16} />
            {!compact && <span>{t('library.edit')}</span>}
          </button>
        )}
        {rights?.delete && (
          <button
            className={compact ? 'icon-button danger' : 'quiet danger'}
            aria-label={t('public.deleteNamed', { name: f.name })}
            disabled={busy}
            onClick={() => setDeleting(f)}
          >
            <Trash2 size={16} />
            {!compact && <span>{t('share.delete')}</span>}
          </button>
        )}
      </div>
    );
  };
  return (
    <main className="public-page">
      <div className="eyebrow">
        {look === 'classic'
          ? t('public.eyebrow', { name: brandName().toUpperCase() })
          : t('public.sharedVia', { name: brandName() })}
      </div>
      {locked ? (
        <section className="panel unlock-panel">
          <div className="upload-symbol">
            <Lock size={26} />
          </div>
          <h1>{t('public.lockedTitle')}</h1>
          <p className="muted">{t('public.lockedDescription')}</p>
          <form onSubmit={unlock}>
            <label>
              {t('public.password')}
              <input
                type="password"
                autoComplete="off"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            {error && (
              <div className="error" role="alert">
                {error}
              </div>
            )}
            <button className="primary full" disabled={busy}>
              {busy ? <LoaderCircle className="spin" size={16} /> : <Lock size={16} />}{' '}
              {t('public.open')}
            </button>
          </form>
        </section>
      ) : share ? (
        <>
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
          {note ? (
            <PublicNote
              share={share}
              file={note}
              version={noteVersion}
              actions={fileActions(note)}
            />
          ) : (
            <>
              {look === 'classic' ? (
                <div className="public-heading">
                  <div>
                    <h1>{share.title}</h1>
                    {share.description && <p className="share-description">{share.description}</p>}
                    <p className="muted">
                      {t('files.summary', {
                        count: formatNumber(share.files.length),
                        size: formatBytes(share.files.reduce((n, f) => n + f.size, 0)),
                      })}{' '}
                      <span>
                        ·{' '}
                        {share.expires === 0
                          ? t('share.neverExpires')
                          : t('share.expires', { date: formatDate(share.expires * 1000) })}
                      </span>
                    </p>
                    {!!share.download_limit && (
                      <p className="limit-note">
                        {share.download_limit === 1
                          ? t('downloads.publicNoteOnce')
                          : t('downloads.publicNote', {
                              count: formatNumber(share.download_limit),
                            })}
                      </p>
                    )}
                  </div>
                  <a
                    className="primary"
                    href={'/api/public/' + id + '/zip'}
                    onClick={() => share.files.forEach((f) => countDown(f.id))}
                  >
                    <PackageOpen size={18} />
                    {t('public.downloadAll')}
                  </a>
                </div>
              ) : (
                <section className="public-hero">
                  <div className="public-hero-text">
                    <h1>{share.title}</h1>
                    {share.description && <p className="share-description">{share.description}</p>}
                    <div className="public-chips">
                      <span>
                        <Files size={15} />
                        {filesText(
                          share.files.length,
                          share.files.reduce((n, f) => n + f.size, 0),
                        )}
                      </span>
                      <span>
                        <Clock size={15} />
                        {share.expires === 0
                          ? t('share.neverExpires')
                          : t('public.expiresIn', { when: expiresIn(share.expires) })}
                      </span>
                      {!!share.download_limit && (
                        <span>
                          <ArrowDownToLine size={15} />
                          {share.download_limit === 1
                            ? t('downloads.publicNoteOnce')
                            : t('downloads.publicNote', {
                                count: formatNumber(share.download_limit),
                              })}
                        </span>
                      )}
                    </div>
                  </div>
                  <a
                    className="primary public-download-all"
                    href={'/api/public/' + id + '/zip'}
                    onClick={() => share.files.forEach((f) => countDown(f.id))}
                  >
                    <PackageOpen size={20} />
                    {t('public.downloadAll')}
                  </a>
                </section>
              )}
              {all.some((f) => f.folder) && (
                <h2 className="folder-crumbs public-crumbs">
                  {dir ? (
                    <>
                      <button className="crumb" onClick={() => setDir('')}>
                        {share.title}
                      </button>
                      {crumbs.map((name, i) => (
                        <span key={i}>
                          <span className="crumb-sep" aria-hidden="true">
                            /
                          </span>
                          {i < crumbs.length - 1 ? (
                            <button
                              className="crumb"
                              onClick={() => setDir(crumbs.slice(0, i + 1).join('/'))}
                            >
                              {name}
                            </button>
                          ) : (
                            name
                          )}
                        </span>
                      ))}
                    </>
                  ) : (
                    share.title
                  )}
                </h2>
              )}
              {gallery || (look !== 'classic' && !inDir.length && subdirs.size > 0) ? (
                <div className="library-grid public-gallery">
                  {[...subdirs].map(([name, inside]) => (
                    <button
                      key={name}
                      className="folder-tile"
                      onClick={() => setDir(prefix + name)}
                    >
                      <span className="tile-preview">
                        <FolderIcon size={32} />
                      </span>
                      <span className="tile-info">
                        <strong>{name}</strong>
                        <small>{filesText(inside.count, inside.size)}</small>
                      </span>
                    </button>
                  ))}
                  {paged.shown.map((f) => (
                    <div className="file-tile" key={f.id}>
                      {share.download_limit ? (
                        <div className="tile-preview">
                          <FileThumb shareId={id} file={f} />
                        </div>
                      ) : (
                        <button
                          className="tile-preview"
                          aria-label={f.name}
                          onClick={() => setPreview(f)}
                        >
                          <FileThumb shareId={id} file={f} />
                        </button>
                      )}
                      <div className="tile-info">
                        <span className="file-name">
                          <strong title={f.name}>{f.name}</strong>
                          <small>{formatBytes(f.size)}</small>
                          <ScanNote scan={f.scan} />
                        </span>
                      </div>
                      {fileActions(f, true)}
                    </div>
                  ))}
                </div>
              ) : (
                <div className="panel public-files">
                  {[...subdirs].map(([name, inside]) => (
                    <div className="file-row folder-row" key={name}>
                      <span className="file-icon folder-icon">
                        <FolderIcon size={19} />
                      </span>
                      <div className="file-name">
                        <button className="filename-preview" onClick={() => setDir(prefix + name)}>
                          {name}
                        </button>
                        <small>{filesText(inside.count, inside.size)}</small>
                      </div>
                    </div>
                  ))}
                  {paged.shown.map((f) => (
                    <div className="file-row" key={f.id}>
                      <FileThumb shareId={id} file={f} />
                      <div className="file-name">
                        {share.download_limit ? (
                          <span className="filename">{f.name}</span>
                        ) : (
                          <button className="filename-preview" onClick={() => setPreview(f)}>
                            {f.name}
                          </button>
                        )}
                        <small>
                          {formatBytes(f.size)}
                          {f.remaining !== undefined &&
                            ' · ' +
                              (f.remaining > 0
                                ? t('downloads.remaining', { count: formatNumber(f.remaining) })
                                : t('downloads.usedUp'))}
                        </small>
                        <ScanNote scan={f.scan} />
                      </div>
                      {fileActions(f)}
                    </div>
                  ))}
                </div>
              )}
              <ShowMore paged={paged} />
            </>
          )}
          {rights?.add && (
            <section className="panel public-section">
              <button
                className="secondary full"
                disabled={addBusy || busy}
                onClick={() => addPicker.current?.click()}
              >
                <CloudUpload size={16} />
                {t('public.addFile', { max: formatBytes(maxAdd) })}
              </button>
              <input
                ref={addPicker}
                type="file"
                className="sr-only"
                aria-hidden="true"
                tabIndex={-1}
                disabled={addBusy || busy}
                onChange={(e) => {
                  void addFile(e.target.files);
                }}
              />
            </section>
          )}
          {rights?.authenticated === false && (
            <section className="panel public-section login-panel">
              <div>
                <h2>{t('auth.login')}</h2>
                <p className="muted">{t('public.signInHint')}</p>
              </div>
              <form onSubmit={signIn}>
                <label>
                  {t('auth.username')}
                  <input
                    required
                    autoComplete="username"
                    value={credentials.username}
                    onChange={(e) => setCredentials({ ...credentials, username: e.target.value })}
                  />
                </label>
                <label>
                  {t('auth.password')}
                  <input
                    type="password"
                    required
                    autoComplete="current-password"
                    value={credentials.password}
                    onChange={(e) => setCredentials({ ...credentials, password: e.target.value })}
                  />
                </label>
                {needCode && (
                  <>
                    <label>
                      {t('auth.code')}
                      <input
                        className="mono"
                        required
                        autoFocus
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        aria-describedby="public-code-help"
                        value={loginCode}
                        onChange={(e) => setLoginCode(e.target.value)}
                      />
                    </label>
                    <small id="public-code-help">{t('auth.codeHint')}</small>
                  </>
                )}
                <button className="primary full" disabled={loginBusy}>
                  {loginBusy ? <LoaderCircle className="spin" size={16} /> : <Lock size={16} />}{' '}
                  {t('auth.login')}
                </button>
              </form>
            </section>
          )}
          {look === 'classic' ? (
            <>
              <div className="send-bottom">
                <span>
                  <Clock size={16} />{' '}
                  {share.expires === 0 ? t('public.permanent') : t('public.expiry')}
                </span>
                <span>
                  <ShieldCheck size={16} /> {t('app.footerSelfHosted')}
                </span>
              </div>
              <p className="download-note muted">{t('public.trust')}</p>
            </>
          ) : (
            <p className="public-trust">
              <ShieldCheck size={15} />
              <span>{t('public.trust')}</span>
            </p>
          )}
        </>
      ) : error ? (
        <section className="empty-state">
          <PackageOpen size={42} />
          <h1>{t('public.unavailable')}</h1>
          <p role="alert">{error}</p>
          {!terminal && (
            <button className="secondary" disabled={busy} onClick={() => void load()}>
              {t('common.retry')}
            </button>
          )}
          <a className="secondary" href="/">
            {t('public.home')}
            <ArrowUpRight size={17} />
          </a>
        </section>
      ) : (
        <div className="loading">
          <LoaderCircle className="spin" />
          {t('public.loading')}
        </div>
      )}
      {preview && share && (
        <FilePreview
          shareId={id}
          file={preview}
          files={inDir}
          show={setPreview}
          close={() => setPreview(null)}
        />
      )}
      {editing && share && (
        <TextEditor
          key={'public-' + id + '/' + editing.id}
          base={'/api/public/' + id + '/files/' + editing.id}
          name={editing.name}
          editable
          close={() => setEditing(null)}
          changed={() => {
            setEditing(null);
            setNoteVersion((v) => v + 1);
            void refresh();
          }}
        />
      )}
      {deleting && share && (
        <Modal title={t('share.delete')} close={() => setDeleting(null)}>
          <p>{t('library.deleteWarning')}</p>
          <div className="dialog-actions">
            <button className="secondary" onClick={() => setDeleting(null)}>
              {t('common.cancel')}
            </button>
            <button
              className="primary danger-solid"
              disabled={busy}
              onClick={() => void removeFile(deleting)}
            >
              <Trash2 size={16} />
              {t('share.delete')}
            </button>
          </div>
        </Modal>
      )}
    </main>
  );
}
