import { useEffect, useState } from 'react';
import { copyText } from './clipboard';
import { Copy, Download, KeyRound, Plus, Trash2 } from 'lucide-react';
import { api } from './api';
import { Modal } from './Modal';
import { t, useLanguage, useMessage, formatDate } from './i18n';

interface Token {
  id: string;
  name: string;
  created: number;
  last_used: number;
}

// Personal upload keys for ShareX, scripts and curl. A token is shown once.
export function ApiTokensDialog({ close }: { close: () => void }) {
  useLanguage();
  const [tokens, setTokens] = useState<Token[]>([]);
  const [name, setName] = useState('');
  const [fresh, setFresh] = useState<{ token: string; name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const endpoint = location.origin + '/api/v1/upload';
  async function load() {
    try {
      const list = await api<Token[]>('/api/account/tokens');
      setTokens(Array.isArray(list) ? list : []);
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function create(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const result = await api<{ token: string; info: Token }>('/api/account/tokens', 'POST', {
        name,
      });
      setFresh({ token: result.token, name: result.info.name });
      setName('');
      await load();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  async function revoke(token: Token) {
    if (!window.confirm(t('tokens.revokeConfirm', { name: token.name }))) return;
    try {
      await api('/api/account/tokens/' + token.id, 'DELETE', {});
      await load();
    } catch (e) {
      setError(e as Error);
    }
  }
  function sharex(token: string) {
    const config = {
      Version: '17.0.0',
      Name: 'MagiSend (' + location.host + ')',
      DestinationType: 'ImageUploader, TextUploader, FileUploader',
      RequestMethod: 'POST',
      RequestURL: endpoint,
      Headers: { Authorization: 'Bearer ' + token },
      Body: 'MultipartFormData',
      FileFormName: 'file',
      URL: '{json:url}',
    };
    const link = document.createElement('a');
    link.href = URL.createObjectURL(
      new Blob([JSON.stringify(config, null, 2)], { type: 'application/json' }),
    );
    link.download = 'MagiSend.sxcu';
    link.click();
    URL.revokeObjectURL(link.href);
  }
  const curl = (token: string) =>
    `curl -H "Authorization: Bearer ${token}" -F "file=@photo.jpg" "${endpoint}?days=7"`;
  return (
    <Modal title={t('tokens.title')} close={close} className="wide">
      <p className="muted">{t('tokens.intro')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {fresh && (
        <div className="token-fresh">
          <p className="success-note" role="status">
            {t('tokens.created', { name: fresh.name })}
          </p>
          <label>
            {t('tokens.token')}
            <input
              className="mono"
              readOnly
              value={fresh.token}
              onFocus={(e) => e.currentTarget.select()}
            />
          </label>
          <label>
            {t('tokens.curl')}
            <textarea className="mono" readOnly rows={2} value={curl(fresh.token)} />
          </label>
          <div className="dialog-actions">
            <button
              className="secondary"
              onClick={() => void copyText(fresh.token).catch(() => {})}
            >
              <Copy size={16} />
              {t('tokens.copy')}
            </button>
            <button className="primary" onClick={() => sharex(fresh.token)}>
              <Download size={16} />
              {t('tokens.sharex')}
            </button>
          </div>
        </div>
      )}
      <form className="token-create" onSubmit={(e) => void create(e)}>
        <label>
          {t('tokens.name')}
          <input
            value={name}
            maxLength={60}
            placeholder={t('tokens.namePlaceholder')}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <button className="secondary" disabled={busy || !name.trim()}>
          <Plus size={16} />
          {t('tokens.create')}
        </button>
      </form>
      {tokens.length > 0 && (
        <ul className="token-list">
          {tokens.map((token) => (
            <li key={token.id}>
              <KeyRound size={16} aria-hidden="true" />
              <div>
                <strong>{token.name}</strong>
                <small>
                  {t('tokens.since', { date: formatDate(token.created * 1000) })} ·{' '}
                  {token.last_used
                    ? t('tokens.used', { date: formatDate(token.last_used * 1000, true) })
                    : t('tokens.unused')}
                </small>
              </div>
              <button
                className="icon-button danger"
                aria-label={t('tokens.revokeNamed', { name: token.name })}
                onClick={() => void revoke(token)}
              >
                <Trash2 size={16} />
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="muted token-help">{t('tokens.params')}</p>
    </Modal>
  );
}
