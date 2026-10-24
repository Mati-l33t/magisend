import { useEffect, useState } from 'react';
import { ArrowDownToLine, DatabaseBackup, LoaderCircle, Play, Save, Trash2 } from 'lucide-react';
import { api, formatBytes } from './api';
import { Select } from './Select';
import { SettingRow } from './SettingRow';
import { formatDate, formatNumber, message, t, useLanguage, useMessage } from './i18n';

interface BackupSettings {
  dir: string;
  every: number;
  keep: number;
  files: boolean;
}
interface BackupFile {
  name: string;
  size: number;
  created: number;
}
interface BackupState {
  settings: BackupSettings;
  folder: string;
  last: number;
  error: string;
  state: { running: boolean; done: number; total: number };
  backups: BackupFile[];
}

// Administration → Backups: when backups are made, where they go, and the
// ones that exist.
export function BackupPanel() {
  useLanguage();
  const [state, setState] = useState<BackupState | null>(null);
  const [form, setForm] = useState<BackupSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [note, setNote] = useMessage();
  const [missing, setMissing] = useState(false);
  async function load() {
    const next = await api<BackupState>('/api/admin/backups');
    // An older server has no backups to show.
    if (!next?.settings || !next.state) {
      setMissing(true);
      return;
    }
    setState({ ...next, backups: Array.isArray(next.backups) ? next.backups : [] });
    setForm((current) => current ?? next.settings);
  }
  useEffect(() => {
    load().catch((e) => setError(e as Error));
  }, []);
  // While a backup runs, its progress is shown.
  useEffect(() => {
    if (!state?.state.running) return;
    const timer = window.setInterval(() => void load().catch(() => {}), 1500);
    return () => window.clearInterval(timer);
  }, [state?.state.running]);
  async function run(fn: () => Promise<unknown>, done?: Parameters<typeof message>[0]) {
    setBusy(true);
    setError('');
    setNote('');
    try {
      await fn();
      if (done) setNote(message(done));
      await load();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  if (missing) return null;
  if (!state || !form)
    return (
      <section className="panel admin-wide">
        <h2>
          <DatabaseBackup size={18} /> {t('backup.title')}
        </h2>
        {error ? (
          <div role="alert" className="error">
            {error}
          </div>
        ) : (
          <p role="status" className="muted">
            <LoaderCircle className="spin" size={16} /> {t('preview.loading')}
          </p>
        )}
      </section>
    );
  const running = state.state;
  return (
    <section className="panel admin-wide">
      <h2>
        <DatabaseBackup size={18} /> {t('backup.title')}
      </h2>
      <p className="muted">{t('backup.intro')}</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void run(() => api('/api/admin/backups', 'PUT', form), 'backup.saved');
        }}
      >
        <SettingRow id="backup-dir" label={t('backup.folder')} hint={t('backup.folderHint')}>
          <input
            id="backup-dir"
            aria-describedby="backup-dir-hint"
            className="mono"
            value={form.dir}
            placeholder={state.folder}
            maxLength={500}
            onChange={(e) => setForm({ ...form, dir: e.target.value })}
          />
        </SettingRow>
        <SettingRow label={t('backup.schedule')}>
          <Select
            ariaLabel={t('backup.schedule')}
            value={form.every}
            onChange={(every) => setForm({ ...form, every })}
            options={[
              { value: 0, label: t('backup.off') },
              { value: 24, label: t('backup.daily') },
              { value: 168, label: t('backup.weekly') },
            ]}
          />
        </SettingRow>
        <SettingRow id="backup-keep" label={t('backup.keep')} hint={t('backup.keepHint')}>
          <input
            id="backup-keep"
            aria-describedby="backup-keep-hint"
            type="number"
            min={1}
            max={100}
            value={form.keep}
            onChange={(e) => setForm({ ...form, keep: Math.round(Number(e.target.value)) })}
          />
        </SettingRow>
        <SettingRow id="backup-files" label={t('backup.files')} hint={t('backup.filesHint')}>
          <input
            id="backup-files"
            aria-describedby="backup-files-hint"
            type="checkbox"
            className="switch"
            checked={form.files}
            onChange={(e) => setForm({ ...form, files: e.target.checked })}
          />
        </SettingRow>
        {error && (
          <div role="alert" className="error">
            {error}
          </div>
        )}
        {note && (
          <p role="status" className="success-note">
            {note}
          </p>
        )}
        <div className="setting-actions">
          <button
            type="button"
            className="secondary"
            disabled={busy || running.running}
            onClick={() => void run(() => api('/api/admin/backups', 'POST', {}), 'backup.started')}
          >
            <Play size={16} />
            {t('backup.now')}
          </button>
          <button className="primary" disabled={busy}>
            <Save size={16} />
            {t('common.save')}
          </button>
        </div>
      </form>
      {running.running ? (
        <p role="status" className="muted backup-status">
          <LoaderCircle className="spin" size={16} />{' '}
          {t('backup.running', {
            done: formatBytes(running.done),
            total: formatBytes(running.total),
          })}
        </p>
      ) : state.error ? (
        <p role="alert" className="error backup-status">
          {t('backup.failed', { date: formatDate(state.last * 1000, true), reason: state.error })}
        </p>
      ) : state.last ? (
        <p className="muted backup-status">
          {t('backup.last', { date: formatDate(state.last * 1000, true) })}
        </p>
      ) : null}
      <h3 className="backup-heading">
        {t('backup.list', { count: formatNumber(state.backups.length) })}
      </h3>
      {state.backups.length === 0 ? (
        <p className="muted">{t('backup.none')}</p>
      ) : (
        <ul className="token-list">
          {state.backups.map((b) => (
            <li key={b.name}>
              <div>
                <strong className="mono">{b.name}</strong>
                <small>
                  {formatDate(b.created * 1000, true)} · {formatBytes(b.size)}
                </small>
              </div>
              <div className="row-actions">
                <a
                  className="icon-button"
                  href={'/api/admin/backups/' + encodeURIComponent(b.name)}
                  aria-label={t('backup.download', { name: b.name })}
                  title={t('backup.download', { name: b.name })}
                >
                  <ArrowDownToLine size={16} />
                </a>
                <button
                  className="icon-button danger"
                  disabled={busy}
                  aria-label={t('backup.delete', { name: b.name })}
                  title={t('backup.delete', { name: b.name })}
                  onClick={() => {
                    if (window.confirm(t('backup.deleteConfirm', { name: b.name })))
                      void run(() =>
                        api('/api/admin/backups/' + encodeURIComponent(b.name), 'DELETE', {}),
                      );
                  }}
                >
                  <Trash2 size={16} />
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
