import { useEffect, useRef, useState } from 'react';
import { ArrowUpRight, CheckCircle2, Clock, LoaderCircle, Lock, Upload, X } from 'lucide-react';
import { api, ApiError, formatBytes, responseData } from './api';
import LocalThumb from './LocalThumb';
import { t, useLanguage, useMessage, formatDate, formatNumber, message } from './i18n';
import { uploadAll, guardUnload, type UploadStats } from './uploader';
import { UploadSpeed } from './UploadSpeed';

interface RequestInfo {
  request: {
    id: string;
    title: string;
    message: string;
    expires: number;
    protected: boolean;
  };
  owner: string;
  unlocked: boolean;
  max_file: number;
  max_upload: number;
}

interface PendingFile {
  id: string;
  name: string;
  size: number;
  offset: number;
}
interface Batch {
  upload: string;
  token: string;
  files: PendingFile[];
}
interface SavedBatch extends Batch {
  known: { name: string; size: number; lastModified: number }[];
}

// The batch being sent is remembered in this browser (with its upload token)
// so a closed tab or lost connection can continue later.
const batchKey = (request: string) => 'magisend.request-upload.' + request;
function savedBatch(request: string): SavedBatch | null {
  try {
    const value = JSON.parse(localStorage.getItem(batchKey(request)) || 'null');
    return value && typeof value.upload === 'string' && typeof value.token === 'string'
      ? value
      : null;
  } catch {
    return null;
  }
}
function rememberBatch(request: string, batch: Batch, files: File[]) {
  try {
    localStorage.setItem(
      batchKey(request),
      JSON.stringify({
        upload: batch.upload,
        token: batch.token,
        files: [],
        known: files.map((f) => ({ name: f.name, size: f.size, lastModified: f.lastModified })),
      }),
    );
  } catch {
    // Without storage an interrupted upload starts over.
  }
}
function forgetBatch(request: string) {
  try {
    localStorage.removeItem(batchKey(request));
  } catch {
    // Nothing to forget.
  }
}

// The page behind an upload link: anyone with it can send files to the owner.
// Visitors never see what others uploaded.
export default function RequestPage({ id }: { id: string }) {
  useLanguage();
  const [info, setInfo] = useState<RequestInfo | null>(null);
  const [gone, setGone] = useState(false);
  const [password, setPassword] = useState('');
  const [sender, setSender] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [drag, setDrag] = useState(false);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const [sent, setSent] = useState(0);
  const [stats, setStats] = useState<UploadStats | null>(null);
  const [unfinished, setUnfinished] = useState<Batch | null>(null);
  const [error, setError] = useMessage();
  const picker = useRef<HTMLInputElement>(null);
  const resuming = useRef(false);
  const base = '/api/requests/' + id;

  async function load() {
    try {
      setInfo(await api<RequestInfo>(base));
    } catch (e) {
      if (e instanceof ApiError && (e.status === 404 || e.status === 410)) setGone(true);
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
  }, [id]);

  async function unlock(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api(base + '/unlock', 'POST', { password });
      setPassword('');
      await load();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  function add(incoming: File[]) {
    if (busy || !info) return;
    setSent(0);
    setError('');
    const next = [...files];
    for (const file of incoming) {
      if (file.size > info.max_file) {
        setError(message('request.tooLarge', { name: file.name, max: formatBytes(info.max_file) }));
        continue;
      }
      if (!next.some((f) => f.name === file.name)) next.push(file);
    }
    setFiles(next.slice(0, 100));
  }

  // Sends what is missing of a batch, then marks it complete.
  async function transfer(batch: Batch, pairs: { target: PendingFile; source: File }[]) {
    setBusy(true);
    setError('');
    setProgress(0);
    const headers = { 'X-MagiSend': '1', 'X-Upload-Token': batch.token };
    const batchUrl = base + '/uploads/' + batch.upload;
    const release = guardUnload(true);
    try {
      await uploadAll(
        pairs.map(({ target, source }) => ({
          source,
          size: target.size,
          offset: target.offset,
          url: batchUrl + '/files/' + target.id,
          headers: { 'X-Upload-Token': batch.token },
          status: async (signal: AbortSignal) => {
            const now = await responseData<{ files: PendingFile[] }>(
              await fetch(batchUrl, { credentials: 'same-origin', headers, signal }),
            );
            return now.files.find((f) => f.id === target.id)?.offset ?? -1;
          },
        })),
        {
          onProgress: (s) => {
            setStats(s);
            setProgress(s.total ? Math.round((s.sent / s.total) * 100) : 100);
          },
        },
      );
      await responseData(
        await fetch(batchUrl + '/complete', {
          method: 'POST',
          credentials: 'same-origin',
          headers,
        }),
      );
      forgetBatch(id);
      setUnfinished(null);
      setSent(pairs.length);
      setFiles([]);
      if (picker.current) picker.current.value = '';
    } catch (e) {
      setError(e as Error);
      // What arrived is kept; the next visit offers to continue.
      void check();
    } finally {
      release();
      setStats(null);
      setBusy(false);
    }
  }

  async function send() {
    if (!files.length) return;
    setBusy(true);
    setError('');
    let started: { upload: string; token: string; files: PendingFile[] };
    try {
      started = await api(base + '/uploads', 'POST', {
        sender,
        files: files.map((f) => ({ name: f.name, size: f.size })),
      });
    } catch (e) {
      setError(e as Error);
      setBusy(false);
      return;
    }
    const batch = { upload: started.upload, token: started.token, files: started.files };
    rememberBatch(id, batch, files);
    await transfer(
      batch,
      started.files.map((target) => ({
        target: { ...target, offset: 0 },
        source: files.find((f) => f.name === target.name)!,
      })),
    );
  }

  // Looks for an unfinished upload from an earlier visit in this browser.
  async function check() {
    const saved = savedBatch(id);
    if (!saved) return setUnfinished(null);
    try {
      const now = await responseData<{ files: PendingFile[] }>(
        await fetch(base + '/uploads/' + saved.upload, {
          credentials: 'same-origin',
          headers: { 'X-MagiSend': '1', 'X-Upload-Token': saved.token },
        }),
      );
      setUnfinished({ ...saved, files: now.files });
    } catch (e) {
      // Finished, removed or cleaned up: nothing to continue.
      if (e instanceof ApiError && e.status !== 429 && e.status < 500) forgetBatch(id);
      setUnfinished(null);
    }
  }
  useEffect(() => {
    if (info?.unlocked) void check();
  }, [info?.unlocked]);

  async function resume(chosen: File[]) {
    resuming.current = false;
    if (picker.current) picker.current.value = '';
    if (!unfinished) return;
    const known = savedBatch(id)?.known ?? [];
    const missing: string[] = [];
    const pairs = unfinished.files
      .filter((f) => f.offset < f.size)
      .map((target) => {
        const was = known.find((k) => k.name === target.name);
        const source = chosen.find(
          (f) =>
            f.name === target.name &&
            f.size === target.size &&
            (!was || was.lastModified === f.lastModified),
        );
        if (!source) missing.push(target.name);
        return { target, source: source! };
      });
    if (missing.length)
      return setError(
        message('request.resumeMissing', {
          count: formatNumber(missing.length),
          names: missing.slice(0, 3).join(', ') + (missing.length > 3 ? ' …' : ''),
        }),
      );
    setSent(0);
    setFiles(pairs.map((p) => p.source));
    await transfer(unfinished, pairs);
  }

  async function startOver() {
    if (!unfinished) return;
    setBusy(true);
    try {
      await responseData(
        await fetch(base + '/uploads/' + unfinished.upload, {
          method: 'DELETE',
          credentials: 'same-origin',
          headers: { 'X-MagiSend': '1', 'X-Upload-Token': unfinished.token },
        }),
      ).catch(() => {});
      forgetBatch(id);
      setUnfinished(null);
    } finally {
      setBusy(false);
    }
  }

  if (!info)
    return (
      <main className="public-page">
        {gone ? (
          <section className="panel unlock-panel">
            <h1>{t('request.unavailable')}</h1>
            {error && <p className="muted">{error}</p>}
          </section>
        ) : error ? (
          <div className="error" role="alert">
            {error}
            <button className="secondary" onClick={() => void load()}>
              {t('common.retry')}
            </button>
          </div>
        ) : (
          <div className="loading">
            <LoaderCircle className="spin" />
          </div>
        )}
      </main>
    );

  const { request } = info;
  const left = unfinished ? unfinished.files.filter((f) => f.offset < f.size) : [];
  return (
    <main className="public-page">
      <div className="eyebrow">{t('request.eyebrow')}</div>
      {!info.unlocked ? (
        <section className="panel unlock-panel">
          <div className="upload-symbol">
            <Lock size={26} />
          </div>
          <h1>{request.title}</h1>
          <p className="muted">{t('request.locked', { owner: info.owner })}</p>
          <form onSubmit={(e) => void unlock(e)}>
            <label>
              {t('auth.password')}
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
              {busy ? <LoaderCircle className="spin" size={16} /> : <Lock size={16} />}
              {t('public.open')}
            </button>
          </form>
        </section>
      ) : (
        <>
          <div className="public-heading">
            <div>
              <h1>{request.title}</h1>
              {request.message && <p className="share-description">{request.message}</p>}
              <p className="muted">
                {t('request.from', { owner: info.owner })} ·{' '}
                <Clock size={13} className="inline-icon" />{' '}
                {request.expires === 0
                  ? t('request.noExpiry')
                  : t('request.until', { date: formatDate(request.expires * 1000) })}
              </p>
            </div>
          </div>
          {sent > 0 && (
            <div className="success-note request-sent" role="status">
              <CheckCircle2 size={18} />
              {sent === 1
                ? t('request.sentOne', { owner: info.owner })
                : t('request.sent', { count: formatNumber(sent), owner: info.owner })}
            </div>
          )}
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
          {unfinished && !busy && (
            <div className="upload-status unfinished panel" role="status">
              <strong>
                {t(left.length === 1 ? 'request.unfinishedOne' : 'request.unfinished', {
                  count: formatNumber(left.length),
                  size: formatBytes(unfinished.files.reduce((n, f) => n + f.size - f.offset, 0)),
                })}
              </strong>
              <span className="muted">{t('request.unfinishedHint')}</span>
              <div className="row-actions">
                <button
                  className="secondary small"
                  onClick={() => {
                    resuming.current = true;
                    picker.current?.click();
                  }}
                >
                  <Upload size={15} />
                  {t('request.resumeFiles')}
                </button>
                <button className="quiet small" onClick={() => void startOver()}>
                  {t('request.discard')}
                </button>
              </div>
            </div>
          )}
          <section className="panel request-panel">
            <div
              className={'dropzone ' + (drag ? 'dragging' : '')}
              tabIndex={0}
              aria-label={t('upload.drop')}
              onDragOver={(e) => {
                e.preventDefault();
                if (!busy) setDrag(true);
              }}
              onDragLeave={() => setDrag(false)}
              onDrop={(e) => {
                e.preventDefault();
                setDrag(false);
                add(Array.from(e.dataTransfer.files));
              }}
            >
              <div className="upload-symbol">
                <Upload size={30} />
              </div>
              <h2>{t('upload.drop')}</h2>
              <p>{t('request.dropHint', { owner: info.owner })}</p>
              <button
                className="secondary"
                disabled={busy}
                onClick={() => {
                  resuming.current = false;
                  picker.current?.click();
                }}
              >
                {t('upload.choose')}
                <ArrowUpRight size={16} />
              </button>
              <input
                ref={picker}
                className="sr-only"
                type="file"
                aria-label={t('upload.choose')}
                multiple
                disabled={busy}
                onChange={(e) => {
                  const chosen = Array.from(e.target.files || []);
                  if (resuming.current) void resume(chosen);
                  else add(chosen);
                }}
              />
              <small>
                {t('request.limits', {
                  file: formatBytes(info.max_file),
                  total: formatBytes(info.max_upload),
                })}
              </small>
            </div>
            {files.length > 0 && (
              <div className="request-send">
                <div className="selected-files">
                  {files.map((f) => (
                    <div className="file-row" key={f.name}>
                      <LocalThumb file={f} />
                      <div className="file-name">
                        <strong>{f.name}</strong>
                        <small>{formatBytes(f.size)}</small>
                      </div>
                      <button
                        className="icon-button"
                        disabled={busy}
                        aria-label={t('request.removeNamed', { name: f.name })}
                        onClick={() => setFiles(files.filter((x) => x !== f))}
                      >
                        <X size={16} />
                      </button>
                    </div>
                  ))}
                </div>
                <label>
                  {t('request.yourName')} <span className="optional">{t('share.optional')}</span>
                  <input
                    value={sender}
                    maxLength={80}
                    disabled={busy}
                    autoComplete="name"
                    onChange={(e) => setSender(e.target.value)}
                  />
                </label>
                {busy && <progress aria-label={t('upload.uploading')} value={progress} max={100} />}
                {busy && <UploadSpeed stats={stats} />}
                <button className="primary full" disabled={busy} onClick={() => void send()}>
                  {busy ? <LoaderCircle className="spin" size={16} /> : <Upload size={16} />}
                  {busy
                    ? t('request.sending', { percent: formatNumber(progress) })
                    : files.length === 1
                      ? t('request.sendOne')
                      : t('request.send', { count: formatNumber(files.length) })}
                </button>
              </div>
            )}
          </section>
          <p className="public-note">{t('request.privacy', { owner: info.owner })}</p>
        </>
      )}
    </main>
  );
}
