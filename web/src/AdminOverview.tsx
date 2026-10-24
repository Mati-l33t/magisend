import { useEffect, useState } from 'react';
import { Activity, ChevronDown, ChevronUp, LoaderCircle } from 'lucide-react';
import { api, formatBytes } from './api';
import { Select } from './Select';
import { t, useLanguage, useMessage, formatDate, formatNumber } from './i18n';

interface Overview {
  users: number;
  disabled: number;
  two_factor: number;
  active_links: number;
  open_requests: number;
  files: number;
  used: number;
  budget: number;
  disk_free?: number;
  downloads: number;
  failed_logins: number;
  uploads_today: number;
  last_download: number;
  email_enabled: boolean;
  public_address: boolean;
  storage_offline?: string[];
}

// The administrator's dashboard: what is stored and shared at a glance,
// plus hints for settings that are still missing.
export function OverviewPanel() {
  useLanguage();
  const [o, setO] = useState<Overview | null>(null);
  const [error, setError] = useMessage();
  useEffect(() => {
    api<Overview>('/api/admin/overview').then(
      (data) => setO(typeof data?.users === 'number' ? data : null),
      (e) => setError(e as Error),
    );
  }, []);
  if (!o)
    return error ? (
      <div role="alert" className="error admin-wide">
        {error}
      </div>
    ) : null;
  const percent = o.budget ? Math.min(100, (o.used / o.budget) * 100) : 0;
  const stats: { label: string; value: string; note?: string; warn?: boolean }[] = [
    {
      label: t('overview.accounts'),
      value: formatNumber(o.users),
      note: t('overview.accountsNote', {
        disabled: formatNumber(o.disabled),
        twoFactor: formatNumber(o.two_factor),
      }),
    },
    {
      label: t('overview.links'),
      value: formatNumber(o.active_links),
      note: t('overview.requests', { count: formatNumber(o.open_requests) }),
    },
    {
      label: t('overview.files'),
      value: formatNumber(o.files),
      note: t('overview.today', { count: formatNumber(o.uploads_today) }),
    },
    {
      label: t('overview.downloads'),
      value: formatNumber(o.downloads),
      note: o.last_download
        ? t('overview.lastDownload', { date: formatDate(o.last_download * 1000) })
        : undefined,
    },
    {
      label: t('overview.failedLogins'),
      value: formatNumber(o.failed_logins),
      note: t('overview.last24h'),
      warn: o.failed_logins >= 10,
    },
  ];
  return (
    <section className="panel admin-wide overview">
      <div className="overview-grid">
        {stats.map((s) => (
          <div className={'overview-stat' + (s.warn ? ' warn' : '')} key={s.label}>
            <span>{s.label}</span>
            <strong>{s.value}</strong>
            {s.note && <small>{s.note}</small>}
          </div>
        ))}
        <div className="overview-stat storage">
          <span>{t('overview.storage')}</span>
          <strong>{formatBytes(o.used)}</strong>
          <div className="quota-track">
            <span style={{ width: percent + '%' }} />
          </div>
          <small>{t('overview.ofBudget', { budget: formatBytes(o.budget) })}</small>
          {o.disk_free !== undefined && (
            <small>{t('overview.diskFree', { free: formatBytes(o.disk_free) })}</small>
          )}
        </div>
      </div>
      {!!o.storage_offline?.length && (
        <div role="alert" className="error">
          {t('overview.storageOffline', { paths: o.storage_offline.join(', ') })}
        </div>
      )}
      {(!o.email_enabled || !o.public_address) && (
        <ul className="overview-hints">
          {!o.email_enabled && <li>{t('overview.hintEmail')}</li>}
          {!o.public_address && <li>{t('overview.hintHttps')}</li>}
        </ul>
      )}
    </section>
  );
}

interface AuditEvent {
  id: number;
  at: number;
  actor: string;
  action: string;
  target: string;
  ip: string;
}

const filters = [
  '',
  'login',
  'account',
  'password',
  'twofactor',
  'share',
  'request',
  'settings',
  'storage',
];

// The newest few are shown; more load on request.
const activityStep = 15;

export function ActivityPanel() {
  useLanguage();
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [filter, setFilter] = useState('');
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  async function load(before?: number) {
    setBusy(true);
    try {
      const page = await api<AuditEvent[]>(
        '/api/admin/activity?' +
          new URLSearchParams({
            ...(filter ? { action: filter } : {}),
            ...(before ? { before: String(before) } : {}),
            limit: String(activityStep),
          }),
      );
      const list = Array.isArray(page) ? page : [];
      setEvents((current) => (before ? [...current, ...list] : list));
      setMore(list.length === activityStep);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, [filter]);
  const label = (action: string) => {
    const key = 'audit.' + action;
    const text = t(key as Parameters<typeof t>[0]);
    return text === key ? action : text;
  };
  return (
    <section className="panel admin-wide">
      <div className="panel-heading-row">
        <h2>
          <Activity size={18} /> {t('audit.title')}
        </h2>
        <Select
          ariaLabel={t('audit.filter')}
          className="activity-filter"
          value={filter}
          onChange={setFilter}
          options={filters.map((f) => ({
            value: f,
            label: t(('audit.filter.' + (f || 'all')) as Parameters<typeof t>[0]),
          }))}
        />
      </div>
      <p className="muted panel-intro">{t('audit.intro')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {events.length === 0 && !busy ? (
        <p className="muted">{t('audit.empty')}</p>
      ) : (
        <ol className="activity-list">
          {events.map((ev) => (
            <li key={ev.id} className={ev.action.endsWith('failed') ? 'warn' : undefined}>
              <time dateTime={new Date(ev.at * 1000).toISOString()}>
                {formatDate(ev.at * 1000, true)}
              </time>
              <span>
                <strong>{ev.actor || t('audit.visitor')}</strong> {label(ev.action)}
                {ev.target && <span className="activity-target"> {ev.target}</span>}
              </span>
              <span className="activity-ip">{ev.ip}</span>
            </li>
          ))}
        </ol>
      )}
      {busy && <LoaderCircle className="spin" size={18} />}
      {!busy && (more || events.length > activityStep) && (
        <div className="show-more">
          {more && (
            <button className="secondary" onClick={() => void load(events[events.length - 1]?.id)}>
              <ChevronDown size={16} />
              {t('audit.more')}
            </button>
          )}
          {events.length > activityStep && (
            <button
              className="quiet"
              onClick={() => {
                setEvents((current) => current.slice(0, activityStep));
                setMore(true);
              }}
            >
              <ChevronUp size={16} />
              {t('audit.less')}
            </button>
          )}
        </div>
      )}
    </section>
  );
}
