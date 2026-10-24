import { useState } from 'react';
import { LoaderCircle, Save } from 'lucide-react';
import { api, type Share } from './api';
import { Modal } from './Modal';
import { Select } from './Select';
import { DownloadLimitSelect, limitHint, validityOptions } from './Downloads';
import { t, useLanguage, useMessage, formatDate } from './i18n';

const UNCHANGED = -1;

// Changes a sent share after it was created. Only the fields that were
// changed are sent; a new validity period counts from today.
export function EditShareDialog({
  share,
  close,
  saved,
}: {
  share: Share;
  close: () => void;
  saved: (share: Share) => void;
}) {
  useLanguage();
  const [title, setTitle] = useState(share.title);
  const [description, setDescription] = useState(share.description);
  const [slug, setSlug] = useState(share.slug);
  const [days, setDays] = useState(UNCHANGED);
  const [limit, setLimit] = useState(share.download_limit || 0);
  const [password, setPassword] = useState('');
  const [removePassword, setRemovePassword] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();

  async function save(event: React.FormEvent) {
    event.preventDefault();
    const changes: Record<string, unknown> = {};
    if (title.trim() !== share.title) changes.title = title;
    if (description.trim() !== share.description) changes.description = description;
    if (slug !== share.slug) changes.slug = slug;
    if (days !== UNCHANGED) Object.assign(changes, { days, permanent: days === 0 });
    if (limit !== (share.download_limit || 0)) changes.download_limit = limit;
    if (removePassword) changes.password = '';
    else if (password) changes.password = password;
    if (!Object.keys(changes).length) return close();
    setBusy(true);
    setError('');
    try {
      saved(await api<Share>('/api/shares/' + share.id, 'PATCH', changes));
      close();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  const current =
    share.expires === 0
      ? t('share.neverExpires')
      : t('share.expires', { date: formatDate(share.expires * 1000) });
  return (
    <Modal title={t('edit.title', { title: share.title })} close={close}>
      <form className="form-stack" onSubmit={(e) => void save(e)} noValidate>
        <label>
          {t('share.title')}
          <input value={title} maxLength={160} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label>
          {t('share.description')} <span className="optional">{t('share.optional')}</span>
          <textarea
            rows={3}
            maxLength={1000}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
        <div className="field-with-hint">
          <label>
            {t('share.customLink')} <span className="optional">{t('share.optional')}</span>
            <input
              className="mono"
              value={slug}
              maxLength={48}
              aria-describedby="edit-slug-hint"
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
            />
          </label>
          <small id="edit-slug-hint" className="field-hint">
            {t('edit.slugHint')}
          </small>
        </div>
        <div className="field-grid">
          <Select
            label={t('share.validity')}
            value={days}
            onChange={setDays}
            hint={days === UNCHANGED ? undefined : t('edit.fromToday')}
            options={[
              { value: UNCHANGED, label: t('edit.unchanged', { current }) },
              ...validityOptions([]),
            ]}
          />
          <DownloadLimitSelect
            value={limit}
            onChange={setLimit}
            hint={limit > 0 ? limitHint(limit) : ''}
          />
        </div>
        {share.protected ? (
          <>
            <label>
              {t('edit.newPassword')} <span className="optional">{t('share.optional')}</span>
              <input
                type="password"
                autoComplete="new-password"
                maxLength={72}
                value={password}
                disabled={removePassword}
                placeholder={t('edit.keepPassword')}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            <label className="checkbox">
              <input
                type="checkbox"
                checked={removePassword}
                onChange={(e) => setRemovePassword(e.target.checked)}
              />
              {t('edit.removePassword')}
            </label>
          </>
        ) : (
          <label>
            {t('auth.password')} <span className="optional">{t('share.optional')}</span>
            <input
              type="password"
              autoComplete="new-password"
              maxLength={72}
              value={password}
              placeholder={t('share.passwordPlaceholder')}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
        )}
        {password && !removePassword && (
          <small className="field-hint">{t('edit.passwordNote')}</small>
        )}
        {error && (
          <div role="alert" className="error">
            {error}
          </div>
        )}
        <div className="dialog-actions">
          <button type="button" className="secondary" onClick={close}>
            {t('common.cancel')}
          </button>
          <button className="primary" disabled={busy || !title.trim()}>
            {busy ? <LoaderCircle className="spin" size={16} /> : <Save size={16} />}
            {t('common.save')}
          </button>
        </div>
      </form>
    </Modal>
  );
}

// Opened from the reminder email: keeps a link open longer in one step.
export function ExtendShareDialog({
  share,
  close,
  saved,
}: {
  share: Share;
  close: () => void;
  saved: (share: Share) => void;
}) {
  useLanguage();
  const [days, setDays] = useState(7);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  async function extend() {
    setBusy(true);
    setError('');
    try {
      saved(
        await api<Share>(
          '/api/shares/' + share.id,
          'PATCH',
          days === 0 ? { permanent: true } : { days },
        ),
      );
      close();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t('share.extendTitle', { title: share.title })} close={() => !busy && close()}>
      <p className="muted">
        {share.expires
          ? t('share.extendNow', { date: formatDate(share.expires * 1000, true) })
          : t('share.neverExpires')}
      </p>
      <Select
        label={t('share.extendFor')}
        value={days}
        onChange={setDays}
        options={validityOptions([7])}
      />
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      <div className="dialog-actions">
        <button className="secondary" disabled={busy} onClick={close}>
          {t('common.cancel')}
        </button>
        <button className="primary" disabled={busy} onClick={() => void extend()}>
          {busy ? <LoaderCircle className="spin" size={16} /> : <Save size={16} />}
          {t('share.extend')}
        </button>
      </div>
    </Modal>
  );
}
