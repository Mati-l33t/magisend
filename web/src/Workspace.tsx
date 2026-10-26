import { useEffect, useState, useRef } from 'react';
import { copyText } from './clipboard';
import { usePaged, ShowMore } from './Paging';
import { Select } from './Select';
import {
  Upload,
  ArrowUpRight,
  Link,
  FolderOpen,
  Settings,
  Clock,
  Lock,
  Trash2,
  X,
  Pause,
  Play,
  Copy,
  Check,
  Search,
  QrCode,
  Mail,
  Download,
  LoaderCircle,
  HardDrive,
  ShieldCheck,
  Pencil,
  Inbox,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-react';
import QRCode from 'qrcode';
import { api, formatBytes, shareUrl, type Me, type Share, type SharedFile } from './api';
import {
  fingerprint,
  readIdentity,
  saveIdentity,
  removeIdentity,
  sameIdentity,
  type UploadIdentity,
} from './uploadIdentity';
import Admin from './Admin';
import { Modal } from './Modal';
import { useUpdateInfo, UpdateBanner } from './Updates';
import { EmailShareDialog, NotifyToggle } from './Email';
import { EditShareDialog, ExtendShareDialog } from './ShareEdit';
import { uploadAll, guardUnload, type UploadJob, type UploadStats } from './uploader';
import { UploadSpeed } from './UploadSpeed';
import { chosenFolder, droppedEntries } from './folderFiles';
import { brandName } from './branding';
import { DownloadLimitSelect, FileDownloads, limitHint, validityOptions } from './Downloads';
import Library from './Library';
import LibraryLinks from './LibraryLinks';
import { RequestsPage } from './Requests';
import FilePreview from './FilePreview';
import FileThumb, { rasterName } from './FileThumb';
import { useFollow } from './follow';
import { thumbsFromUpload } from './thumbMaker';
import LocalThumb from './LocalThumb';
import SendLayout from './SendLayouts';
import { KeepFolderSelect, useKeepFolders } from './KeepFiles';
import { useLook } from './look';
import { SearchPalette, openSearchEvent } from './SearchPalette';
import {
  t,
  useLanguage,
  formatDate,
  formatNumber,
  useMessage,
  message,
  LocalizedError,
  getLanguage,
} from './i18n';
export { Modal };
type Tab = 'send' | 'shares' | 'requests' | 'admin' | 'library';
const tabPaths: Record<Tab, string> = {
  send: '/send',
  library: '/library',
  shares: '/shares',
  requests: '/requests',
  admin: '/admin',
};
const tabTitles = {
  send: 'workspace.send',
  library: 'library.title',
  shares: 'workspace.shares',
  requests: 'requests.title',
  admin: 'workspace.admin',
} as const;
// Share of the quota in use, as in the storage card: no quota counts as
// full, and a little use never shows as 0%.
function storageShare(used: number, quota: number) {
  const share = quota === 0 ? 1 : Math.min(1, used / quota);
  if (used > 0 && share < 0.01) return '<' + formatNumber(0.01, { style: 'percent' });
  return formatNumber(share, { style: 'percent' });
}
function tabFromPath(): Tab {
  const segment = location.pathname.split('/')[1];
  return (Object.keys(tabPaths) as Tab[]).find((tab) => tab === segment) || 'send';
}

export default function Workspace({ me, reload }: { me: Me; reload: () => Promise<void> }) {
  useLanguage();
  const look = useLook();
  const [railOpen, setRailOpen] = useState(() => {
    try {
      return localStorage.getItem('magisend-studio-rail') === 'open';
    } catch {
      return false;
    }
  });
  function toggleRail() {
    const next = !railOpen;
    setRailOpen(next);
    try {
      if (next) localStorage.setItem('magisend-studio-rail', 'open');
      else localStorage.removeItem('magisend-studio-rail');
    } catch {}
  }
  const [tab, showTab] = useState<Tab>(tabFromPath);
  const [sharesView, setSharesView] = useState<'sent' | 'library'>('sent');
  // Where "Back to workspace" in Administration returns to.
  const lastTab = useRef<Tab>('send');
  if (tab !== 'admin') lastTab.current = tab;
  // Every view has its own address, so refresh, bookmarks and Back work.
  function setTab(next: Tab) {
    // A just-made link belongs to Send; leaving the page closes it.
    if (fresh) {
      setResult(null);
      setFresh(false);
    }
    const path = tabPaths[next];
    if (location.pathname !== path && !location.pathname.startsWith(path + '/'))
      history.pushState(null, '', path);
    showTab(next);
  }
  useEffect(() => {
    if (location.pathname === '/') history.replaceState(null, '', tabPaths.send + location.search);
    const back = () => showTab(tabFromPath());
    addEventListener('popstate', back);
    return () => removeEventListener('popstate', back);
  }, []);
  // Search opens with ⌘K or Ctrl K, or from the button in the top bar.
  const [searching, setSearching] = useState(false);
  useEffect(() => {
    const open = () => setSearching(true);
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setSearching((current) => current || !document.querySelector('dialog[open]'));
      }
    };
    addEventListener(openSearchEvent, open);
    addEventListener('keydown', key);
    return () => {
      removeEventListener(openSearchEvent, open);
      removeEventListener('keydown', key);
    };
  }, []);
  function openPath(path: string) {
    if (fresh) {
      setResult(null);
      setFresh(false);
    }
    history.pushState(null, '', path);
    dispatchEvent(new PopStateEvent('popstate'));
  }
  useEffect(() => {
    if (tab === 'admin' && me.user.role !== 'admin') setTab('send');
    document.title = t(tabTitles[tab]) + ' · ' + brandName();
  }, [tab, me.user.role, getLanguage()]);
  const [shares, setShares] = useState<Share[]>([]);
  const [files, setFiles] = useState<File[]>([]);
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [slug, setSlug] = useState('');
  const [days, setDays] = useState(me.policy.default_days);
  const [limit, setLimit] = useState(0);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [drag, setDrag] = useState(false);
  const [error, setError] = useMessage();
  const [progress, setProgress] = useState(0);
  const [stats, setStats] = useState<UploadStats | null>(null);
  const [draft, setDraft] = useState<Share | null>(null);
  const [result, setResult] = useState<Share | null>(null);
  const [search, setSearch] = useState('');
  const [remove, setRemove] = useState<Share | null>(null);
  const [qr, setQr] = useState(false);
  // Set when result is a link just made on Send (not one opened from My shares).
  const [fresh, setFresh] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const folderPicker = useRef<HTMLInputElement>(null);
  // Not in React's typings: lets the picker choose a whole folder.
  useEffect(() => folderPicker.current?.setAttribute('webkitdirectory', ''), []);
  const picker = useRef<HTMLInputElement>(null);
  const identities = useRef(new Map<string, UploadIdentity[]>());
  const [emailing, setEmailing] = useState<Share | null>(null);
  const [editing, setEditing] = useState<Share | null>(null);
  // The reminder email opens /shares?extend=<id>.
  const [extending, setExtending] = useState<Share | null>(null);
  const extendId = useRef(new URLSearchParams(location.search).get('extend'));
  const [notifyNew, setNotifyNew] = useState(false);
  // Files sent here stay in the library when the link expires, unless turned off.
  const [keep, setKeep] = useState(true);
  // Photos lose where they were taken, unless turned off.
  const [stripLocation, setStripLocation] = useState(true);
  const [keepFolder, setKeepFolder] = useState('');
  const keepFolders = useKeepFolders(tab === 'send');
  const canEmail = !!me.email_enabled;
  const canNotify = canEmail && !!me.user.email;
  const [updateInfo] = useUpdateInfo(me.user.role === 'admin');
  const [preview, setPreview] = useState<{ shareId: string; file: SharedFile } | null>(null);
  const total = files.reduce((n, f) => n + f.size, 0);
  // In the background (following scans), a failure is not shown: the next
  // turn tries again.
  async function list(background = false) {
    try {
      const all = await api<Share[]>('/api/shares');
      setShares(all);
      // The link just made follows too, for its thumbnails.
      setResult((current) => (current && all.find((s) => s.id === current.id)) || current);
      const wanted = extendId.current;
      if (wanted) {
        extendId.current = null;
        history.replaceState(null, '', location.pathname);
        const found = all.find((s) => s.id === wanted);
        if (found) setExtending(found);
        else setError(message('share.extendGone'));
      }
    } catch (e) {
      if (!background) setError(e as Error);
    }
  }
  useEffect(() => {
    void list();
    return () => controller.current?.abort();
  }, []);
  // Download counts change while the page is open: refresh My shares when it
  // is shown and whenever the window gets focus again.
  useEffect(() => {
    if (tab !== 'shares') return;
    void list();
    const refresh = () => void list();
    addEventListener('focus', refresh);
    return () => removeEventListener('focus', refresh);
  }, [tab]);
  // Files waiting for their virus scan get their thumbnails once it is done.
  const waitingIds = [...shares, ...(result ? [result] : [])]
    .flatMap((s) => s.files)
    .filter((f) => f.scan === 'pending')
    .map((f) => f.id)
    .join(',');
  useFollow(!!waitingIds && (tab === 'shares' || !!result), () => list(true), waitingIds);
  function select(incoming: File[]) {
    if (busy) return;
    setError('');
    const next = draft ? [] : [...files];
    for (const f of incoming) {
      if (next.some((n) => n.name === f.name)) {
        setError(message('errors.duplicateFiles', { name: f.name }));
        return;
      }
      next.push(f);
    }
    if (next.length > 100) {
      setError(message('errors.fileCount'));
      return;
    }
    if (
      next.some((f) => f.size > me.policy.max_file) ||
      next.reduce((n, f) => n + f.size, 0) > me.policy.max_share
    ) {
      setError(message('errors.fileLimits'));
      return;
    }
    if (
      !draft &&
      me.user.quota > 0 &&
      next.reduce((n, f) => n + f.size, 0) > me.user.quota - me.user.used
    ) {
      setError(message('errors.storageFull'));
      return;
    }
    setFiles(next);
  }
  // Files that arrived from the share sheet, waiting to be confirmed.
  const [sharedIn, setSharedIn] = useState<File[]>([]);
  // Files shared to the installed app from the system share sheet are parked
  // by the service worker; pick them up once, then forget them.
  useEffect(() => {
    if (!new URLSearchParams(location.search).has('shared') || !('caches' in window)) return;
    void (async () => {
      const cache = await caches.open('magisend-shared');
      const incoming: File[] = [];
      for (const request of await cache.keys()) {
        const response = await cache.match(request);
        if (!response) continue;
        const blob = await response.blob();
        const name = decodeURIComponent(response.headers.get('X-File-Name') || 'shared-file');
        incoming.push(new File([blob], name, { type: blob.type }));
      }
      await caches.delete('magisend-shared');
      history.replaceState(null, '', tabPaths.send);
      // They are only added once confirmed: any website could send files here.
      if (incoming.length) {
        setTab('send');
        setSharedIn(incoming);
      }
    })().catch(() => setError(message('errors.sharedFiles')));
  }, []);
  useEffect(() => {
    function paste(event: ClipboardEvent) {
      if (tab !== 'send' || busy || draft || event.defaultPrevented) return;
      if (
        event.target instanceof Element &&
        event.target.closest('input,textarea,[contenteditable]:not([contenteditable="false"])')
      )
        return;
      const data = event.clipboardData;
      if (!data) return;
      let images = Array.from(data.files || []).filter((file) => file.type.startsWith('image/'));
      if (!images.length)
        images = Array.from(data.items || [])
          .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
          .map((item) => item.getAsFile())
          .filter((file): file is File => !!file);
      if (!images.length) return;
      event.preventDefault();
      const names = new Set(files.map((file) => file.name));
      const incoming = images.map((file) => {
        let name = file.name || 'image.png';
        const dot = name.lastIndexOf('.');
        const stem = dot > 0 ? name.slice(0, dot) : name;
        const extension = dot > 0 ? name.slice(dot) : '';
        let suffix = 2;
        while (names.has(name)) {
          name = stem + '-' + suffix + extension;
          suffix++;
        }
        names.add(name);
        return name === file.name
          ? file
          : new File([file], name, { type: file.type, lastModified: file.lastModified });
      });
      select(incoming);
    }
    window.addEventListener('paste', paste);
    return () => window.removeEventListener('paste', paste);
  }, [tab, busy, draft, files, me]);
  async function send() {
    setError('');
    if (files.length === 0) return;
    setBusy(true);
    const abort = new AbortController();
    controller.current = abort;
    const release = guardUnload(true);
    try {
      let active = draft;
      const selected: UploadIdentity[] = [];
      for (const file of files) selected.push(await fingerprint(file, abort.signal));
      if (!active) {
        if (abort.signal.aborted) throw new DOMException('Paused', 'AbortError');
        active = await api<Share>('/api/shares', 'POST', {
          title: title || files[0].name,
          description,
          slug,
          days,
          permanent: days === 0,
          password,
          download_limit: limit,
          language: getLanguage(),
          strip_location: stripLocation,
          keep,
          folder_id: keep && keepFolders.some((f) => f.id === keepFolder) ? keepFolder : '',
          files: files.map((f) => ({ name: f.name, size: f.size })),
        });
        setDraft(active);
        setPassword('');
        saveIdentity(active.id, selected, identities.current);
        setShares((current) => [active!, ...current.filter((s) => s.id !== active!.id)]);
        await reload().catch((e) =>
          setError(message('errors.draftRefresh', { error: e as Error })),
        );
      }
      const original = readIdentity(active.id, identities.current);
      if (!original) throw new LocalizedError(message('errors.resumeBrowser'));
      if (
        !sameIdentity(original, selected) ||
        active.files.length !== files.length ||
        new Set(active.files.map((f) => f.name)).size !== active.files.length ||
        active.files.some((f) => !files.some((n) => n.name === f.name && n.size === f.size))
      )
        throw new LocalizedError(message('errors.resumeIdentity'));
      const jobs: UploadJob[] = [];
      for (const target of active.files) {
        if (abort.signal.aborted) throw new DOMException('Paused', 'AbortError');
        const source = files.find((f) => f.name === target.name)!;
        const status = async (signal: AbortSignal) =>
          (await api<SharedFile>('/api/uploads/' + target.id, 'GET', undefined, signal))?.offset;
        const offset = await status(abort.signal);
        if (!Number.isSafeInteger(offset) || offset < 0 || offset > source.size)
          throw new LocalizedError(message('errors.invalidOffset'));
        jobs.push({
          source,
          size: source.size,
          offset,
          url: '/api/uploads/' + target.id,
          status,
        });
      }
      await uploadAll(jobs, {
        signal: abort.signal,
        onProgress: (stats) => {
          setStats(stats);
          setProgress(
            stats.total === 0
              ? 100
              : Math.min(100, Math.max(0, Math.round((stats.sent / stats.total) * 100))),
          );
        },
      });
      if (abort.signal.aborted) throw new DOMException('Paused', 'AbortError');
      let published = await api<Share>('/api/shares/' + active.id + '/publish', 'POST', {});
      thumbsFromUpload(
        published.id,
        active.files.map((target) => ({
          id: target.id,
          source: files.find((f) => f.name === target.name)!,
        })),
      );
      if (notifyNew && canNotify) {
        try {
          await api('/api/shares/' + published.id + '/notify', 'PUT', {
            enabled: true,
            language: getLanguage(),
          });
          published = { ...published, notify: true };
        } catch (e) {
          setError(e as Error);
        }
      }
      setResult(published);
      setFresh(true);
      setQr(false);
      setDraft(null);
      setFiles([]);
      setTitle('');
      setDescription('');
      setSlug('');
      setPassword('');
      setLimit(0);
      setProgress(0);
      setShares((current) => [published, ...current.filter((s) => s.id !== published.id)]);
      removeIdentity(active.id, identities.current);
      if (picker.current) picker.current.value = '';
      await reload().catch((e) =>
        setError(message('errors.publishRefresh', { error: e as Error })),
      );
    } catch (e) {
      if ((e as Error).name === 'AbortError') setError(message('errors.paused'));
      else setError(e as Error);
    } finally {
      release();
      setBusy(false);
      setStats(null);
      controller.current = null;
    }
  }
  async function deleteShare() {
    if (!remove) return;
    setBusy(true);
    setError('');
    try {
      await api('/api/shares/' + remove.id, 'DELETE', {});
      setShares((current) => current.filter((s) => s.id !== remove.id));
      if (draft?.id === remove.id) {
        setDraft(null);
        setFiles([]);
        setProgress(0);
        setTitle('');
        if (picker.current) picker.current.value = '';
      }
      setPassword('');
      removeIdentity(remove.id, identities.current);
      setRemove(null);
      await reload().catch((e) => setError(message('errors.deleteRefresh', { error: e as Error })));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  function resume(sh: Share) {
    setDraft(sh);
    setFiles([]);
    setPassword('');
    setProgress(0);
    if (picker.current) picker.current.value = '';
    setTitle(sh.title);
    setLimit(sh.download_limit || 0);
    setError(message('upload.resumeHint'));
    setTab('send');
  }
  const filtered = shares.filter(
    (s) => !s.mode && s.title.toLowerCase().includes(search.toLowerCase()),
  );
  const pagedShares = usePaged(filtered, search);
  return (
    <div className="app-layout" data-tab={tab} data-rail={railOpen ? 'open' : undefined}>
      <aside className="sidebar">
        <div className="sidebar-label">{t('workspace.label')}</div>
        <nav aria-label={t('workspace.navigation')}>
          <button
            className={tab === 'library' ? 'active' : ''}
            onClick={() => setTab('library')}
            title={t('library.title')}
          >
            <FolderOpen size={18} />
            <span className="nav-text">{t('library.title')}</span>
            <span className="nav-short" aria-hidden="true">
              {t('nav.libraryShort')}
            </span>
          </button>
          <button
            className={tab === 'send' ? 'active' : ''}
            onClick={() => setTab('send')}
            title={t('workspace.send')}
          >
            <Upload size={18} />
            <span className="nav-text">{t('workspace.send')}</span>
            <span className="nav-short" aria-hidden="true">
              {t('nav.sendShort')}
            </span>
          </button>
          <button
            className={tab === 'shares' ? 'active' : ''}
            onClick={() => setTab('shares')}
            title={t('workspace.shares')}
          >
            <Link size={18} />
            <span className="nav-text">{t('workspace.shares')}</span>
            <span className="nav-short" aria-hidden="true">
              {t('nav.sharesShort')}
            </span>
            <span className="count">{formatNumber(shares.length)}</span>
          </button>
          <button
            className={tab === 'requests' ? 'active' : ''}
            onClick={() => setTab('requests')}
            title={t('requests.title')}
          >
            <Inbox size={18} />
            <span className="nav-text">{t('requests.title')}</span>
            <span className="nav-short" aria-hidden="true">
              {t('nav.requestsShort')}
            </span>
          </button>
          {me.user.role === 'admin' && (
            <button
              className={tab === 'admin' ? 'active' : ''}
              onClick={() => setTab('admin')}
              title={t('workspace.admin')}
            >
              <Settings size={18} />
              <span className="nav-text">{t('workspace.admin')}</span>
              <span className="nav-short" aria-hidden="true">
                {t('nav.adminShort')}
              </span>
            </button>
          )}
        </nav>
        <div className="storage-card">
          <div className="storage-head">
            <span>{t('workspace.storage')}</span>
            <HardDrive size={15} />
          </div>
          <div className="quota-track">
            <span
              style={{
                width:
                  Math.min(100, me.user.quota === 0 ? 100 : (me.user.used / me.user.quota) * 100) +
                  '%',
              }}
            />
          </div>
          <span className="mono">
            {formatBytes(me.user.used)} / {formatBytes(me.user.quota)}
          </span>
          <small>{t('workspace.reserved')}</small>
        </div>
        <div
          className="storage-mini"
          title={formatBytes(me.user.used) + ' / ' + formatBytes(me.user.quota)}
        >
          <HardDrive size={16} />
          <span className="mono">{storageShare(me.user.used, me.user.quota)}</span>
        </div>
        {look === 'studio' && (
          <button
            type="button"
            className="rail-toggle"
            aria-expanded={railOpen}
            title={railOpen ? t('workspace.collapseMenu') : t('workspace.expandMenu')}
            onClick={toggleRail}
          >
            {railOpen ? <PanelLeftClose size={18} /> : <PanelLeftOpen size={18} />}
            <span className="nav-text">
              {railOpen ? t('workspace.collapseMenu') : t('workspace.expandMenu')}
            </span>
          </button>
        )}
      </aside>
      <main className="workspace">
        {updateInfo && (
          <UpdateBanner
            info={updateInfo}
            open={() => {
              setTab('admin');
              window.setTimeout(() => document.getElementById('updates')?.scrollIntoView(), 50);
            }}
          />
        )}
        {tab === 'library' ? (
          <Library me={me} reload={reload} />
        ) : tab === 'admin' ? (
          <Admin
            policy={me.policy}
            reload={reload}
            currentUser={me.user.id}
            currentEmail={me.user.email}
            updateInfo={updateInfo}
            back={() => setTab(lastTab.current)}
          />
        ) : tab === 'requests' ? (
          <RequestsPage me={me} />
        ) : (
          <>
            <header className="page-heading">
              <div>
                <div className="eyebrow">
                  {tab === 'send' ? t('workspace.sendStep') : t('workspace.sharesStep')}
                </div>
                <h1>{tab === 'send' ? t('workspace.sendTitle') : t('workspace.sharesTitle')}</h1>
                <p>
                  {tab === 'send'
                    ? t('workspace.sendDescription')
                    : t('workspace.sharesDescription')}
                </p>
              </div>
              {tab === 'shares' && (
                <div className="page-actions">
                  <button className="primary" disabled={busy} onClick={() => setTab('send')}>
                    <Upload size={16} />
                    {t('upload.new')}
                  </button>
                </div>
              )}
            </header>
            {error && (
              <div role="alert" className="error">
                {error}
              </div>
            )}
            {sharedIn.length > 0 && (
              <div role="alert" className="notice shared-in">
                <p>
                  {t('shared.confirm', {
                    count: formatNumber(sharedIn.length),
                    names: sharedIn
                      .slice(0, 3)
                      .map((f) => f.name)
                      .join(', '),
                  })}
                </p>
                <div className="row-actions">
                  <button className="quiet small" onClick={() => setSharedIn([])}>
                    {t('shared.discard')}
                  </button>
                  <button
                    className="primary small"
                    onClick={() => {
                      select(sharedIn);
                      setSharedIn([]);
                    }}
                  >
                    {t('shared.add')}
                  </button>
                </div>
              </div>
            )}
            {tab === 'send' && look !== 'classic' && fresh && result ? (
              <LinkReady
                share={result}
                email={canEmail ? () => setEmailing(result) : undefined}
                edit={() => setEditing(result)}
                again={() => {
                  setResult(null);
                  setFresh(false);
                }}
                openShares={() => {
                  setResult(null);
                  setFresh(false);
                  setTab('shares');
                }}
              />
            ) : tab === 'send' && look !== 'classic' ? (
              <SendLayout
                look={look}
                s={{
                  me,
                  files,
                  setFiles,
                  select,
                  busy,
                  draft,
                  progress,
                  stats,
                  title,
                  setTitle,
                  description,
                  setDescription,
                  slug,
                  setSlug,
                  days,
                  setDays,
                  limit,
                  setLimit,
                  password,
                  setPassword,
                  notifyNew,
                  setNotifyNew,
                  keep,
                  setKeep,
                  keepFolder,
                  setKeepFolder,
                  keepFolders,
                  stripLocation,
                  setStripLocation,
                  canEmail,
                  canNotify,
                  send: () => void send(),
                  pause: () => controller.current?.abort(),
                  deleteDraft: () => draft && setRemove(draft),
                  recent: shares,
                  openShares: () => setTab('shares'),
                }}
              />
            ) : tab === 'send' ? (
              <div className="send-grid">
                <section className="panel upload-panel">
                  <div className="section-title">
                    <span>
                      <Upload size={17} />
                      {t('upload.new')}
                    </span>
                    <small>{t('upload.local')}</small>
                  </div>
                  <div
                    className={'dropzone ' + (drag ? 'dragging' : '')}
                    tabIndex={0}
                    aria-label={t('upload.drop')}
                    onClick={(e) => {
                      if (e.target instanceof Element && !e.target.closest('button,input'))
                        e.currentTarget.focus();
                    }}
                    onDragOver={(e) => {
                      e.preventDefault();
                      if (!busy) setDrag(true);
                    }}
                    onDragLeave={() => setDrag(false)}
                    onDrop={(e) => {
                      e.preventDefault();
                      setDrag(false);
                      const gather = droppedEntries(e.dataTransfer);
                      void gather().then((g) => select(g.files));
                    }}
                  >
                    <div className="upload-symbol">
                      <Upload size={30} />
                    </div>
                    <h2>{draft ? t('upload.continueTitle') : t('upload.drop')}</h2>
                    <p>{t('upload.allTypes')}</p>
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => picker.current?.click()}
                    >
                      {t('upload.choose')}
                      <ArrowUpRight size={16} />
                    </button>
                    <input
                      ref={picker}
                      className="sr-only"
                      type="file"
                      aria-label={t('upload.choose')}
                      multiple
                      disabled={busy}
                      onChange={(e) => select(Array.from(e.target.files || []))}
                    />
                    <button
                      type="button"
                      className="text-button"
                      disabled={busy || !!draft}
                      onClick={() => folderPicker.current?.click()}
                    >
                      {t('upload.chooseFolder')}
                    </button>
                    <input
                      ref={folderPicker}
                      className="sr-only"
                      type="file"
                      aria-label={t('upload.chooseFolder')}
                      disabled={busy || !!draft}
                      onChange={(e) => {
                        select(chosenFolder(e.target.files).files);
                        e.target.value = '';
                      }}
                    />
                    {!draft && <small>{t('upload.pasteHint')}</small>}
                    <small>
                      {t('upload.limits', {
                        file: formatBytes(me.policy.max_file),
                        share: formatBytes(me.policy.max_share),
                      })}
                    </small>
                  </div>
                  {files.length > 0 && (
                    <div className="selected-files">
                      {files.map((f) => (
                        <div className="file-row" key={f.name}>
                          <LocalThumb file={f} />
                          <div className="file-name">
                            <strong>{f.name}</strong>
                            <small>{formatBytes(f.size)}</small>
                          </div>
                          {!draft && (
                            <button
                              aria-label={t('files.remove', { name: f.name })}
                              className="icon-button"
                              disabled={busy}
                              onClick={() => setFiles(files.filter((n) => n !== f))}
                            >
                              <X size={17} />
                            </button>
                          )}
                        </div>
                      ))}
                      <div className="file-total">
                        <span>
                          {files.length === 1
                            ? t('files.countOne')
                            : t('files.count', { count: formatNumber(files.length) })}
                        </span>
                        <strong>{formatBytes(total)}</strong>
                      </div>
                    </div>
                  )}
                  {draft && (
                    <div className="upload-progress">
                      <div>
                        <span>{busy ? t('upload.uploading') : t('upload.saved')}</span>
                        <strong className="mono">
                          {formatNumber(progress / 100, { style: 'percent' })}
                        </strong>
                      </div>
                      <progress aria-label={t('upload.uploading')} value={progress} max="100" />
                      {busy && <UploadSpeed stats={stats} />}
                      <small>{t('upload.draftExpiry')}</small>
                      <button className="quiet" disabled={busy} onClick={() => setRemove(draft)}>
                        {t('upload.deleteDraft')}
                      </button>
                    </div>
                  )}
                </section>
                <section className="panel options-panel">
                  <div className="section-title">
                    <span>
                      <Link size={17} />
                      {t('share.yourLink')}
                    </span>
                    <small>{t('share.settings')}</small>
                  </div>
                  <div className="options">
                    <label>
                      {t('share.title')}
                      <input
                        disabled={busy || !!draft}
                        value={title}
                        onChange={(e) => setTitle(e.target.value)}
                        placeholder={t('share.titlePlaceholder')}
                        maxLength={160}
                      />
                    </label>
                    <div className="field-grid">
                      <Select
                        label={t('share.validity')}
                        value={days}
                        disabled={busy || !!draft}
                        onChange={setDays}
                        options={validityOptions([me.policy.default_days])}
                      />
                      <label>
                        {t('auth.password')} <span className="muted">{t('share.optional')}</span>
                        <input
                          type="password"
                          autoComplete="new-password"
                          disabled={busy || !!draft}
                          value={password}
                          onChange={(e) => setPassword(e.target.value)}
                          maxLength={72}
                          placeholder={t('share.passwordPlaceholder')}
                        />
                      </label>
                      <DownloadLimitSelect
                        value={limit}
                        onChange={setLimit}
                        disabled={busy || !!draft}
                      />
                      {limit > 0 && (
                        <small className="field-hint grid-hint">{limitHint(limit, keep)}</small>
                      )}
                    </div>
                    <label>
                      {t('share.description')} <span className="muted">{t('share.optional')}</span>
                      <textarea
                        rows={3}
                        disabled={busy || !!draft}
                        value={description}
                        onChange={(e) => setDescription(e.target.value)}
                        maxLength={1000}
                        placeholder={t('share.descriptionPlaceholder')}
                      />
                    </label>
                    <label>
                      {t('share.customLink')} <span className="muted">{t('share.optional')}</span>
                      <input
                        className="mono"
                        disabled={busy || !!draft}
                        value={slug}
                        onChange={(e) => setSlug(e.target.value.toLowerCase())}
                        maxLength={48}
                        placeholder={t('share.customLinkPlaceholder')}
                      />
                    </label>
                    <small className="field-hint">{t('share.customLinkHint')}</small>
                    {canEmail && (
                      <div>
                        <label className="checkbox">
                          <input
                            type="checkbox"
                            checked={notifyNew && canNotify}
                            disabled={busy || !!draft || !canNotify}
                            onChange={(e) => setNotifyNew(e.target.checked)}
                          />
                          {t('email.notifyOnDownload')}
                        </label>
                        {!canNotify && (
                          <small className="field-help">{t('email.addAddressFirst')}</small>
                        )}
                      </div>
                    )}
                    <div>
                      <label className="checkbox">
                        <input
                          type="checkbox"
                          checked={keep}
                          disabled={busy || !!draft}
                          onChange={(e) => setKeep(e.target.checked)}
                        />
                        {t('share.keep')}
                      </label>
                      {!keep && <small className="field-help">{t('share.keepOff')}</small>}
                    </div>
                    <div>
                      <label className="checkbox">
                        <input
                          type="checkbox"
                          checked={stripLocation}
                          disabled={busy || !!draft}
                          onChange={(e) => setStripLocation(e.target.checked)}
                        />
                        {t('share.stripLocation')}
                      </label>
                      <small className="field-help">{t('share.stripLocationHint')}</small>
                    </div>
                    {keep && (
                      <KeepFolderSelect
                        folders={keepFolders}
                        value={keepFolder}
                        onChange={setKeepFolder}
                        disabled={busy || !!draft}
                      />
                    )}
                    <p className="privacy-note">
                      <ShieldCheck size={17} />
                      <span>{t('share.privacy')}</span>
                    </p>
                    {busy ? (
                      <button
                        className="secondary full"
                        onClick={() => controller.current?.abort()}
                      >
                        <Pause size={17} />
                        {t('upload.pause')}
                      </button>
                    ) : (
                      <button
                        className="primary full"
                        disabled={files.length === 0}
                        onClick={() => void send()}
                      >
                        {draft ? <Play size={17} /> : <ArrowUpRight size={17} />}{' '}
                        {draft ? t('upload.continue') : t('share.createLink')}
                      </button>
                    )}
                    <small className="button-note">
                      {files.length
                        ? t('files.summary', {
                            count: formatNumber(files.length),
                            size: formatBytes(total),
                          })
                        : t('share.linkAfterUpload')}
                    </small>
                  </div>
                </section>
                <div className="send-bottom">
                  <span>
                    <Clock size={16} /> {t('share.cleanup')}
                  </span>
                  <span>
                    <Lock size={16} /> {t('share.onServer')}
                  </span>
                </div>
              </div>
            ) : (
              <>
                <div className="view-tabs" role="tablist" aria-label={t('workspace.shares')}>
                  <button
                    role="tab"
                    aria-selected={sharesView === 'sent'}
                    onClick={() => setSharesView('sent')}
                  >
                    <Upload size={15} />
                    {t('shares.sent')}
                  </button>
                  <button
                    role="tab"
                    aria-selected={sharesView === 'library'}
                    onClick={() => setSharesView('library')}
                  >
                    <FolderOpen size={15} />
                    {t('shares.fromLibrary')}
                  </button>
                </div>
                {sharesView === 'library' ? (
                  <LibraryLinks me={me} reload={reload} />
                ) : (
                  <>
                    <div className="list-toolbar">
                      <label className="search">
                        <Search size={16} />
                        <input
                          aria-label={t('share.search')}
                          placeholder={t('share.searchPlaceholder')}
                          value={search}
                          onChange={(e) => setSearch(e.target.value)}
                        />
                      </label>
                    </div>
                    {filtered.length === 0 ? (
                      <div className="empty-state">
                        <FolderOpen size={35} />
                        <h2>{search ? t('share.noMatch') : t('share.emptyTitle')}</h2>
                        <p>
                          {search ? t('share.noMatchDescription') : t('share.emptyDescription')}
                        </p>
                      </div>
                    ) : (
                      <div className="shares-list">
                        {pagedShares.shown.map((sh) => (
                          <article className="panel share-card" key={sh.id}>
                            <span className="file-icon">
                              <FolderOpen size={22} />
                            </span>
                            <div className="share-info">
                              <h2>{sh.title}</h2>
                              {sh.description && (
                                <p className="share-description">{sh.description}</p>
                              )}
                              <div>
                                <span>
                                  {t('files.summary', {
                                    count: formatNumber(sh.files.length),
                                    size: formatBytes(sh.files.reduce((n, f) => n + f.size, 0)),
                                  })}
                                </span>
                                <span>
                                  <Clock size={13} />
                                  {sh.expires === 0
                                    ? t('share.neverExpires')
                                    : formatDate(sh.expires * 1000)}
                                </span>
                                {sh.protected && (
                                  <span>
                                    <Lock size={13} />
                                    {t('share.protected')}
                                  </span>
                                )}
                                {sh.published && (
                                  <span>
                                    <Download size={13} />
                                    {sh.downloads === 1
                                      ? t('share.downloadsOne')
                                      : t('share.downloads', {
                                          count: formatNumber(sh.downloads || 0),
                                        })}
                                  </span>
                                )}
                                {!!sh.download_limit && (
                                  <span>
                                    {t('downloads.limitShort', {
                                      count: formatNumber(sh.download_limit),
                                    })}
                                  </span>
                                )}
                                {!!sh.last_download && (
                                  <span>
                                    {t('downloads.lastDownload', {
                                      date: formatDate(sh.last_download * 1000),
                                    })}
                                  </span>
                                )}
                              </div>
                              {sh.published && (
                                <ul className="owner-preview-files">
                                  {sh.files.map((file) => (
                                    <li key={file.id}>
                                      <FileThumb shareId={sh.id} file={file} owner />
                                      <button
                                        className="filename-preview"
                                        disabled={
                                          busy ||
                                          file.offset !== file.size ||
                                          (sh.expires !== 0 && sh.expires * 1000 <= Date.now())
                                        }
                                        onClick={() => setPreview({ shareId: sh.id, file })}
                                      >
                                        {file.name}
                                      </button>
                                      <FileDownloads share={sh} file={file} />
                                    </li>
                                  ))}
                                </ul>
                              )}
                            </div>
                            <span className={'badge ' + (!sh.published ? 'draft' : '')}>
                              {!sh.published
                                ? t('share.draft')
                                : sh.expires !== 0 && sh.expires * 1000 <= Date.now()
                                  ? t('share.expired')
                                  : t('share.active')}
                            </span>
                            <div className="card-actions">
                              {sh.published ? (
                                <>
                                  <button
                                    className="icon-button"
                                    disabled={busy}
                                    aria-label={t('share.copyNamed', { title: sh.title })}
                                    onClick={() => {
                                      setResult(sh);
                                      setFresh(false);
                                      setQr(false);
                                    }}
                                  >
                                    <Copy size={17} />
                                  </button>
                                  <button
                                    className="icon-button"
                                    disabled={busy}
                                    aria-label={t('share.qrNamed', { title: sh.title })}
                                    onClick={() => {
                                      setResult(sh);
                                      setFresh(false);
                                      setQr(true);
                                    }}
                                  >
                                    <QrCode size={18} />
                                  </button>
                                  <button
                                    className="icon-button"
                                    disabled={
                                      busy || (sh.expires !== 0 && sh.expires * 1000 <= Date.now())
                                    }
                                    aria-label={t('edit.named', { title: sh.title })}
                                    title={t('edit.action')}
                                    onClick={() => setEditing(sh)}
                                  >
                                    <Pencil size={17} />
                                  </button>
                                  {canEmail && (
                                    <button
                                      className="icon-button"
                                      disabled={busy}
                                      aria-label={t('email.sendNamed', { title: sh.title })}
                                      onClick={() => setEmailing(sh)}
                                    >
                                      <Mail size={17} />
                                    </button>
                                  )}
                                  <NotifyToggle
                                    share={sh}
                                    available={canNotify}
                                    changed={(notify) =>
                                      setShares((current) =>
                                        current.map((s) => (s.id === sh.id ? { ...s, notify } : s)),
                                      )
                                    }
                                  />
                                </>
                              ) : (
                                <button
                                  className="quiet"
                                  disabled={busy}
                                  onClick={() => resume(sh)}
                                >
                                  {t('share.resume')}
                                </button>
                              )}
                              <button
                                className="icon-button danger"
                                disabled={busy}
                                aria-label={t('share.deleteNamed', { title: sh.title })}
                                onClick={() => setRemove(sh)}
                              >
                                <Trash2 size={17} />
                              </button>
                            </div>
                          </article>
                        ))}
                        <ShowMore paged={pagedShares} />
                      </div>
                    )}
                  </>
                )}
              </>
            )}
          </>
        )}
      </main>
      {preview && (
        <FilePreview
          key={preview.shareId + '/' + preview.file.id}
          shareId={preview.shareId}
          file={preview.file}
          owner
          close={() => setPreview(null)}
        />
      )}
      {result && !(fresh && look !== 'classic' && tab === 'send') && (
        <ShareDialog
          share={result}
          showQr={qr}
          close={() => setResult(null)}
          email={canEmail ? () => setEmailing(result) : undefined}
        />
      )}
      {searching && (
        <SearchPalette
          close={() => setSearching(false)}
          admin={me.user.role === 'admin'}
          shares={shares}
          openTab={setTab}
          openPath={openPath}
          openShare={(sh) => {
            setTab('shares');
            setSharesView(sh.mode ? 'library' : 'sent');
            if (!sh.mode && sh.published) {
              setResult(sh);
              setFresh(false);
              setQr(false);
            }
          }}
        />
      )}
      {emailing && <EmailShareDialog share={emailing} close={() => setEmailing(null)} />}
      {extending && (
        <ExtendShareDialog
          share={extending}
          close={() => setExtending(null)}
          saved={(updated) =>
            setShares((current) => current.map((s) => (s.id === updated.id ? updated : s)))
          }
        />
      )}
      {editing && (
        <EditShareDialog
          share={editing}
          close={() => setEditing(null)}
          saved={(updated) => {
            setShares((current) => current.map((s) => (s.id === updated.id ? updated : s)));
            setResult((current) => (current?.id === updated.id ? updated : current));
          }}
        />
      )}
      {remove && (
        <Modal
          title={t('share.deleteTitle')}
          close={() => {
            if (!busy) setRemove(null);
          }}
        >
          <p>{t('share.deleteDescription', { title: remove.title })}</p>
          <div className="dialog-actions">
            <button className="secondary" disabled={busy} onClick={() => setRemove(null)}>
              {t('common.cancel')}
            </button>
            <button
              className="primary danger-solid"
              disabled={busy}
              onClick={() => void deleteShare()}
            >
              {busy ? <LoaderCircle className="spin" size={16} /> : <Trash2 size={16} />}{' '}
              {t('share.delete')}
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

// The first picture of a new link, shown large with the done mark on it. The
// small thumbnail stands in until the full picture has loaded.
function ReadyImage({ shareId, file }: { shareId: string; file: SharedFile }) {
  const [source, setSource] = useState('');
  useEffect(() => {
    // Asked for once its scan is done.
    if (file.scan) return;
    const controller = new AbortController();
    let url = '';
    fetch(
      '/api/shares/' +
        encodeURIComponent(shareId) +
        '/files/' +
        encodeURIComponent(file.id) +
        '/preview',
      {
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        headers: { 'X-MagiSend': '1' },
        signal: controller.signal,
      },
    )
      .then((response) => {
        const type = (response.headers?.get('Content-Type') || '').split(';')[0].trim();
        return response.ok && /^image\/(png|jpeg|gif|webp)$/.test(type) ? response.blob() : null;
      })
      .then((blob) => {
        if (!blob || controller.signal.aborted) return;
        url = URL.createObjectURL(blob);
        setSource(url);
      })
      .catch(() => {});
    return () => {
      controller.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [shareId, file.id, file.scan]);
  return (
    <div className="ready-image">
      <span>
        {source ? (
          <img src={source} alt={file.name} onError={() => setSource('')} />
        ) : (
          <FileThumb shareId={shareId} file={file} owner />
        )}
        <span className="ready-badge" aria-hidden="true">
          <Check size={16} />
        </span>
      </span>
    </div>
  );
}

// The moment after a link is made on Send, in the new looks: the link is
// copied straight away when the browser allows it, the QR code is always in
// view, and the next steps are one click away.
function LinkReady({
  share,
  email,
  edit,
  again,
  openShares,
}: {
  share: Share;
  email?: () => void;
  edit: () => void;
  again: () => void;
  openShares: () => void;
}) {
  useLanguage();
  const url = shareUrl(share);
  const [copied, setCopied] = useState(false);
  const [qr, setQr] = useState('');
  const input = useRef<HTMLInputElement>(null);
  async function copy() {
    try {
      await copyText(url);
      setCopied(true);
    } catch {
      setCopied(false);
      input.current?.focus();
      input.current?.select();
    }
  }
  useEffect(() => {
    let live = true;
    setCopied(false);
    // Browsers may refuse a copy that no click started; the button stays.
    copyText(url).then(
      () => live && setCopied(true),
      () => {},
    );
    QRCode.toDataURL(url, { width: 240, margin: 1, color: { dark: '#050505', light: '#ffffff' } })
      .then((value) => live && setQr(value))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [url]);
  const images = share.files.filter((f) => rasterName.test(f.name));
  const size = share.files.reduce((n, f) => n + f.size, 0);
  return (
    <section className="link-ready" aria-live="polite">
      <div className="link-ready-main">
        {images.length > 0 ? (
          <ReadyImage shareId={share.id} file={images[0]} />
        ) : (
          <div className="success-mark">
            <Check size={28} />
          </div>
        )}
        <h2>{t('share.readyTitle')}</h2>
        <p className="link-ready-meta">
          {share.title} ·{' '}
          {share.files.length === 1
            ? t('files.countOne')
            : t('files.count', { count: formatNumber(share.files.length) })}{' '}
          · {formatBytes(size)} ·{' '}
          {share.expires === 0
            ? t('share.neverExpires')
            : t('share.expires', { date: formatDate(share.expires * 1000, true) })}
          {share.protected ? t('share.passwordProtected') : ''}
        </p>
        <div className="link-field">
          <input
            ref={input}
            aria-label={t('share.link')}
            value={url}
            readOnly
            onFocus={(e) => e.currentTarget.select()}
          />
          <button className="primary" onClick={() => void copy()}>
            {copied ? <Check size={16} /> : <Copy size={16} />}
            {copied ? t('share.copied') : t('share.copy')}
          </button>
        </div>
        {copied && <p className="link-copied">{t('share.autoCopied')}</p>}
        <div className="link-ready-actions">
          {email && (
            <button className="secondary small" onClick={email}>
              <Mail size={15} />
              {t('email.sendByEmail')}
            </button>
          )}
          <a className="secondary small" href={url} target="_blank" rel="noopener noreferrer">
            <ArrowUpRight size={15} />
            {t('share.openLink')}
          </a>
          <button className="secondary small" onClick={edit}>
            <Pencil size={15} />
            {t('share.editLink')}
          </button>
          <button className="quiet small" onClick={openShares}>
            {t('send.seeAll')}
          </button>
        </div>
        <button className="primary send-again" onClick={again}>
          <Upload size={16} />
          {t('share.sendMore')}
        </button>
      </div>
      <figure className="link-ready-qr">
        {qr ? <img src={qr} alt={t('share.qrAlt')} /> : <QrCode size={40} />}
        <figcaption>{t('share.scanQr')}</figcaption>
      </figure>
    </section>
  );
}

export function ShareDialog({
  share,
  showQr,
  close,
  email,
}: {
  share: Share;
  showQr: boolean;
  close: () => void;
  email?: () => void;
}) {
  useLanguage();
  const [copied, setCopied] = useState(false);
  const [qr, setQr] = useState('');
  const [error, setError] = useMessage();
  const input = useRef<HTMLInputElement>(null);
  const live = useRef(0);
  const url = shareUrl(share);
  useEffect(() => {
    const generation = ++live.current;
    setCopied(false);
    setQr('');
    setError('');
    QRCode.toDataURL(url, { width: 220, margin: 2, color: { dark: '#050505', light: '#ffffff' } })
      .then((value) => {
        if (live.current === generation) setQr(value);
      })
      .catch(() => {
        if (live.current === generation) setError(message('errors.qr'));
      });
    return () => {
      live.current++;
    };
  }, [url]);
  async function copy() {
    const generation = live.current;
    try {
      await copyText(url).catch(() => {
        input.current?.focus();
        input.current?.select();
        throw new LocalizedError(message('errors.copyManually'));
      });
      if (live.current === generation) setCopied(true);
    } catch (e) {
      if (live.current === generation) {
        setCopied(false);
        setError(e as Error);
      }
    }
  }
  const images = share.files.filter((f) => rasterName.test(f.name));
  return (
    <Modal title={t('share.ready')} close={close}>
      {images.length > 0 ? (
        <ReadyImage shareId={share.id} file={images[0]} />
      ) : (
        <div className="success-mark">
          <Check size={28} />
        </div>
      )}
      <p className="ready-title">
        {share.title} ·{' '}
        {share.files.length === 1
          ? t('files.countOne')
          : t('files.count', { count: formatNumber(share.files.length) })}
      </p>
      {share.description && <p className="share-description">{share.description}</p>}
      {images.length > 1 && (
        <div className="result-thumbs" aria-hidden="true">
          {images.slice(1).map((f) => (
            <FileThumb key={f.id} shareId={share.id} file={f} owner />
          ))}
        </div>
      )}
      <label>
        {t('share.link')}
        <input ref={input} value={url} readOnly onFocus={(e) => e.currentTarget.select()} />
      </label>
      <button className="primary full" onClick={() => void copy()}>
        {copied ? <Check size={17} /> : <Copy size={17} />}{' '}
        {copied ? t('share.copied') : t('share.copy')}
      </button>
      {email && (
        <button className="secondary full dialog-secondary" onClick={email}>
          <Mail size={17} />
          {t('email.sendByEmail')}
        </button>
      )}
      <details open={showQr} className="qr-details">
        <summary>
          <QrCode size={17} /> {t('share.showQr')}
        </summary>
        {qr && <img className="qr-image" src={qr} alt={t('share.qrAlt')} />}
      </details>
      <small className="muted">
        {share.expires === 0
          ? t('share.neverExpires')
          : t('share.expires', { date: formatDate(share.expires * 1000, true) })}
        {share.protected ? t('share.passwordProtected') : ''}
      </small>
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
    </Modal>
  );
}
