import { useEffect, useState } from 'react';
import { copyText } from './clipboard';
import { Copy, Link2, Mail } from 'lucide-react';
import { api, shareUrl, type Capabilities, type Me, type Share } from './api';
import { usePaged, ShowMore } from './Paging';
import LibraryShareDialog from './LibraryShareDialog';
import { EmailShareDialog, NotifyToggle } from './Email';
import { formatDate, formatNumber, t, useLanguage, useMessage } from './i18n';

// Links made from the library: live folders and file selections. They are
// managed on the Shares page, next to the links made from Send.
export default function LibraryLinks({ me, reload }: { me: Me; reload: () => Promise<void> }) {
  useLanguage();
  const [shares, setShares] = useState<Share[]>([]);
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState('');
  const [emailing, setEmailing] = useState<Share | null>(null);
  const [sharing, setSharing] = useState<{ share?: Share } | null>(null);
  const [error, setError] = useMessage();
  const pagedLinks = usePaged(shares);

  async function load() {
    try {
      const [sh, c] = await Promise.all([
        api<Share[]>('/api/shares'),
        api<Capabilities>('/api/capabilities'),
      ]);
      setShares(sh.filter((s) => s.mode));
      setCaps(c);
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
  }, []);

  async function action(fn: () => Promise<unknown>) {
    setBusy(true);
    setError('');
    try {
      await fn();
      await load();
      await reload();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  async function copyLink(share: Share) {
    try {
      await copyText(shareUrl(share));
      setCopied(share.id);
    } catch {
      setCopied('');
    }
  }

  return (
    <>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      <section className="panel">
        <div className="panel-title">
          <span>
            <Link2 size={17} />
            {t('library.links')}
          </span>
          <small>{formatNumber(shares.length)}</small>
        </div>
        {shares.length === 0 ? (
          <p className="library-foot">{t('library.noLinks')}</p>
        ) : (
          pagedLinks.shown.map((s) => (
            <article className="link-row" key={s.id}>
              <div>
                <strong>{s.title}</strong>
                <div className="share-meta">
                  <span className={'badge ' + (s.mode === 'live' ? 'accent' : 'neutral')}>
                    {s.mode === 'live' ? t('library.live') : t('library.selection')}
                  </span>
                  <span>
                    {s.expires === 0 ? t('share.neverExpires') : formatDate(s.expires * 1000)}
                  </span>
                  <span>
                    {s.downloads === 1
                      ? t('share.downloadsOne')
                      : t('share.downloads', { count: formatNumber(s.downloads || 0) })}
                  </span>
                  {!!s.download_limit && (
                    <span>
                      {t('downloads.limitShort', { count: formatNumber(s.download_limit) })}
                    </span>
                  )}
                </div>
              </div>
              <input
                aria-label={t('share.link')}
                readOnly
                value={shareUrl(s)}
                onFocus={(e) => e.currentTarget.select()}
              />
              <div className="row-actions">
                <button
                  className="icon-button"
                  aria-label={copied === s.id ? t('share.copied') : t('share.copy')}
                  onClick={() => void copyLink(s)}
                >
                  <Copy size={16} />
                </button>
                {me.email_enabled && (
                  <button
                    className="icon-button"
                    aria-label={t('email.sendNamed', { title: s.title })}
                    onClick={() => setEmailing(s)}
                  >
                    <Mail size={16} />
                  </button>
                )}
                <NotifyToggle
                  share={s}
                  available={!!me.email_enabled && !!me.user.email}
                  changed={(notify) =>
                    setShares((current) =>
                      current.map((item) => (item.id === s.id ? { ...item, notify } : item)),
                    )
                  }
                />
                <button
                  className="secondary small"
                  disabled={busy || !caps?.share}
                  onClick={() => setSharing({ share: s })}
                >
                  {t('library.manageShare')}
                </button>
                <button
                  className="quiet danger small"
                  disabled={busy}
                  onClick={() => {
                    if (window.confirm(t('library.revokeConfirm')))
                      void action(() => api('/api/shares/' + s.id, 'DELETE', {}));
                  }}
                >
                  {t('library.revoke')}
                </button>
              </div>
            </article>
          ))
        )}
        <ShowMore paged={pagedLinks} />
      </section>
      {sharing && caps && (
        <LibraryShareDialog
          {...sharing}
          daysDefault={me.policy.default_days}
          caps={caps}
          close={() => setSharing(null)}
          changed={() => void load()}
        />
      )}
      {emailing && <EmailShareDialog share={emailing} close={() => setEmailing(null)} />}
    </>
  );
}
