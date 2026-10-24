import { useEffect, useState } from 'react';
import { Select } from './Select';
import { api, shareUrl, type Share, type Capabilities } from './api';
import { Modal } from './Modal';
import { DownloadLimitSelect, limitHint, validityOptions } from './Downloads';
import { t, useLanguage, useMessage } from './i18n';
interface Permissions {
  recipients: string[];
  edit: boolean;
  add: boolean;
  delete: boolean;
}
const allowLabels = {
  edit: 'library.allow.edit',
  add: 'library.allow.add',
  delete: 'library.allow.delete',
} as const;
export default function LibraryShareDialog({
  share,
  folder,
  files,
  daysDefault,
  caps,
  close,
  changed,
}: {
  share?: Share;
  folder?: string;
  files?: string[];
  daysDefault: number;
  caps: Capabilities;
  close: () => void;
  changed: () => void;
}) {
  useLanguage();
  const [title, setTitle] = useState(share?.title || '');
  const [days, setDays] = useState(
    share
      ? share.expires === 0
        ? 0
        : Math.max(1, Math.ceil((share.expires * 1000 - Date.now()) / 86400000))
      : daysDefault,
  );
  const [password, setPassword] = useState('');
  const [limit, setLimit] = useState(share?.download_limit || 0);
  const [clearPassword, setClearPassword] = useState(false);
  const [permissions, setPermissions] = useState<Permissions>({
    recipients: [],
    edit: false,
    add: false,
    delete: false,
  });
  const [recipients, setRecipients] = useState('');
  const [busy, setBusy] = useState(!!share);
  const [error, setError] = useMessage();
  const [result, setResult] = useState<Share | null>(null);
  const live = share?.mode === 'live' || !!folder;
  useEffect(() => {
    if (!share) return;
    api<Permissions>('/api/library/shares/' + share.id + '/permissions')
      .then((p) => {
        setPermissions(p);
        setRecipients(p.recipients.join(', '));
      })
      .catch((e) => setError(e as Error))
      .finally(() => setBusy(false));
  }, [share?.id]);
  async function save() {
    setBusy(true);
    setError('');
    try {
      const names = recipients.split(/[\s,]+/).filter(Boolean);
      const next = { ...permissions, recipients: names };
      const settings = {
        title: title || t('library.sharedFiles'),
        days,
        permanent: days === 0,
        download_limit: limit,
        ...(password || clearPassword ? { password } : {}),
      };
      let active = share;
      if (!active) {
        active = await api<Share>('/api/library/shares', 'POST', {
          ...settings,
          ...next,
          ...(live ? { folder_id: folder } : { files }),
        });
      } else await api('/api/library/shares/' + active.id, 'PATCH', { ...settings, ...next });
      setResult(active);
      changed();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={share ? t('library.manageShare') : t('library.share')} close={close}>
      <p>{live ? t('library.liveHint') : t('library.selectionHint')}</p>
      <p className="muted">{t('library.readDefault')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {result ? (
        <>
          <label>
            {t('share.link')}
            <input readOnly value={shareUrl(result)} onFocus={(e) => e.currentTarget.select()} />
          </label>
          <a className="secondary" href={shareUrl(result)} target="_blank" rel="noreferrer">
            {t('library.openLink')}
          </a>
        </>
      ) : (
        <>
          <label>
            {t('share.title')}
            <input maxLength={160} value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <Select
            label={t('share.validity')}
            value={days}
            onChange={setDays}
            options={validityOptions([daysDefault, days])}
          />
          <label>
            {t('auth.password')}
            <input
              type="password"
              autoComplete="new-password"
              maxLength={72}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <DownloadLimitSelect
            value={limit}
            onChange={setLimit}
            disabled={busy || permissions.edit || permissions.add || permissions.delete}
            hint={limit > 0 ? limitHint(limit, true) : ''}
          />
          {share?.protected && (
            <label className="checkbox">
              <input
                type="checkbox"
                checked={clearPassword}
                onChange={(e) => setClearPassword(e.target.checked)}
              />
              {t('library.clearPassword')}
            </label>
          )}
          <div className="field-with-hint">
            <label>
              {t('library.recipients')}
              <input
                value={recipients}
                aria-describedby="library-recipient-hint"
                onChange={(e) => setRecipients(e.target.value)}
                disabled={!caps.grant_edit}
              />
            </label>
            <small id="library-recipient-hint" className="field-hint">
              {t('library.recipientHint')}
            </small>
          </div>
          <div className="dialog-checks">
            {(['edit', 'add', 'delete'] as const).map((key) => (
              <label className="checkbox" key={key}>
                <input
                  type="checkbox"
                  checked={permissions[key]}
                  disabled={
                    busy ||
                    (!caps.grant_edit && !permissions[key]) ||
                    (key === 'add' && !live) ||
                    (limit > 0 && !permissions[key])
                  }
                  onChange={(e) => setPermissions({ ...permissions, [key]: e.target.checked })}
                />
                {t(allowLabels[key])}
              </label>
            ))}
          </div>
          {(permissions.delete || limit > 0 || !caps.grant_edit) && (
            <div className="dialog-notes">
              {permissions.delete && <p>{t('library.deleteWarning')}</p>}
              {limit > 0 && <p>{t('downloads.noCollaboration')}</p>}
              {!caps.grant_edit && <p>{t('library.grantDisabled')}</p>}
            </div>
          )}
          <button className="primary full" disabled={busy} onClick={() => void save()}>
            {t('common.save')}
          </button>
        </>
      )}
    </Modal>
  );
}
