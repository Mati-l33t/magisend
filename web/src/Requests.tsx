import CopyButton from './CopyButton';
import { useEffect, useRef, useState } from 'react';
import { usePaged, ShowMore } from './Paging';
import { Inbox, Folder as FolderIcon, LoaderCircle, Pencil, Plus, Save } from 'lucide-react';
import { api, formatBytes, type Folder, type Me } from './api';
import { folderLabel, folderOptions } from './folderTree';
import { Modal } from './Modal';
import MoreMenu from './MoreMenu';
import { Select } from './Select';
import { validityOptions } from './Downloads';
import { t, useLanguage, useMessage, formatDate, formatNumber, getLanguage } from './i18n';

export interface UploadRequest {
  id: string;
  title: string;
  message: string;
  folder_id: string;
  expires: number;
  created: number;
  closed: boolean;
  notify: boolean;
  protected: boolean;
  uploads: number;
  files: number;
  bytes: number;
  last_upload: number;
}

export const requestUrl = (q: UploadRequest) => location.origin + '/r/' + q.id;
const UNCHANGED = -1;

// The owner's list of upload links, shown in the library.
export function RequestsPanel({
  me,
  folders,
  received,
}: {
  me: Me;
  folders: Folder[];
  received: () => void;
}) {
  useLanguage();
  const [requests, setRequests] = useState<UploadRequest[]>([]);
  const pagedRequests = usePaged(requests);
  const [editing, setEditing] = useState<UploadRequest | 'new' | null>(null);
  const [error, setError] = useMessage();
  const seen = useRef(-1);
  async function load() {
    try {
      const answer = await api<UploadRequest[]>('/api/requests');
      const list = Array.isArray(answer) ? answer : [];
      setRequests(list);
      // New files arrived: let the library show them.
      const count = list.reduce((n, q) => n + q.files, 0);
      if (seen.current >= 0 && count !== seen.current) received();
      seen.current = count;
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
    const refresh = () => void load();
    addEventListener('focus', refresh);
    return () => removeEventListener('focus', refresh);
  }, []);
  async function update(q: UploadRequest, changes: Partial<UploadRequest>) {
    try {
      await api('/api/requests/' + q.id, 'PATCH', changes);
      await load();
    } catch (e) {
      setError(e as Error);
    }
  }
  const folderName = (id: string) => (id && folderLabel(folders, id)) || t('library.unfiled');
  const expired = (q: UploadRequest) => q.expires !== 0 && q.expires * 1000 <= Date.now();
  return (
    <section className="panel">
      <div className="panel-title">
        <span>
          <Inbox size={17} />
          {t('requests.title')}
        </span>
        <button className="secondary small" onClick={() => setEditing('new')}>
          <Plus size={14} />
          {t('requests.new')}
        </button>
      </div>
      {error && (
        <div role="alert" className="error panel-error">
          {error}
        </div>
      )}
      {requests.length === 0 ? (
        <p className="library-foot">{t('requests.empty')}</p>
      ) : (
        pagedRequests.shown.map((q) => (
          <article className="link-row" key={q.id}>
            <div>
              <strong>{q.title}</strong>
              <div className="share-meta">
                <span className={'badge ' + (q.closed || expired(q) ? 'neutral' : 'accent')}>
                  {q.closed
                    ? t('requests.closed')
                    : expired(q)
                      ? t('share.expired')
                      : t('requests.open')}
                </span>
                <span>
                  <FolderIcon size={12} /> {folderName(q.folder_id)}
                </span>
                <span>
                  {t('requests.received', {
                    files: formatNumber(q.files),
                    size: formatBytes(q.bytes),
                  })}
                </span>
                {!!q.last_upload && (
                  <span>{t('requests.last', { date: formatDate(q.last_upload * 1000) })}</span>
                )}
              </div>
            </div>
            <input
              aria-label={t('requests.link')}
              readOnly
              value={requestUrl(q)}
              onFocus={(e) => e.currentTarget.select()}
            />
            <div className="row-actions">
              <CopyButton
                text={requestUrl(q)}
                label={t('requests.copyNamed', { title: q.title })}
              />
              <button
                className="icon-button"
                aria-label={t('requests.editNamed', { title: q.title })}
                onClick={() => setEditing(q)}
              >
                <Pencil size={16} />
              </button>
              <MoreMenu
                label={t('requests.moreNamed', { title: q.title })}
                items={[
                  {
                    label: q.closed ? t('requests.reopen') : t('requests.close'),
                    onSelect: () => void update(q, { closed: !q.closed }),
                  },
                  {
                    label: t('requests.delete'),
                    danger: true,
                    onSelect: () => {
                      if (window.confirm(t('requests.deleteConfirm')))
                        void api('/api/requests/' + q.id, 'DELETE', {}).then(load, (e) =>
                          setError(e as Error),
                        );
                    },
                  },
                ]}
              />
            </div>
          </article>
        ))
      )}
      <ShowMore paged={pagedRequests} />
      {editing && (
        <RequestDialog
          me={me}
          folders={folders}
          request={editing === 'new' ? undefined : editing}
          close={() => setEditing(null)}
          saved={() => void load()}
        />
      )}
    </section>
  );
}

function RequestDialog({
  me,
  folders,
  request,
  close,
  saved,
}: {
  me: Me;
  folders: Folder[];
  request?: UploadRequest;
  close: () => void;
  saved: () => void;
}) {
  useLanguage();
  const [title, setTitle] = useState(request?.title || '');
  const [note, setNote] = useState(request?.message || '');
  const [folder, setFolder] = useState(request?.folder_id || '');
  const [days, setDays] = useState(request ? UNCHANGED : me.policy.default_days);
  const [password, setPassword] = useState('');
  const [removePassword, setRemovePassword] = useState(false);
  const [notify, setNotify] = useState(request?.notify || false);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<UploadRequest | null>(null);
  const [error, setError] = useMessage();
  const canNotify = !!me.email_enabled && !!me.user.email;
  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    const body: Record<string, unknown> = {
      title,
      message: note,
      folder_id: folder,
      notify,
      language: getLanguage(),
    };
    if (days !== UNCHANGED) Object.assign(body, { days, permanent: days === 0 });
    if (removePassword) body.password = '';
    else if (password) body.password = password;
    try {
      const result = await api<UploadRequest>(
        request ? '/api/requests/' + request.id : '/api/requests',
        request ? 'PATCH' : 'POST',
        body,
      );
      saved();
      if (request) close();
      else setCreated(result);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  if (created)
    return (
      <Modal title={t('requests.ready')} close={close}>
        <p>{t('requests.readyHint')}</p>
        <label>
          {t('requests.link')}
          <input readOnly value={requestUrl(created)} onFocus={(e) => e.currentTarget.select()} />
        </label>
        <div className="dialog-actions">
          <CopyButton className="secondary" text={requestUrl(created)} label={t('share.copy')}>
            {t('share.copy')}
          </CopyButton>
          <button className="primary" onClick={close}>
            {t('common.close')}
          </button>
        </div>
      </Modal>
    );
  const current = request
    ? request.expires === 0
      ? t('share.neverExpires')
      : t('share.expires', { date: formatDate(request.expires * 1000) })
    : '';
  return (
    <Modal title={request ? t('requests.edit') : t('requests.new')} close={close}>
      <form className="form-stack" onSubmit={(e) => void save(e)} noValidate>
        {!request && <p className="muted">{t('requests.intro')}</p>}
        <label>
          {t('requests.name')}
          <input
            value={title}
            maxLength={160}
            placeholder={t('requests.namePlaceholder')}
            onChange={(e) => setTitle(e.target.value)}
          />
        </label>
        <label>
          {t('requests.message')} <span className="optional">{t('share.optional')}</span>
          <textarea
            rows={3}
            maxLength={1000}
            value={note}
            placeholder={t('requests.messagePlaceholder')}
            onChange={(e) => setNote(e.target.value)}
          />
        </label>
        <div className="field-grid">
          <Select
            label={t('requests.folder')}
            value={folder}
            onChange={setFolder}
            options={[
              { value: '', label: t('library.unfiled'), icon: <Inbox size={16} /> },
              ...folderOptions(folders).map(({ folder: f, label }) => ({
                value: f.id,
                label,
                icon: <FolderIcon size={16} />,
              })),
            ]}
          />
          <Select
            label={t('requests.openFor')}
            value={days}
            onChange={setDays}
            hint={request && days === UNCHANGED ? current : undefined}
            options={[
              ...(request ? [{ value: UNCHANGED, label: t('edit.unchanged', { current }) }] : []),
              ...validityOptions([me.policy.default_days]),
            ]}
          />
        </div>
        <label>
          {request?.protected ? t('edit.newPassword') : t('auth.password')}{' '}
          <span className="optional">{t('share.optional')}</span>
          <input
            type="password"
            autoComplete="new-password"
            maxLength={72}
            value={password}
            disabled={removePassword}
            placeholder={
              request?.protected ? t('edit.keepPassword') : t('share.passwordPlaceholder')
            }
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {request?.protected && (
          <label className="checkbox">
            <input
              type="checkbox"
              checked={removePassword}
              onChange={(e) => setRemovePassword(e.target.checked)}
            />
            {t('edit.removePassword')}
          </label>
        )}
        <label className="checkbox" title={canNotify ? undefined : t('email.notifyUnavailable')}>
          <input
            type="checkbox"
            checked={notify}
            disabled={!canNotify && !notify}
            onChange={(e) => setNotify(e.target.checked)}
          />
          {t('requests.notify')}
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
          <button className="primary" disabled={busy || !title.trim()}>
            {busy ? <LoaderCircle className="spin" size={16} /> : <Save size={16} />}
            {request ? t('common.save') : t('requests.create')}
          </button>
        </div>
      </form>
    </Modal>
  );
}

// The Upload requests page: its own place in the menu, with the folders the
// files can be filed into.
export function RequestsPage({ me }: { me: Me }) {
  useLanguage();
  const [folders, setFolders] = useState<Folder[]>([]);
  useEffect(() => {
    api<Folder[]>('/api/folders')
      .then((list) => setFolders(Array.isArray(list) ? list : []))
      .catch(() => setFolders([]));
  }, []);
  return (
    <>
      <header className="page-heading">
        <div>
          <h1>{t('requests.title')}</h1>
          <p>{t('requests.pageDescription')}</p>
        </div>
      </header>
      <RequestsPanel me={me} folders={folders} received={() => {}} />
    </>
  );
}
