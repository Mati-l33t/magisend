import { useEffect, useState } from 'react';
import { Select } from './Select';
import { Bell, BellOff, LoaderCircle, Mail, Save, Send } from 'lucide-react';
import { api, type Share } from './api';
import { SettingRow } from './SettingRow';
import { Modal } from './Modal';
import { t, useLanguage, useMessage, message, getLanguage } from './i18n';

// Splits "a@x.se, b@y.se; c@z.se" into addresses; the server validates them.
const addresses = (value: string) =>
  value
    .split(/[\s,;]+/)
    .map((part) => part.trim())
    .filter(Boolean);

export function EmailShareDialog({ share, close }: { share: Share; close: () => void }) {
  useLanguage();
  const [to, setTo] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(0);
  const [error, setError] = useMessage();
  async function send(event: React.FormEvent) {
    event.preventDefault();
    const list = addresses(to);
    if (!list.length || list.length > 10) {
      setError(message('errors.too_many_recipients'));
      return;
    }
    setBusy(true);
    setError('');
    try {
      const result = await api<{ sent: number }>('/api/shares/' + share.id + '/email', 'POST', {
        to: list,
        message: note,
        language: getLanguage(),
      });
      setSent(result.sent);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t('email.sendTitle', { title: share.title })} close={close}>
      {sent ? (
        <>
          <p className="success-note" role="status">
            {t('email.sent', { count: sent })}
          </p>
          <div className="dialog-actions">
            <button className="primary" onClick={close}>
              {t('common.close')}
            </button>
          </div>
        </>
      ) : (
        <form className="form-stack" onSubmit={send} noValidate>
          <div>
            <label>
              {t('email.recipients')}
              <input
                type="email"
                multiple
                autoComplete="email"
                aria-describedby="email-recipients-help"
                value={to}
                onChange={(e) => setTo(e.target.value)}
                placeholder="name@example.com"
              />
            </label>
            <small id="email-recipients-help" className="field-help">
              {t('email.recipientsHint')}
            </small>
          </div>
          <label>
            {t('email.message')} <span className="optional">{t('share.optional')}</span>
            <textarea
              rows={4}
              maxLength={1000}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </label>
          {share.protected && <p className="muted">{t('email.passwordNote')}</p>}
          {error && (
            <div role="alert" className="error">
              {error}
            </div>
          )}
          <div className="dialog-actions">
            <button type="button" className="secondary" onClick={close}>
              {t('common.cancel')}
            </button>
            <button className="primary" disabled={busy || !to.trim()}>
              {busy ? <LoaderCircle className="spin" size={16} /> : <Send size={16} />}
              {t('email.send')}
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}

// The bell on a share: email the owner when its files are downloaded.
export function NotifyToggle({
  share,
  available,
  changed,
}: {
  share: Share;
  available: boolean;
  changed: (notify: boolean) => void;
}) {
  useLanguage();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  async function toggle() {
    setBusy(true);
    setError('');
    try {
      const result = await api<{ notify: boolean }>('/api/shares/' + share.id + '/notify', 'PUT', {
        enabled: !share.notify,
        language: getLanguage(),
      });
      changed(result.notify);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <button
        className={'icon-button' + (share.notify ? ' active' : '')}
        aria-pressed={!!share.notify}
        aria-label={t('email.notifyNamed', { title: share.title })}
        title={available ? t('email.notifyHint') : t('email.notifyUnavailable')}
        disabled={busy || (!available && !share.notify)}
        onClick={() => void toggle()}
      >
        {share.notify ? <Bell size={17} /> : <BellOff size={17} />}
      </button>
      {error && (
        <span role="alert" className="sr-only">
          {error}
        </span>
      )}
    </>
  );
}

export function AccountEmailDialog({
  current,
  remind: remindNow,
  close,
  saved,
}: {
  current: string;
  remind: boolean;
  close: () => void;
  saved: () => void;
}) {
  useLanguage();
  const [email, setEmail] = useState(current);
  const [password, setPassword] = useState('');
  const [remind, setRemind] = useState(remindNow);
  // A new address needs the password: password resets are sent there.
  const changing = email.trim().toLowerCase() !== current.trim().toLowerCase();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api('/api/account/email', 'PUT', {
        email,
        remind_expiry: remind,
        password: changing ? password : '',
        language: getLanguage(),
      });
      saved();
      close();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t('email.accountTitle')} close={close}>
      <form className="form-stack" onSubmit={save} noValidate>
        <p className="muted">{t('email.accountHint')}</p>
        <label>
          {t('email.address')}
          <input
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="name@example.com"
          />
        </label>
        {changing && (
          <label>
            {t('email.confirmPassword')}
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
        )}
        <label className="checkbox">
          <input type="checkbox" checked={remind} onChange={(e) => setRemind(e.target.checked)} />
          {t('email.remindExpiry')}
        </label>
        {error && (
          <div role="alert" className="error">
            {error}
          </div>
        )}
        <div className="dialog-actions">
          <button type="button" className="secondary" onClick={close}>
            {t('common.cancel')}
          </button>
          <button className="primary" disabled={busy || (changing && !password)}>
            <Save size={16} />
            {t('common.save')}
          </button>
        </div>
      </form>
    </Modal>
  );
}

interface EmailSettings {
  host: string;
  port: number;
  security: 'starttls' | 'tls' | 'none';
  username: string;
  password?: string;
  from: string;
  from_name: string;
  site_url: string;
}

export function EmailSettingsPanel({ adminEmail }: { adminEmail: string }) {
  useLanguage();
  const [settings, setSettings] = useState<EmailSettings | null>(null);
  const [passwordSet, setPasswordSet] = useState(false);
  const [password, setPassword] = useState('');
  const [testTo, setTestTo] = useState(adminEmail);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [notice, setNotice] = useMessage();
  useEffect(() => {
    api<{ settings: EmailSettings; password_set: boolean }>('/api/admin/email').then(
      (data) => {
        setSettings(data.settings);
        setPasswordSet(data.password_set);
      },
      (e) => setError(e as Error),
    );
  }, []);
  if (!settings) return null;
  const set = (patch: Partial<EmailSettings>) => setSettings({ ...settings, ...patch });
  async function save(event: React.FormEvent, clearPassword = false) {
    event.preventDefault();
    setBusy(true);
    setError('');
    setNotice('');
    try {
      const data = await api<{ settings: EmailSettings; password_set: boolean }>(
        '/api/admin/email',
        'PUT',
        { ...settings, password, clear_password: clearPassword },
      );
      setSettings(data.settings);
      setPasswordSet(data.password_set);
      setPassword('');
      setNotice(message('email.saved'));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function test() {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await api('/api/admin/email/test', 'POST', { to: testTo, language: getLanguage() });
      setNotice(message('email.testSent', { to: testTo }));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel admin-wide">
      <h2>
        <Mail size={18} /> {t('email.settingsTitle')}
      </h2>
      <p className="muted panel-intro">{t('email.settingsHint')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notice && (
        <div role="status" className="success-note">
          {notice}
        </div>
      )}
      <form className="settings-form" onSubmit={(e) => void save(e)}>
        <SettingRow id="smtp-host" label={t('email.host')} hint={t('email.hostHint')}>
          <input
            id="smtp-host"
            aria-describedby="smtp-host-hint"
            value={settings.host}
            onChange={(e) => set({ host: e.target.value })}
            placeholder="smtp.example.com"
          />
        </SettingRow>
        <SettingRow label={t('email.security')} hint={t('email.securityHint')}>
          <Select
            ariaLabel={t('email.security')}
            value={settings.security}
            onChange={(security) =>
              set({
                security,
                port: security === 'tls' ? 465 : security === 'starttls' ? 587 : 25,
              })
            }
            options={[
              { value: 'starttls', label: 'STARTTLS' },
              { value: 'tls', label: 'SSL/TLS' },
              { value: 'none', label: t('email.securityNone') },
            ]}
          />
        </SettingRow>
        <SettingRow id="smtp-port" label={t('email.port')}>
          <input
            id="smtp-port"
            type="number"
            min={1}
            max={65535}
            value={settings.port}
            onChange={(e) => set({ port: Number(e.target.value) })}
          />
        </SettingRow>
        <SettingRow
          id="smtp-username"
          label={
            <>
              {t('email.username')} <span className="optional">{t('share.optional')}</span>
            </>
          }
        >
          <input
            id="smtp-username"
            autoComplete="off"
            value={settings.username}
            onChange={(e) => set({ username: e.target.value })}
          />
        </SettingRow>
        <SettingRow id="smtp-password" label={t('email.password')}>
          <input
            id="smtp-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={passwordSet ? t('email.passwordUnchanged') : ''}
          />
        </SettingRow>
        <SettingRow id="smtp-from" label={t('email.from')} hint={t('email.fromHint')}>
          <input
            id="smtp-from"
            aria-describedby="smtp-from-hint"
            type="email"
            value={settings.from}
            onChange={(e) => set({ from: e.target.value })}
            placeholder="share@example.com"
          />
        </SettingRow>
        <SettingRow id="smtp-from-name" label={t('email.fromName')}>
          <input
            id="smtp-from-name"
            value={settings.from_name}
            maxLength={80}
            onChange={(e) => set({ from_name: e.target.value })}
          />
        </SettingRow>
        <SettingRow id="email-site" label={t('email.siteUrl')} hint={t('email.siteUrlHint')}>
          <input
            id="email-site"
            type="text"
            inputMode="url"
            autoCapitalize="off"
            spellCheck={false}
            value={settings.site_url}
            placeholder="https://share.example.com"
            aria-describedby="email-site-hint"
            onChange={(e) => set({ site_url: e.target.value })}
          />
        </SettingRow>
        <div className="setting-actions">
          <button className="primary" disabled={busy}>
            <Save size={16} />
            {t('email.save')}
          </button>
          {passwordSet && (
            <button
              type="button"
              className="quiet"
              disabled={busy}
              onClick={(e) => void save(e, true)}
            >
              {t('email.clearPassword')}
            </button>
          )}
        </div>
      </form>
      <div className="settings-form email-test">
        <SettingRow id="smtp-test" label={t('email.testTo')} hint={t('email.testHint')}>
          <div className="setting-inline">
            <input
              id="smtp-test"
              aria-describedby="smtp-test-hint"
              type="email"
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              placeholder="name@example.com"
            />
            <button
              className="secondary"
              disabled={busy || !testTo || !settings.host || !settings.from}
              onClick={() => void test()}
            >
              {busy ? <LoaderCircle className="spin" size={16} /> : <Send size={16} />}
              {t('email.test')}
            </button>
          </div>
        </SettingRow>
      </div>
    </section>
  );
}
