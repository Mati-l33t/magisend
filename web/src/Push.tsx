import { useEffect, useState } from 'react';
import { Bell, Plus, Send, Trash2 } from 'lucide-react';
import { api } from './api';
import { Modal } from './Modal';
import { Select } from './Select';
import { SettingRow } from './SettingRow';
import { formatDate, getLanguage, t, useLanguage, useMessage } from './i18n';

type Kind = 'ntfy' | 'gotify' | 'discord' | 'webhook';
const kinds: Kind[] = ['ntfy', 'gotify', 'discord', 'webhook'];
const events = ['downloaded', 'received', 'expiring', 'closed'] as const;
const placeholders: Record<Kind, string> = {
  ntfy: 'https://ntfy.sh/…',
  gotify: 'https://gotify.example.com',
  discord: 'https://discord.com/api/webhooks/…',
  webhook: 'https://example.com/hook',
};

interface Channel {
  id: string;
  kind: Kind;
  url: string;
  has_token: boolean;
  events: string[];
  created: number;
  last_sent: number;
  last_error: string;
}

// The server sends a short code; the reader gets it in their language.
function reason(code: string) {
  const status = code.match(/^status (\d+)$/)?.[1];
  if (status) return t('push.reason.status', { status });
  if (code === 'local' || code === 'timeout') return t(`push.reason.${code}`);
  return t('push.reason.connect');
}

// Where the person's push notifications go, and which events each gets.
export function PushDialog({ close }: { close: () => void }) {
  useLanguage();
  const [channels, setChannels] = useState<Channel[]>([]);
  const [allowLocal, setAllowLocal] = useState(true);
  const [kind, setKind] = useState<Kind>('ntfy');
  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [chosen, setChosen] = useState<string[]>(['downloaded', 'received', 'expiring', 'closed']);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [note, setNote] = useState('');
  async function load() {
    const result = await api<{ channels: Channel[]; allow_local: boolean }>('/api/account/push');
    setChannels(Array.isArray(result?.channels) ? result.channels : []);
    setAllowLocal(!!result?.allow_local);
  }
  useEffect(() => {
    load().catch((e) => setError(e as Error));
  }, []);
  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError('');
    setNote('');
    try {
      await fn();
    } catch (e) {
      setError(e as Error);
    } finally {
      await load().catch(() => {});
      setBusy(false);
    }
  }
  const tokenLabel =
    kind === 'gotify' ? t('push.tokenGotify') : kind === 'ntfy' ? t('push.tokenOptional') : '';
  return (
    <Modal title={t('push.title')} close={close} className="wide">
      <p className="muted">{t('push.intro')}</p>
      {!allowLocal && <p className="muted">{t('push.localOff')}</p>}
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
      {channels.length === 0 ? (
        <p className="muted">{t('push.none')}</p>
      ) : (
        <ul className="token-list push-list">
          {channels.map((c) => (
            <li key={c.id}>
              <div>
                <strong>{t(`push.kind.${c.kind}`)}</strong>
                <span className="mono push-url">{c.url}</span>
                <small>
                  {c.events.map((e) => t(`push.event.${e}`)).join(' · ')}
                  {c.last_error
                    ? ' — ' + t('push.failed', { reason: reason(c.last_error) })
                    : c.last_sent
                      ? ' — ' + t('push.lastSent', { date: formatDate(c.last_sent * 1000, true) })
                      : ''}
                </small>
              </div>
              <div className="row-actions">
                <button
                  className="quiet small"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      await api('/api/account/push/' + c.id + '/test', 'POST', {});
                      setNote(t('push.testSent'));
                    })
                  }
                >
                  <Send size={15} />
                  {t('push.test')}
                </button>
                <button
                  className="icon-button danger"
                  disabled={busy}
                  aria-label={t('push.remove', { name: t(`push.kind.${c.kind}`) })}
                  onClick={() => void run(() => api('/api/account/push/' + c.id, 'DELETE', {}))}
                >
                  <Trash2 size={16} />
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <form
        className="form-stack push-form"
        onSubmit={(e) => {
          e.preventDefault();
          void run(async () => {
            await api('/api/account/push', 'POST', {
              kind,
              url,
              token,
              events: chosen,
              language: getLanguage(),
            });
            setUrl('');
            setToken('');
          });
        }}
      >
        <div className="field-grid">
          <Select
            label={t('push.kind')}
            value={kind}
            onChange={setKind}
            options={kinds.map((k) => ({ value: k, label: t(`push.kind.${k}`) }))}
          />
          <label>
            {t('push.url')}
            <input
              className="mono"
              value={url}
              maxLength={500}
              placeholder={placeholders[kind]}
              onChange={(e) => setUrl(e.target.value)}
            />
          </label>
        </div>
        <small className="field-hint">{t(`push.urlHint.${kind}`)}</small>
        {tokenLabel && (
          <label>
            {tokenLabel}
            <input
              className="mono"
              value={token}
              maxLength={300}
              autoComplete="off"
              onChange={(e) => setToken(e.target.value)}
            />
          </label>
        )}
        <fieldset className="push-events">
          <legend>{t('push.events')}</legend>
          {events.map((e) => (
            <label className="checkbox" key={e}>
              <input
                type="checkbox"
                checked={chosen.includes(e)}
                onChange={(event) =>
                  setChosen((current) =>
                    event.target.checked ? [...current, e] : current.filter((x) => x !== e),
                  )
                }
              />
              {t(`push.event.${e}`)}
            </label>
          ))}
        </fieldset>
        <div className="dialog-actions">
          <button type="button" className="secondary" onClick={close}>
            {t('common.close')}
          </button>
          <button
            className="primary"
            disabled={busy || !url.trim() || !chosen.length || (kind === 'gotify' && !token.trim())}
          >
            <Plus size={16} />
            {t('push.add')}
          </button>
        </div>
      </form>
    </Modal>
  );
}

// Administration: whether accounts may send to addresses on the local network.
export function PushAdminPanel() {
  useLanguage();
  const [allow, setAllow] = useState<boolean | null>(null);
  const [error, setError] = useMessage();
  useEffect(() => {
    api<{ allow_local: boolean }>('/api/admin/push')
      .then((r) => setAllow(!!r?.allow_local))
      .catch((e) => setError(e as Error));
  }, []);
  return (
    <section className="panel admin-wide">
      <h2>
        <Bell size={18} /> {t('admin.push')}
      </h2>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      <SettingRow id="push-local" label={t('admin.pushLocal')} hint={t('admin.pushLocalHint')}>
        <input
          id="push-local"
          type="checkbox"
          className="switch"
          aria-describedby="push-local-hint"
          checked={!!allow}
          disabled={allow === null}
          onChange={(e) => {
            const next = e.target.checked;
            setAllow(next);
            api<{ allow_local: boolean }>('/api/admin/push', 'PUT', { allow_local: next })
              .then((r) => setAllow(r.allow_local))
              .catch((err) => {
                setAllow(!next);
                setError(err as Error);
              });
          }}
        />
      </SettingRow>
    </section>
  );
}
