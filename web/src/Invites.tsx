import { useEffect, useState } from 'react';
import { Mail, Send, Trash2 } from 'lucide-react';
import { api, formatBytes } from './api';
import { SettingRow } from './SettingRow';
import { t, useLanguage, useMessage, message, formatDate, getLanguage } from './i18n';

interface Invite {
  id: string;
  email: string;
  quota: number;
  created: number;
  expires: number;
}
const GIB = 1024 ** 3;

// Invitations by email: the recipient chooses a username and password
// through the link, even when ordinary registration is closed.
export function InvitesPanel({ defaultQuota, budget }: { defaultQuota: number; budget: number }) {
  useLanguage();
  const [invites, setInvites] = useState<Invite[]>([]);
  const [email, setEmail] = useState('');
  const [quota, setQuota] = useState(defaultQuota / GIB);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [notice, setNotice] = useMessage();
  async function load() {
    try {
      const list = await api<Invite[]>('/api/admin/invites');
      setInvites(Array.isArray(list) ? list : []);
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function run(action: () => Promise<unknown>, done?: ReturnType<typeof message>) {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await action();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel admin-wide">
      <h2>
        <Mail size={18} /> {t('invites.title')}
      </h2>
      <p className="muted panel-intro">{t('invites.intro')}</p>
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
      <form
        className="settings-form"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (!Number.isFinite(quota) || quota < 0 || quota > budget)
            return setError(message('errors.quota'));
          void run(
            async () => {
              await api('/api/admin/invites', 'POST', {
                email,
                quota: Math.round(quota * GIB),
                language: getLanguage(),
              });
              setEmail('');
            },
            message('invites.sent', { email }),
          );
        }}
      >
        <SettingRow id="invite-email" label={t('email.address')}>
          <input
            id="invite-email"
            type="email"
            autoComplete="off"
            value={email}
            placeholder="name@example.com"
            onChange={(e) => setEmail(e.target.value)}
          />
        </SettingRow>
        <SettingRow
          id="invite-quota"
          label={t('admin.storageQuota')}
          hint={t('admin.storageQuotaHint')}
        >
          <input
            id="invite-quota"
            aria-describedby="invite-quota-hint"
            type="number"
            min="0"
            max={budget}
            step="0.01"
            value={quota}
            onChange={(e) => setQuota(e.target.value === '' ? NaN : Number(e.target.value))}
          />
        </SettingRow>
        <div className="setting-actions">
          <button className="primary" disabled={busy || !email.trim()}>
            <Send size={16} />
            {t('invites.send')}
          </button>
        </div>
      </form>
      {invites.length > 0 && (
        <div className="invite-list">
          <h3>{t('invites.pending')}</h3>
          {invites.map((invite) => (
            <div className="invite-row" key={invite.id}>
              <div>
                <strong>{invite.email}</strong>
                <small>
                  {formatBytes(invite.quota)} ·{' '}
                  {t('invites.until', { date: formatDate(invite.expires * 1000) })}
                </small>
              </div>
              <button
                className="icon-button danger"
                disabled={busy}
                aria-label={t('invites.revokeNamed', { email: invite.email })}
                onClick={() => void run(() => api('/api/admin/invites/' + invite.id, 'DELETE', {}))}
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
