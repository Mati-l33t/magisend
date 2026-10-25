import { useEffect, useState } from 'react';
import { Download, LoaderCircle, PlugZap, Save, ShieldAlert, ShieldCheck } from 'lucide-react';
import { api } from './api';
import { Select } from './Select';
import { SettingRow } from './SettingRow';
import { useFollow } from './follow';
import { formatDate, formatNumber, message, t, useLanguage, useMessage } from './i18n';

interface ScanSettings {
  mode: 'off' | 'received' | 'all';
  address: string;
  max_size: number;
}
interface InfectedFile {
  file_id: string;
  name: string;
  owner: string;
  signature: string;
  scanned: number;
}
interface ScanState {
  settings: ScanSettings;
  // Where MagiSend looks for ClamAV when no address is set.
  address: string;
  counts: { pending: number; clean: number; infected: number; unscanned: number };
  infected: InfectedFile[];
  status: { error?: string; at?: number };
  // Whether ClamAV answers, and whether it can be installed from here.
  clamav?: {
    running: boolean;
    version?: string;
    install: 'native' | 'docker' | 'manual';
    can_install: boolean;
    memory?: number;
    requested?: boolean;
    progress?: { state: 'running' | 'success' | 'failed'; step?: string; message: string };
    // ClamAV here lacks the settings the install gives it.
    needs_settings?: boolean;
  };
}

// ClamAV is installed with this much memory on the machine (MB).
const clamavMemory = 2400;

// The updater's steps, shown in the page's language.
const installSteps: Record<string, string> = {
  packages: 'antivirus.stepPackages',
  database: 'antivirus.stepDatabase',
  starting: 'antivirus.stepStarting',
  stopped: 'antivirus.stepStopped',
  apt: 'antivirus.stepApt',
  memory: 'antivirus.stepMemory',
  start: 'antivirus.stepStart',
};
// A step that failed says what did not work.
const failedSteps: Record<string, string> = {
  packages: 'antivirus.failPackages',
  database: 'antivirus.failDatabase',
  starting: 'antivirus.stepStart',
};
function stepText(progress: { state: string; step?: string; message: string }) {
  const key = progress.step
    ? (progress.state === 'failed' && failedSteps[progress.step]) || installSteps[progress.step]
    : undefined;
  return key ? t(key) : progress.message;
}

const megabyte = 1024 * 1024;

// Administration → Virus scanning: whether new files are checked with
// ClamAV, where it runs, and the files in which a virus was found.
export function AntivirusPanel() {
  useLanguage();
  const [state, setState] = useState<ScanState | null>(null);
  const [form, setForm] = useState<ScanSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [note, setNote] = useMessage();
  const [missing, setMissing] = useState(false);
  async function load() {
    const next = await api<ScanState>('/api/admin/antivirus');
    // An older server has no virus scanning.
    if (!next?.settings || !next.counts) {
      setMissing(true);
      return;
    }
    setState({ ...next, infected: Array.isArray(next.infected) ? next.infected : [] });
    setForm((current) => current ?? next.settings);
  }
  useEffect(() => {
    load().catch((e) => setError(e as Error));
  }, []);
  // While files wait for their scan, or ClamAV is being installed, the page
  // follows.
  const waiting = !!state?.counts.pending && state.settings.mode !== 'off';
  const installing =
    !!state?.clamav && (!!state.clamav.requested || state.clamav.progress?.state === 'running');
  // The note that the install started goes once it is over.
  useEffect(() => {
    if (!installing) setNote('');
  }, [installing]);
  useFollow(
    waiting || installing,
    load,
    installing ? 'install ' + (state?.clamav?.progress?.step ?? '') : 'scan',
  );
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
  function install() {
    void run(() => api('/api/admin/antivirus/install', 'POST', {}), 'antivirus.installStarted');
  }
  if (missing) return null;
  if (!state || !form)
    return (
      <section className="panel admin-wide">
        <h2>
          <ShieldCheck size={18} /> {t('antivirus.title')}
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
  const counts = state.counts;
  return (
    <section className="panel admin-wide">
      <h2>
        <ShieldCheck size={18} /> {t('antivirus.title')}
      </h2>
      <p className="muted">{t('antivirus.intro')}</p>
      {state.clamav && <ClamavStatus clamav={state.clamav} busy={busy} install={install} />}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void run(() => api('/api/admin/antivirus', 'PUT', form), 'antivirus.saved');
        }}
      >
        <SettingRow label={t('antivirus.mode')} hint={t('antivirus.modeHint')}>
          <Select
            ariaLabel={t('antivirus.mode')}
            value={form.mode}
            onChange={(mode) => setForm({ ...form, mode })}
            options={[
              { value: 'off', label: t('antivirus.off') },
              { value: 'received', label: t('antivirus.received') },
              { value: 'all', label: t('antivirus.all') },
            ]}
          />
        </SettingRow>
        <details className="antivirus-advanced" open={!!state.settings.address}>
          <summary>{t('antivirus.advanced')}</summary>
          <SettingRow
            id="antivirus-address"
            label={t('antivirus.address')}
            hint={t('antivirus.addressHint')}
          >
            <input
              id="antivirus-address"
              aria-describedby="antivirus-address-hint"
              className="mono"
              value={form.address}
              placeholder={state.address}
              maxLength={300}
              onChange={(e) => setForm({ ...form, address: e.target.value })}
            />
          </SettingRow>
        </details>
        <SettingRow
          id="antivirus-size"
          label={t('antivirus.maxSize')}
          hint={t('antivirus.maxSizeHint')}
        >
          <input
            id="antivirus-size"
            aria-describedby="antivirus-size-hint"
            type="number"
            min={1}
            max={4096}
            value={Math.round(form.max_size / megabyte)}
            onChange={(e) =>
              setForm({ ...form, max_size: Math.round(Number(e.target.value)) * megabyte })
            }
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
            disabled={busy}
            onClick={() =>
              void run(async () => {
                const result = await api<{ version: string }>('/api/admin/antivirus/test', 'POST', {
                  address: form.address,
                });
                setNote(message('antivirus.works', { version: result.version }));
              })
            }
          >
            <PlugZap size={16} />
            {t('antivirus.test')}
          </button>
          <button className="primary" disabled={busy}>
            <Save size={16} />
            {t('common.save')}
          </button>
        </div>
      </form>
      {state.settings.mode !== 'off' && state.status.error && (
        <p role="alert" className="error backup-status">
          {t('antivirus.failing', {
            date: formatDate((state.status.at ?? 0) * 1000, true),
            reason: state.status.error,
          })}
        </p>
      )}
      <p className="muted backup-status" role="status">
        {t('antivirus.counts', {
          pending: formatNumber(counts.pending),
          clean: formatNumber(counts.clean),
          infected: formatNumber(counts.infected),
          unscanned: formatNumber(counts.unscanned),
        })}
      </p>
      <h3 className="backup-heading">
        {t('antivirus.found', { count: formatNumber(state.infected.length) })}
      </h3>
      {state.infected.length === 0 ? (
        <p className="muted">{t('antivirus.none')}</p>
      ) : (
        <>
          <p className="muted">{t('antivirus.foundHint')}</p>
          <ul className="token-list">
            {state.infected.map((f) => (
              <li key={f.file_id}>
                <div>
                  <strong>
                    <ShieldAlert size={15} className="danger-icon" /> {f.name}
                  </strong>
                  <small>
                    {f.signature} · {f.owner} · {formatDate(f.scanned * 1000, true)}
                  </small>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

// Whether ClamAV runs, and if not, how to get it: installed from here on a
// native installation, as a container with Docker, by hand otherwise.
function ClamavStatus({
  clamav,
  busy,
  install,
}: {
  clamav: NonNullable<ScanState['clamav']>;
  busy: boolean;
  install: () => void;
}) {
  const progress = clamav.progress;
  if (clamav.requested || progress?.state === 'running')
    return (
      <p role="status" className="notice antivirus-state">
        <LoaderCircle className="spin" size={16} />{' '}
        {t('antivirus.installing', {
          step: progress?.state === 'running' ? stepText(progress) : '…',
        })}
      </p>
    );
  if (clamav.running)
    return (
      <>
        <p role="status" className="success-note">
          <ShieldCheck size={16} /> {t('antivirus.running', { version: clamav.version ?? '' })}
        </p>
        {clamav.needs_settings && (
          <div className="notice antivirus-state" role="status">
            <p>
              <ShieldAlert size={16} /> {t('antivirus.needsSettings')}
            </p>
            {progress?.state === 'failed' && (
              <p className="error">
                {t('antivirus.installFailed', { reason: stepText(progress) })}
              </p>
            )}
            {clamav.can_install ? (
              <button type="button" className="secondary" disabled={busy} onClick={install}>
                <Download size={16} />
                {t('antivirus.applySettings')}
              </button>
            ) : (
              <p>{t('antivirus.manual')}</p>
            )}
          </div>
        )}
      </>
    );
  const lowMemory = !!clamav.memory && clamav.memory < clamavMemory;
  return (
    <div className="notice antivirus-state" role="status">
      <p>
        <ShieldAlert size={16} /> {t('antivirus.missing')}
      </p>
      {progress?.state === 'failed' && (
        <p className="error">{t('antivirus.installFailed', { reason: stepText(progress) })}</p>
      )}
      {clamav.install === 'docker' ? (
        <p>{t('antivirus.docker')}</p>
      ) : clamav.can_install && lowMemory ? (
        <p>{t('antivirus.lowMemory', { memory: formatNumber(clamav.memory ?? 0) })}</p>
      ) : clamav.can_install ? (
        <>
          <p className="muted">{t('antivirus.installHint')}</p>
          <button type="button" className="secondary" disabled={busy} onClick={install}>
            <Download size={16} />
            {t('antivirus.install')}
          </button>
        </>
      ) : (
        <p>{t('antivirus.manual')}</p>
      )}
    </div>
  );
}
