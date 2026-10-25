import { useEffect, useRef, useState } from 'react';
import {
  Activity,
  Bell,
  DatabaseBackup,
  ArrowLeft,
  AtSign,
  Gauge,
  LayoutDashboard,
  MailPlus,
  Palette,
  RefreshCw,
  Search,
  Users,
  UserPlus,
  Save,
  HardDrive,
  Shield,
  ShieldCheck,
  KeyRound,
  UserX,
  UserCheck,
  Trash2,
  ShieldOff,
} from 'lucide-react';
import { SettingRow } from './SettingRow';
import MoreMenu from './MoreMenu';
import { UpdatePanel, type UpdateInfo } from './Updates';
import { EmailSettingsPanel } from './Email';
import { PushAdminPanel } from './Push';
import { AntivirusPanel } from './Antivirus';
import { BackupPanel } from './Backups';
import { InvitesPanel } from './Invites';
import { ActivityPanel, OverviewPanel } from './AdminOverview';
import { StoragePanel } from './StoragePanel';
import { UnclaimedPanel } from './UnclaimedPanel';
import { BrandingPanel } from './BrandingPanel';
import { Modal } from './Modal';
import { Avatar } from './Profile';
import { api, formatBytes, type Policy, type User, type Capabilities } from './api';
import {
  t,
  useLanguage,
  validateCredentialsMessage,
  useMessage,
  message,
  type MessageDescriptor,
} from './i18n';
const capabilityKeys: readonly (keyof Capabilities)[] = [
  'folders',
  'upload',
  'share',
  'edit_own',
  'grant_edit',
  'edit_shared',
  'add_shared',
  'delete_shared',
];
const capabilityLabels: Record<keyof Capabilities, string> = {
  folders: 'capability.folders',
  upload: 'capability.upload',
  share: 'capability.share',
  edit_own: 'capability.editOwn',
  edit_shared: 'capability.editShared',
  grant_edit: 'capability.grantEdit',
  add_shared: 'capability.addShared',
  delete_shared: 'capability.deleteShared',
};
const capabilityGroups: readonly { label: string; keys: readonly (keyof Capabilities)[] }[] = [
  { label: 'capability.groupLibrary', keys: ['folders', 'upload', 'share'] },
  { label: 'capability.groupEditing', keys: ['edit_own', 'grant_edit'] },
  { label: 'capability.groupShared', keys: ['edit_shared', 'add_shared', 'delete_shared'] },
];
// Values 'inherit' / 'on' / 'off'. The backend stores a nullable
// per-user override: null removes the row so future default changes inherit
// dynamically, exactly what 'inherit' selects here. Only one capability is
// sent per action, so explicit settings for other capabilities are preserved.
function overrideValue(
  user: User,
  name: keyof Capabilities,
  defaults: Capabilities,
  overrides: Record<string, Partial<Record<keyof Capabilities, boolean>>>,
): 'inherit' | 'on' | 'off' {
  const row = overrides[user.id];
  if (!row || !Object.hasOwn(row, name)) return 'inherit';
  return row[name] ? 'on' : 'off';
}
const GIB = 1073741824;
const gib = (bytes: number) => Math.round((bytes / GIB) * 100) / 100;
export default function Admin({
  policy,
  reload,
  currentUser = '',
  currentEmail = '',
  updateInfo = null,
  back,
}: {
  policy: Policy;
  reload: () => Promise<void>;
  currentUser?: string;
  currentEmail?: string;
  updateInfo?: UpdateInfo | null;
  back?: () => void;
}) {
  useLanguage();
  const [p, setP] = useState(policy);
  const [users, setUsers] = useState<User[]>([]);
  const [storage, setStorage] = useState<{
    backend: string;
    reserved_bytes: number;
    capacity_bytes: number;
    disk_free_bytes: number;
    locations?: { id: string; path: string; active: boolean }[];
  } | null>(null);
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [quota, setQuota] = useState(policy.default_quota / GIB);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [notice, setNotice] = useMessage();
  const [editing, setEditing] = useState('');
  const [resetting, setResetting] = useState<User | null>(null);
  const [resetPassword, setResetPassword] = useState('');
  const [deleting, setDeleting] = useState<User | null>(null);
  const [defaults, setDefaults] = useState<Capabilities | null>(null);
  const [overrides, setOverrides] = useState<
    Record<string, Partial<Record<keyof Capabilities, boolean>>>
  >({});
  async function load() {
    try {
      const [u, s] = await Promise.all([
        api<User[]>('/api/admin/users'),
        api<NonNullable<typeof storage>>('/api/admin/storage'),
      ]);
      setUsers(u);
      setStorage(s);
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
    void loadCaps();
  }, []);
  async function loadCaps() {
    try {
      const data = await api<{
        defaults?: Capabilities;
        overrides?: Record<string, Partial<Record<keyof Capabilities, boolean>>>;
      }>('/api/admin/capabilities');
      setDefaults(data?.defaults || null);
      setOverrides(data?.overrides || {});
    } catch (e) {
      setError(e as Error);
    }
  }
  async function action(fn: () => Promise<void>, notice: MessageDescriptor) {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await fn();
      setNotice(notice);
      await load();
      await loadCaps();
      await reload();
    } catch (e) {
      setError(e as Error);
      await loadCaps();
    } finally {
      setBusy(false);
    }
  }
  function updateQuota(user: User, value: number) {
    setUsers(users.map((u) => (u.id === user.id ? { ...u, quota: value } : u)));
  }
  async function saveDefault(name: keyof Capabilities, value: boolean) {
    setDefaults((d) => d && { ...d, [name]: value });
    await action(async () => {
      await api('/api/admin/capabilities/defaults', 'PUT', { [name]: value });
    }, message('capability.saved'));
  }
  async function saveOverride(
    user: User,
    name: keyof Capabilities,
    value: 'inherit' | 'on' | 'off',
  ) {
    setOverrides((o) => {
      const row = { ...o[user.id] };
      if (value === 'inherit') delete row[name];
      else row[name] = value === 'on';
      return { ...o, [user.id]: row };
    });
    await action(async () => {
      await api('/api/admin/capabilities/users/' + user.id, 'PUT', {
        [name]: value === 'inherit' ? null : value === 'on',
      });
    }, message('capability.saved'));
  }
  const editingUser = users.find((u) => u.id === editing);
  const overrideCount = (id: string) => Object.keys(overrides[id] || {}).length;
  const budget = gib(p.storage_budget || storage?.capacity_bytes || 4 * GIB);
  const gibField = (
    key: 'default_quota' | 'max_file' | 'max_share',
    label: string,
    hint: string,
    min = 0.01,
  ) => (
    <SettingRow id={'policy-' + key} label={label} hint={hint}>
      <input
        id={'policy-' + key}
        aria-describedby={'policy-' + key + '-hint'}
        required
        type="number"
        step="0.01"
        min={min}
        max={budget}
        value={gib(p[key])}
        onChange={(e) => setP({ ...p, [key]: Math.round(Number(e.target.value) * GIB) })}
      />
    </SettingRow>
  );
  const countField = (
    key: 'files_per_user' | 'files_total',
    label: string,
    hint: string,
    min: number,
    max = 10000000,
  ) => (
    <SettingRow id={'policy-' + key} label={label} hint={hint}>
      <input
        id={'policy-' + key}
        aria-describedby={'policy-' + key + '-hint'}
        type="number"
        step="1"
        min={min}
        max={max}
        value={p[key] ?? ''}
        onChange={(e) => setP({ ...p, [key]: Math.round(Number(e.target.value)) })}
      />
    </SettingRow>
  );
  const sections = [
    { key: 'overview', label: t('admin.nav.overview'), icon: LayoutDashboard },
    { key: 'accounts', label: t('admin.nav.accounts'), icon: Users },
    { key: 'invites', label: t('admin.nav.invites'), icon: MailPlus },
    { key: 'limits', label: t('admin.nav.limits'), icon: Gauge },
    { key: 'permissions', label: t('admin.nav.permissions'), icon: Shield },
    ...(currentUser ? [{ key: 'email', label: t('admin.nav.email'), icon: AtSign }] : []),
    { key: 'push', label: t('admin.nav.push'), icon: Bell },
    { key: 'storage', label: t('admin.nav.storage'), icon: HardDrive },
    { key: 'backups', label: t('admin.nav.backups'), icon: DatabaseBackup },
    { key: 'antivirus', label: t('admin.nav.antivirus'), icon: ShieldCheck },
    { key: 'branding', label: t('admin.nav.branding'), icon: Palette },
    ...(currentUser ? [{ key: 'updates', label: t('admin.nav.updates'), icon: RefreshCw }] : []),
    { key: 'activity', label: t('admin.nav.activity'), icon: Activity },
  ];
  const [query, setQuery] = useState('');
  const [matches, setMatches] = useState<string[] | null>(null);
  const [current, setCurrent] = useState('overview');
  const sectionsRef = useRef<HTMLDivElement>(null);
  const labels = useRef(sections);
  labels.current = sections;
  // Searching hides the settings and sections that do not mention the words,
  // and keeps doing so while panels finish loading.
  useEffect(() => {
    const root = sectionsRef.current;
    if (!root) return;
    const q = query.trim().toLocaleLowerCase();
    const text = (el: Element) => (el.textContent ?? '').toLocaleLowerCase();
    const apply = () => {
      const found: string[] = [];
      root.querySelectorAll<HTMLElement>(':scope > .admin-section').forEach((section) => {
        const key = section.id.slice('admin-'.length);
        const title = labels.current.find((s) => s.key === key)?.label.toLocaleLowerCase() ?? '';
        const whole =
          !q ||
          title.includes(q) ||
          [...section.querySelectorAll('h2')].some((h) => text(h).includes(q));
        let hit = whole;
        section.querySelectorAll<HTMLElement>('.setting-row, .cap-default').forEach((row) => {
          const show = whole || text(row).includes(q);
          row.hidden = !show;
          hit ||= show;
        });
        hit ||= text(section).includes(q);
        section.hidden = !hit;
        if (hit) found.push(key);
      });
      const next = q ? found : null;
      setMatches((prev) => (String(prev) === String(next) ? prev : next));
    };
    apply();
    if (!q) return;
    const observer = new MutationObserver(apply);
    observer.observe(root, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [query]);
  // The list beside the sections marks the one being read.
  useEffect(() => {
    const root = sectionsRef.current;
    if (!root || typeof IntersectionObserver === 'undefined') return;
    const visible = new Map<string, boolean>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) visible.set(entry.target.id, entry.isIntersecting);
        const first = [...root.querySelectorAll<HTMLElement>(':scope > .admin-section')].find(
          (el) => !el.hidden && visible.get(el.id),
        );
        if (first) setCurrent(first.id.slice('admin-'.length));
      },
      { rootMargin: '-120px 0px -55% 0px' },
    );
    root.querySelectorAll(':scope > .admin-section').forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, [sections.length]);
  // On narrow screens the list scrolls sideways; keep the marked one in view.
  const navRef = useRef<HTMLElement>(null);
  useEffect(() => {
    const nav = navRef.current;
    const link = nav?.querySelector<HTMLElement>('[aria-current]');
    if (nav && link && nav.scrollWidth > nav.clientWidth)
      nav.scrollLeft += link.getBoundingClientRect().left - nav.getBoundingClientRect().left - 16;
  }, [current]);
  function jump(key: string) {
    setCurrent(key);
    document.getElementById('admin-' + key)?.scrollIntoView({
      behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
      block: 'start',
    });
  }
  return (
    <>
      <header className="page-heading">
        <div>
          <div className="eyebrow">{t('admin.step')}</div>
          <h1>{t('admin.title')}</h1>
          <p>{t('admin.description')}</p>
        </div>
      </header>
      {error && !editing && !resetting && !deleting && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notice && (
        <div role="status" className="success-note">
          {notice}
        </div>
      )}
      <div className="admin-search">
        <Search size={17} aria-hidden="true" />
        <input
          type="search"
          value={query}
          aria-label={t('admin.search')}
          placeholder={t('admin.searchPlaceholder')}
          autoComplete="off"
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => e.key === 'Escape' && setQuery('')}
        />
      </div>
      <div className="admin-layout">
        <nav ref={navRef} className="admin-nav" aria-label={t('admin.sections')}>
          {back && (
            <button type="button" className="admin-back" onClick={back}>
              <ArrowLeft size={16} aria-hidden="true" />
              {t('admin.backToWorkspace')}
            </button>
          )}
          {sections
            .filter((s) => !matches || matches.includes(s.key))
            .map((s) => (
              <a
                key={s.key}
                href={'#admin-' + s.key}
                aria-current={current === s.key ? 'true' : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  jump(s.key);
                }}
              >
                <s.icon size={16} aria-hidden="true" />
                {s.label}
              </a>
            ))}
        </nav>
        <div className="admin-grid" ref={sectionsRef}>
          {matches?.length === 0 && (
            <p className="muted admin-search-empty">{t('admin.searchEmpty')}</p>
          )}
          <div className="admin-section" id="admin-overview">
            <OverviewPanel />
          </div>
          <div className="admin-section" id="admin-accounts">
            <section className="panel admin-wide">
              <h2>
                <UserPlus size={18} /> {t('admin.accounts')}
              </h2>
              {users.map((u) => {
                const self = u.id === currentUser;
                const percent = u.quota ? Math.min(100, (u.used / u.quota) * 100) : 100;
                return (
                  <div className="user-row" key={u.id}>
                    <Avatar user={u} size={32} />
                    <div>
                      <strong>
                        <button
                          type="button"
                          className="filename-preview"
                          onClick={() => setEditing(u.id)}
                        >
                          {u.username}
                        </button>
                        {u.display_name && <span className="muted">{u.display_name}</span>}
                        <span className={'badge ' + (u.role === 'admin' ? 'accent' : 'neutral')}>
                          {u.role === 'admin' ? t('roles.admin') : t('roles.user')}
                        </span>
                        {u.disabled && <span className="badge warn">{t('admin.disabled')}</span>}
                        {u.two_factor && (
                          <span className="badge neutral" title={t('twoFactor.isOn')}>
                            2FA
                          </span>
                        )}
                        {self && <span className="badge neutral">{t('admin.you')}</span>}
                      </strong>
                      <div className="user-usage">
                        <div className="quota-track">
                          <span style={{ width: percent + '%' }} />
                        </div>
                        <small>
                          {t('admin.used', {
                            used: formatBytes(u.used),
                            quota: formatBytes(u.quota),
                          })}
                        </small>
                      </div>
                    </div>
                    <label>
                      {t('admin.quota')}
                      <input
                        aria-label={t('admin.quotaNamed', { name: u.username })}
                        type="number"
                        min="0"
                        max={budget}
                        step="0.01"
                        value={gib(u.quota)}
                        onChange={(e) => updateQuota(u, Math.round(Number(e.target.value) * GIB))}
                      />
                    </label>
                    <button
                      className="secondary"
                      disabled={
                        busy || !Number.isFinite(u.quota) || u.quota < 0 || gib(u.quota) > budget
                      }
                      onClick={() =>
                        void action(async () => {
                          await api('/api/admin/users/' + u.id, 'PATCH', { quota: u.quota });
                        }, message('admin.quotaUpdated'))
                      }
                    >
                      <Save size={14} />
                      {t('common.save')}
                    </button>
                    <button
                      type="button"
                      className="secondary"
                      aria-label={
                        u.role === 'admin'
                          ? t('capability.adminAllHint')
                          : t('capability.openNamed', { name: u.username })
                      }
                      title={u.role === 'admin' ? t('capability.adminAllHint') : undefined}
                      disabled={u.role === 'admin'}
                      onClick={() => setEditing(u.id)}
                    >
                      <Shield size={14} />
                      {u.role === 'admin' ? t('capability.adminAll') : t('capability.open')}
                      {overrideCount(u.id) > 0 && (
                        <span className="override-count">{overrideCount(u.id)}</span>
                      )}
                    </button>
                    <MoreMenu
                      label={t('admin.userActions', { name: u.username })}
                      items={[
                        {
                          label: t('admin.resetPassword'),
                          icon: <KeyRound size={15} />,
                          disabled: busy || self,
                          onSelect: () => {
                            setResetPassword('');
                            setResetting(u);
                          },
                        },
                        ...(u.two_factor
                          ? [
                              {
                                label: t('twoFactor.reset'),
                                icon: <ShieldOff size={15} />,
                                disabled: busy || self,
                                onSelect: () => {
                                  if (
                                    window.confirm(
                                      t('twoFactor.resetConfirm', { name: u.username }),
                                    )
                                  )
                                    void action(async () => {
                                      await api('/api/admin/users/' + u.id, 'PATCH', {
                                        reset_two_factor: true,
                                      });
                                    }, message('twoFactor.resetDone'));
                                },
                              },
                            ]
                          : []),
                        {
                          label: u.disabled ? t('admin.enable') : t('admin.disable'),
                          icon: u.disabled ? <UserCheck size={15} /> : <UserX size={15} />,
                          disabled: busy || self,
                          onSelect: () =>
                            void action(
                              async () => {
                                await api('/api/admin/users/' + u.id, 'PATCH', {
                                  disabled: !u.disabled,
                                });
                              },
                              message(
                                u.disabled ? 'admin.accountEnabled' : 'admin.accountDisabled',
                              ),
                            ),
                        },
                        {
                          label: t('admin.deleteAccount'),
                          icon: <Trash2 size={15} />,
                          danger: true,
                          disabled: busy || self,
                          onSelect: () => setDeleting(u),
                        },
                      ]}
                    />
                  </div>
                );
              })}
            </section>
            <section className="panel">
              <h2>
                <UserPlus size={18} /> {t('admin.newUser')}
              </h2>
              <form
                className="settings-form"
                noValidate
                onSubmit={(e) => {
                  e.preventDefault();
                  const invalid = validateCredentialsMessage(name, password);
                  if (invalid) {
                    setError(invalid);
                    return;
                  }
                  if (!Number.isFinite(quota) || quota < 0 || quota > budget) {
                    setError(message('errors.quota'));
                    return;
                  }
                  void action(async () => {
                    await api('/api/admin/users', 'POST', {
                      username: name,
                      password,
                      quota: Math.round(quota * GIB),
                    });
                    setName('');
                    setPassword('');
                  }, message('admin.userCreated'));
                }}
              >
                <SettingRow
                  id="new-user-name"
                  label={t('admin.username')}
                  hint={t('auth.usernameHint')}
                >
                  <input
                    id="new-user-name"
                    required
                    value={name}
                    autoComplete="off"
                    aria-describedby="new-user-name-hint"
                    onChange={(e) => setName(e.target.value)}
                  />
                </SettingRow>
                <SettingRow
                  id="new-user-password"
                  label={t('admin.password')}
                  hint={t('auth.passwordHint')}
                >
                  <input
                    id="new-user-password"
                    required
                    type="password"
                    autoComplete="new-password"
                    aria-describedby="new-user-password-hint"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </SettingRow>
                <SettingRow
                  id="new-user-quota"
                  label={t('admin.storageQuota')}
                  hint={t('admin.storageQuotaHint')}
                >
                  <input
                    id="new-user-quota"
                    aria-describedby="new-user-quota-hint"
                    required
                    type="number"
                    min="0"
                    max={budget}
                    step="0.01"
                    value={quota}
                    onChange={(e) => setQuota(e.target.value === '' ? NaN : Number(e.target.value))}
                  />
                </SettingRow>
                <div className="setting-actions">
                  <button className="primary" disabled={busy}>
                    <UserPlus size={16} />
                    {t('admin.createUser')}
                  </button>
                </div>
              </form>
            </section>
          </div>
          <div className="admin-section" id="admin-invites">
            <InvitesPanel defaultQuota={policy.default_quota} budget={budget} />
          </div>
          <div className="admin-section" id="admin-limits">
            <section className="panel">
              <h2>
                <Gauge size={18} /> {t('admin.policy')}
              </h2>
              <form
                className="settings-form"
                onSubmit={(e) => {
                  e.preventDefault();
                  void action(async () => {
                    await api('/api/admin/policy', 'PUT', p);
                  }, message('admin.policySaved'));
                }}
              >
                <SettingRow
                  id="policy-registration"
                  label={t('admin.allowRegistration')}
                  hint={t('admin.registrationHint')}
                >
                  <input
                    id="policy-registration"
                    aria-describedby="policy-registration-hint"
                    type="checkbox"
                    className="switch"
                    checked={p.registration_open}
                    disabled={busy}
                    onChange={(e) => setP({ ...p, registration_open: e.target.checked })}
                  />
                </SettingRow>
                <SettingRow
                  id="policy-budget"
                  label={t('admin.storageBudget')}
                  hint={t('admin.storageBudgetHint')}
                >
                  <input
                    id="policy-budget"
                    aria-describedby="policy-budget-hint"
                    required
                    type="number"
                    step="0.5"
                    min="1"
                    value={budget}
                    onChange={(e) =>
                      setP({ ...p, storage_budget: Math.round(Number(e.target.value) * GIB) })
                    }
                  />
                </SettingRow>
                {gibField('default_quota', t('admin.defaultQuota'), t('admin.defaultQuotaHint'), 0)}
                {gibField('max_file', t('admin.maxFile'), t('admin.maxFileHint'))}
                {gibField('max_share', t('admin.maxShare'), t('admin.maxShareHint'))}
                {countField(
                  'files_per_user',
                  t('admin.filesPerUser'),
                  t('admin.filesPerUserHint'),
                  100,
                  p.files_total,
                )}
                {countField('files_total', t('admin.filesTotal'), t('admin.filesTotalHint'), 100)}
                <SettingRow
                  id="policy-days"
                  label={t('admin.defaultDays')}
                  hint={t('admin.defaultDaysHint')}
                >
                  <input
                    id="policy-days"
                    aria-describedby="policy-days-hint"
                    required
                    type="number"
                    min="1"
                    max="365"
                    value={p.default_days}
                    onChange={(e) => setP({ ...p, default_days: Number(e.target.value) })}
                  />
                </SettingRow>
                <div className="setting-actions">
                  <button className="primary" disabled={busy}>
                    <Save size={16} />
                    {t('admin.savePolicy')}
                  </button>
                </div>
              </form>
            </section>
          </div>
          <div className="admin-section" id="admin-permissions">
            <section className="panel admin-wide">
              <h2>
                <Shield size={18} /> {t('capability.title')}
              </h2>
              <p className="muted">{t('capability.description')}</p>
              {defaults && (
                <div className="caps-defaults">
                  {capabilityGroups.map((group) => (
                    <div key={group.label}>
                      <h3 className="caps-group-title">{t(group.label)}</h3>
                      {group.keys.map((key) => (
                        <label className="cap-default" key={key}>
                          <span>{t(capabilityLabels[key])}</span>
                          <input
                            type="checkbox"
                            className="switch"
                            aria-label={t('capability.cap', {
                              cap: t(capabilityLabels[key]),
                              user: 'default',
                            })}
                            checked={defaults[key]}
                            disabled={busy}
                            onChange={(e) => void saveDefault(key, e.target.checked)}
                          />
                        </label>
                      ))}
                    </div>
                  ))}
                </div>
              )}
              <p className="muted caps-legend">{t('capability.autosave')}</p>
            </section>
          </div>
          {currentUser && (
            <div className="admin-section" id="admin-email">
              <EmailSettingsPanel adminEmail={currentEmail} />
            </div>
          )}
          <div className="admin-section" id="admin-push">
            <PushAdminPanel />
          </div>
          <div className="admin-section" id="admin-storage">
            <section className="panel admin-wide">
              <h2>
                <HardDrive size={18} /> {t('admin.storage')}
              </h2>
              {storage ? (
                <>
                  <div className="storage-meter">
                    <div className="quota-track">
                      <span
                        style={{
                          width:
                            Math.min(
                              100,
                              (storage.reserved_bytes / Math.max(1, storage.capacity_bytes)) * 100,
                            ) + '%',
                        }}
                      />
                    </div>
                  </div>
                  <div className="storage-detail">
                    <span>{t('admin.budget')}</span>
                    <strong>
                      {formatBytes(storage.reserved_bytes)} / {formatBytes(storage.capacity_bytes)}
                    </strong>
                  </div>
                  <div className="storage-detail">
                    <span>{t('admin.diskFree')}</span>
                    <strong>{formatBytes(storage.disk_free_bytes)}</strong>
                  </div>
                  <div className="storage-detail">
                    <span>{t('admin.backend')}</span>
                    <strong>
                      {storage.locations?.find((l) => l.active && l.id !== 'local')?.path ??
                        t('admin.localDisk')}
                    </strong>
                  </div>
                  <div className="storage-detail">
                    <span>{t('admin.metadata')}</span>
                    <strong>{t('admin.metadataStorage')}</strong>
                  </div>
                </>
              ) : (
                <p className="muted">{t('admin.loadingStorage')}</p>
              )}
              <p className="muted budget-note">{t('admin.budgetNote')}</p>
            </section>
            <StoragePanel changed={() => void load()} />
            <UnclaimedPanel changed={() => void load()} />
          </div>
          <div className="admin-section" id="admin-backups">
            <BackupPanel />
          </div>
          <div className="admin-section" id="admin-antivirus">
            <AntivirusPanel />
          </div>
          <div className="admin-section" id="admin-branding">
            <BrandingPanel changed={reload} />
          </div>
          {currentUser && (
            <div className="admin-section" id="admin-updates">
              <UpdatePanel initial={updateInfo} />
            </div>
          )}
          <div className="admin-section" id="admin-activity">
            <ActivityPanel />
          </div>
        </div>
      </div>
      {resetting && (
        <Modal
          title={t('admin.resetPasswordTitle', { name: resetting.username })}
          close={() => setResetting(null)}
        >
          <form
            className="form-stack"
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              const bytes = new TextEncoder().encode(resetPassword).length;
              if (bytes < 12 || bytes > 72) {
                setError(message('errors.invalid_password'));
                return;
              }
              void action(async () => {
                await api('/api/admin/users/' + resetting.id, 'PATCH', {
                  password: resetPassword,
                });
                setResetting(null);
              }, message('admin.passwordReset'));
            }}
          >
            <p className="muted">{t('admin.resetPasswordHint')}</p>
            <div>
              <label>
                {t('admin.password')}
                <input
                  type="password"
                  autoComplete="new-password"
                  value={resetPassword}
                  onChange={(e) => setResetPassword(e.target.value)}
                />
              </label>
              <small className="field-help">{t('auth.passwordHint')}</small>
            </div>
            {error && (
              <div role="alert" className="error">
                {error}
              </div>
            )}
            <div className="dialog-actions">
              <button type="button" className="secondary" onClick={() => setResetting(null)}>
                {t('common.cancel')}
              </button>
              <button className="primary" disabled={busy || !resetPassword}>
                <KeyRound size={16} />
                {t('admin.resetPassword')}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {deleting && (
        <Modal
          title={t('admin.deleteAccountTitle', { name: deleting.username })}
          close={() => setDeleting(null)}
        >
          <p>{t('admin.deleteAccountWarning')}</p>
          {error && (
            <div role="alert" className="error">
              {error}
            </div>
          )}
          <div className="dialog-actions">
            <button className="secondary" onClick={() => setDeleting(null)}>
              {t('common.cancel')}
            </button>
            <button
              className="primary danger-solid"
              disabled={busy}
              onClick={() =>
                void action(async () => {
                  await api('/api/admin/users/' + deleting.id, 'DELETE', {});
                  setDeleting(null);
                }, message('admin.accountDeleted'))
              }
            >
              <Trash2 size={16} />
              {t('admin.deleteAccount')}
            </button>
          </div>
        </Modal>
      )}
      {editingUser && defaults && (
        <Modal
          title={t('capability.userTitle', { user: editingUser.username })}
          close={() => setEditing('')}
          className="caps-dialog"
        >
          <p>
            {t('capability.legend')} {t('capability.autosave')}
          </p>
          {error && (
            <div role="alert" className="error">
              {error}
            </div>
          )}
          {capabilityGroups.map((group) => (
            <div key={group.label}>
              <h3 className="caps-group-title">{t(group.label)}</h3>
              {group.keys.map((key) => {
                const value = overrideValue(editingUser, key, defaults, overrides);
                const label = t('capability.cap', {
                  cap: t(capabilityLabels[key]),
                  user: editingUser.username,
                });
                return (
                  <div className="cap-row" key={key}>
                    <span>{t(capabilityLabels[key])}</span>
                    <div role="radiogroup" aria-label={label} className="segmented">
                      {(['inherit', 'on', 'off'] as const).map((option) => (
                        <label
                          key={option}
                          className={option + (value === option ? ' checked' : '')}
                        >
                          <input
                            type="radio"
                            name={'cap-' + key}
                            value={option}
                            checked={value === option}
                            disabled={busy}
                            onChange={() => void saveOverride(editingUser, key, option)}
                          />
                          {option === 'inherit'
                            ? defaults[key]
                              ? t('capability.inheritOn')
                              : t('capability.inheritOff')
                            : option === 'on'
                              ? t('capability.allow')
                              : t('capability.deny')}
                        </label>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          ))}
        </Modal>
      )}
    </>
  );
}
