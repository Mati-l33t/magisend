import { useEffect, useRef, useState } from 'react';
import {
  ArrowUpRight,
  CircleCheck,
  Download,
  LoaderCircle,
  RefreshCw,
  Sparkles,
} from 'lucide-react';
import { api } from './api';
import { t, useLanguage, useMessage, formatDate } from './i18n';

export interface UpdateInfo {
  current: string;
  latest: string;
  available: boolean;
  enabled: boolean;
  mode: 'native' | 'docker' | 'manual';
  can_apply: boolean;
  pending: boolean;
  failed: boolean;
  repo: string;
  release_url?: string;
  changelog_url?: string;
  checked?: number;
  status?: { state: string; from?: string; to?: string; message?: string; time?: string } | null;
}

export function useUpdateInfo(enabled: boolean) {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  useEffect(() => {
    if (!enabled) return;
    api<UpdateInfo>('/api/admin/update')
      .then(setInfo)
      .catch(() => setInfo(null));
  }, [enabled]);
  return [info, setInfo] as const;
}

// Shown to administrators above every page while a newer release exists.
export function UpdateBanner({ info, open }: { info: UpdateInfo; open: () => void }) {
  useLanguage();
  if (!info.available) return null;
  return (
    <div className="update-banner" role="status">
      <Sparkles size={16} />
      <span>{t('update.banner', { latest: info.latest, current: info.current })}</span>
      {info.changelog_url && (
        <a href={info.changelog_url} target="_blank" rel="noreferrer noopener">
          {t('update.changelog')}
        </a>
      )}
      <button className="secondary small" onClick={open}>
        {t('update.view')}
      </button>
    </div>
  );
}

const running = (info: UpdateInfo) =>
  info.pending || info.status?.state === 'running' || info.status?.state === 'requested';

export function UpdatePanel({ initial }: { initial: UpdateInfo | null }) {
  useLanguage();
  const [info, setInfo] = useState<UpdateInfo | null>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const startVersion = useRef(initial?.current);

  useEffect(() => {
    if (!info) void api<UpdateInfo>('/api/admin/update').then(setInfo, (e) => setError(e as Error));
  }, []);

  // While the updater works, follow its status file. The server restarts in
  // the middle, so failed requests are expected and simply retried; a changed
  // version means the new release is running and the page reloads into it.
  useEffect(() => {
    if (!info || !running(info)) return;
    const timer = window.setInterval(async () => {
      try {
        const next = await api<UpdateInfo>('/api/admin/update');
        if (next.current !== startVersion.current) window.location.reload();
        else setInfo(next);
      } catch {
        // restarting
      }
    }, 3000);
    return () => window.clearInterval(timer);
  }, [info]);

  async function check() {
    setBusy(true);
    setError('');
    try {
      setInfo(await api<UpdateInfo>('/api/admin/update/check', 'POST', {}));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    if (!info || !window.confirm(t('update.confirm', { version: info.latest }))) return;
    setBusy(true);
    setError('');
    try {
      startVersion.current = info.current;
      setInfo(await api<UpdateInfo>('/api/admin/update', 'POST', {}));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  const status = info?.status;
  return (
    <section className="panel admin-wide" id="updates">
      <h2>
        <RefreshCw size={18} /> {t('update.title')}
      </h2>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {info && (
        <>
          <div className="update-summary">
            <div>
              <small>{t('update.current')}</small>
              <strong className="mono">{info.current}</strong>
            </div>
            <div>
              <small>{t('update.latest')}</small>
              <strong className="mono">{info.latest || '—'}</strong>
            </div>
            <div className="update-state">
              {running(info) ? (
                <span className="badge accent">
                  <LoaderCircle className="spin" size={12} />
                  {t('update.running', { version: info.latest })}
                </span>
              ) : info.available ? (
                <span className="badge accent">
                  <Sparkles size={12} />
                  {t('update.available', { version: info.latest })}
                </span>
              ) : info.latest ? (
                <span className="badge">
                  <CircleCheck size={12} />
                  {t('update.upToDate')}
                </span>
              ) : null}
              <small>
                {!info.enabled
                  ? t('update.disabled')
                  : info.failed
                    ? t('update.checkFailed')
                    : info.checked
                      ? t('update.checked', { date: formatDate(info.checked * 1000, true) })
                      : t('update.never')}
              </small>
            </div>
          </div>
          {status && !running(info) && status.state !== 'success' && (
            <p className="error">
              {status.state === 'rolled_back'
                ? t('update.rolledBack', { version: status.to || info.latest })
                : t('update.failed', { message: status.message || status.state })}
            </p>
          )}
          {status?.state === 'success' && status.to === info.current && (
            <p className="success-note">{t('update.success', { version: info.current })}</p>
          )}
          {info.available && !info.can_apply && (
            <div className="update-help">
              {info.mode === 'docker' ? (
                <>
                  <p className="muted">{t('update.docker')}</p>
                  <code>docker compose pull && docker compose up -d</code>
                </>
              ) : (
                <p className="muted">{t('update.manual')}</p>
              )}
            </div>
          )}
          <div className="update-actions">
            {info.available && info.can_apply && (
              <button
                className="primary"
                disabled={busy || running(info)}
                onClick={() => void apply()}
              >
                <Download size={16} />
                {t('update.apply', { version: info.latest })}
              </button>
            )}
            <button
              className="secondary"
              disabled={busy || running(info) || !info.enabled}
              onClick={() => void check()}
            >
              {busy ? <LoaderCircle className="spin" size={16} /> : <RefreshCw size={16} />}
              {t('update.check')}
            </button>
            {info.changelog_url && (
              <a
                className="quiet"
                href={info.changelog_url}
                target="_blank"
                rel="noreferrer noopener"
              >
                {t('update.changelog')}
                <ArrowUpRight size={14} />
              </a>
            )}
            {info.release_url && (
              <a
                className="quiet"
                href={info.release_url}
                target="_blank"
                rel="noreferrer noopener"
              >
                {t('update.release')}
                <ArrowUpRight size={14} />
              </a>
            )}
          </div>
        </>
      )}
    </section>
  );
}
