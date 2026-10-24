import { useState, useEffect } from 'react';
import { Select } from './Select';
import { ScrollTop } from './SettingRow';
import { HelpDialog, type HelpPage } from './Help';
import {
  Sun,
  Moon,
  ShieldCheck,
  LoaderCircle,
  Settings,
  ArrowUpRight,
  CircleHelp,
  Search,
} from 'lucide-react';
import {
  t,
  useLanguage,
  initializeLanguage,
  languages,
  getLanguageChoice,
  getLanguage,
  setLanguage,
  validateCredentialsMessage,
  useMessage,
  message,
} from './i18n';
import { api, ApiError, type Me } from './api';
import Workspace from './Workspace';
import { Modal } from './Modal';
import AccountMenu from './AccountMenu';
import { openSearchEvent, searchShortcut } from './SearchPalette';
import PublicShare from './PublicShare';
import RequestPage from './RequestPage';
import { InvitePage, ResetPage } from './AccountLinks';
import { applyBranding, brandLogo, brandName, customBrand, type Branding } from './branding';
import { applyLook, useLook } from './look';
import LookPicker from './LookPicker';
export default function App() {
  useState(() => {
    initializeLanguage();
    return null;
  });
  useLanguage();
  const [settings, setSettings] = useState(false);
  const [help, setHelp] = useState<HelpPage | null>(null);
  const [theme, setTheme] = useState(() => {
    try {
      return localStorage.getItem('magisend-theme') === 'light' ? 'light' : 'dark';
    } catch {
      return 'dark';
    }
  });
  const look = useLook();
  const [status, setStatus] = useState<{
    setup_required: boolean;
    registration_open: boolean;
    version?: string;
    password_reset?: boolean;
    branding?: Branding;
  } | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useMessage();
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem('magisend-theme', theme);
    } catch {}
  }, [theme]);
  async function reload() {
    try {
      const [s, u] = await Promise.all([
        api<{
          setup_required: boolean;
          registration_open: boolean;
          version?: string;
          password_reset?: boolean;
          branding?: Branding;
        }>('/api/status'),
        api<Me>('/api/me').catch((e) => {
          if (e instanceof ApiError && e.status === 401) {
            setMe(null);
            return null;
          }
          throw e;
        }),
      ]);
      applyBranding(s.branding);
      setStatus(s);
      setMe(u);
      setError('');
    } catch (e) {
      setError(e as Error);
      throw e;
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void reload().catch(() => {});
  }, []);
  const requestId = location.pathname.match(/^\/r\/([a-f0-9]{48})\/?$/)?.[1];
  const emailLink = location.pathname.match(/^\/(invite|reset)\/([a-f0-9]{48})\/?$/);
  const publicId = location.pathname.match(
    /^\/s\/([a-f0-9]{48}|[a-z0-9][a-z0-9-]{1,46}[a-z0-9])\/?$/,
  )?.[1];
  // The help shown is about the page on screen when it is opened.
  function helpPage(): HelpPage {
    if (publicId) return 'public';
    if (requestId) return 'request';
    if (!me || emailLink) return 'signin';
    const section = location.pathname.split('/')[1];
    return section === 'library' || section === 'shares' || section === 'admin' ? section : 'send';
  }
  return (
    <>
      <header className="topbar">
        <a className="brand" href="/">
          <img src={brandLogo()} alt="" />
          <span className="brand-name">
            {brandName()}
            {!customBrand() && <small>{t('brand.headerByline')}</small>}
          </span>
        </a>
        <div className="top-actions">
          <span className="edition">{t('app.edition')}</span>
          {me && !publicId && !requestId && !emailLink && (
            <button
              className="search-button"
              onClick={() => dispatchEvent(new Event(openSearchEvent))}
              aria-label={t('search.open')}
              title={t('search.open') + ' (' + searchShortcut + ')'}
            >
              <Search size={17} />
              <span className="search-button-text">{t('search.open')}</span>
              <kbd>{searchShortcut}</kbd>
            </button>
          )}
          <button
            className="icon-button"
            onClick={() => setHelp(helpPage())}
            aria-label={t('help.open')}
            title={t('help.open')}
          >
            <CircleHelp size={19} />
          </button>
          <button
            className="icon-button"
            onClick={() => setSettings(true)}
            aria-label={t('common.settings')}
          >
            <Settings size={19} />
          </button>
          <button
            className="icon-button"
            onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
            aria-label={theme === 'dark' ? t('theme.light') : t('theme.dark')}
          >
            {theme === 'dark' ? <Sun size={19} /> : <Moon size={19} />}
          </button>
          {me && !publicId && !requestId && !emailLink && (
            <AccountMenu
              me={me}
              reload={reload}
              logout={async () => {
                try {
                  await api('/api/logout', 'POST', {});
                  history.replaceState(null, '', '/');
                  setMe(null);
                  await reload();
                } catch (e) {
                  setError(e as Error);
                }
              }}
            />
          )}
        </div>
      </header>
      {emailLink ? (
        emailLink[1] === 'invite' ? (
          <InvitePage token={emailLink[2]} />
        ) : (
          <ResetPage token={emailLink[2]} />
        )
      ) : requestId ? (
        <RequestPage id={requestId} />
      ) : publicId ? (
        <PublicShare id={publicId} />
      ) : loading ? (
        <main className="loading">
          <LoaderCircle className="spin" />
          {t('app.loading')}
        </main>
      ) : me ? (
        <Workspace me={me} reload={reload} />
      ) : status ? (
        <Login
          setup={status.setup_required}
          registration={status.registration_open}
          passwordReset={!!status.password_reset}
          done={reload}
        />
      ) : (
        <main className="loading">{t('app.connectionFailed')}</main>
      )}
      {help && <HelpDialog page={help} close={() => setHelp(null)} />}
      {settings && (
        <Modal
          title={t('settings.title')}
          close={() => setSettings(false)}
          className="settings-dialog"
        >
          <Select
            label={t('settings.language')}
            value={getLanguageChoice()}
            onChange={(choice) => setLanguage(choice)}
            options={[
              { value: 'automatic', label: t('settings.automatic') },
              ...languages.map((language) => ({ value: language.code, label: language.name })),
            ]}
          />
          <LookPicker
            value={look}
            onChange={(next) => {
              // Studio is designed light-first.
              if (next === 'studio' && look !== 'studio') setTheme('light');
              applyLook(next);
            }}
          />
        </Modal>
      )}
      {error && (
        <div role="alert" className="toast error">
          {error}
          <button onClick={() => void reload().catch(() => {})}>{t('common.retry')}</button>
        </div>
      )}
      <ScrollTop />
      <footer className="footer">
        <span>
          {t('app.footerSelfHosted')}
          {status?.version && <span className="mono">{status.version}</span>}
        </span>
        <span>
          {t('app.footer')}
          <ArrowUpRight size={13} />
        </span>
      </footer>
    </>
  );
}
function Login({
  setup: setupRequired,
  registration,
  passwordReset,
  done,
}: {
  setup: boolean;
  registration: boolean;
  passwordReset: boolean;
  done: () => Promise<void>;
}) {
  useLanguage();
  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [forgot, setForgot] = useState(false);
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [repeatedPassword, setRepeatedPassword] = useState('');
  const [email, setEmail] = useState('');
  // Set once the password was accepted for an account with two-step login.
  const [needCode, setNeedCode] = useState(false);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [created, setCreated] = useState(false);
  const setup = setupRequired && !created;
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    if (setup || mode === 'register') {
      const invalid = validateCredentialsMessage(name, password);
      if (invalid) {
        setError(invalid);
        return;
      }
      if (password !== repeatedPassword) {
        setError(message('auth.passwordMismatch'));
        return;
      }
    }
    setBusy(true);
    const credentials = { username: name, password };
    // Optional, for notifications; never sent with a login.
    const account = email.trim() ? { ...credentials, email: email.trim() } : credentials;
    try {
      if (setup) {
        await api('/api/setup', 'POST', account);
        setCreated(true);
        setPassword('');
        setRepeatedPassword('');
        setMode('login');
        await done().catch(() => {});
      } else if (mode === 'register') {
        await api('/api/register', 'POST', account);
        setCreated(true);
        setPassword('');
        setRepeatedPassword('');
        setMode('login');
        await done().catch(() => {});
      }
      await api('/api/login', 'POST', needCode ? { ...credentials, code } : credentials);
      setPassword('');
      setRepeatedPassword('');
      setCode('');
      setNeedCode(false);
      await done().catch(() => {});
    } catch (e) {
      if (e instanceof ApiError && e.code === 'two_factor_required') {
        setNeedCode(true);
        return;
      }
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="auth-layout">
      <section className="auth-art">
        <div className="eyebrow">{t('auth.eyebrow')}</div>
        <h1>
          {t('auth.heroFirst')}
          <br />
          <span>{t('auth.heroSecond')}</span>
        </h1>
        <p>{t('auth.heroDescription')}</p>
        <ol className="hero-points">
          {(['auth.point1', 'auth.point2', 'auth.point3'] as const).map((key, i) => (
            <li key={key}>
              <span>{String(i + 1).padStart(2, '0')}</span>
              {t(key)}
            </li>
          ))}
        </ol>
        <div className="art-bottom">
          <ShieldCheck size={16} /> {t('auth.selfHosted')}
        </div>
      </section>
      <section className="auth-form-wrap">
        {forgot ? (
          <ForgotPassword back={() => setForgot(false)} />
        ) : (
          <form className="auth-form" onSubmit={submit} noValidate={setup || mode === 'register'}>
            <div className="eyebrow">
              {setup
                ? t('auth.setupStep')
                : mode === 'register'
                  ? t('auth.registerStep')
                  : t('auth.loginStep')}
            </div>
            <h2>
              {setup
                ? t('auth.setupTitle')
                : mode === 'register'
                  ? t('auth.registerTitle')
                  : t('auth.loginTitle')}
            </h2>
            <p className="muted">
              {setup
                ? t('auth.setupDescription')
                : mode === 'register'
                  ? t('auth.registerDescription')
                  : t('auth.loginDescription')}
            </p>
            <label>
              {t('auth.username')}
              <input
                autoComplete="username"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                aria-describedby={setup || mode === 'register' ? 'username-help' : undefined}
                placeholder={t('auth.usernamePlaceholder')}
              />
            </label>
            {(setup || mode === 'register') && (
              <small id="username-help">{t('auth.usernameHint')}</small>
            )}
            {(setup || mode === 'register') && (
              <>
                <label>
                  {t('email.address')} <span className="optional">{t('share.optional')}</span>
                  <input
                    type="email"
                    autoComplete="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    aria-describedby="email-help"
                    placeholder="name@example.com"
                  />
                </label>
                <small id="email-help">{t('auth.emailHint')}</small>
              </>
            )}
            <label>
              {t('auth.password')}
              <input
                type="password"
                autoComplete={setup || mode === 'register' ? 'new-password' : 'current-password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                aria-describedby={setup || mode === 'register' ? 'password-help' : undefined}
                placeholder={t('auth.passwordPlaceholder')}
              />
            </label>
            {(setup || mode === 'register') && (
              <small id="password-help">{t('auth.passwordHint')}</small>
            )}
            {needCode && !setup && mode !== 'register' && (
              <>
                <label>
                  {t('auth.code')}
                  <input
                    className="mono code-input"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    autoFocus
                    maxLength={16}
                    value={code}
                    aria-describedby="code-help"
                    onChange={(e) => setCode(e.target.value)}
                  />
                </label>
                <small id="code-help">{t('auth.codeHint')}</small>
              </>
            )}
            {setup || mode === 'register' ? (
              <label>
                {t('auth.repeatPassword')}
                <input
                  type="password"
                  autoComplete="new-password"
                  value={repeatedPassword}
                  onChange={(e) => setRepeatedPassword(e.target.value)}
                  required
                />
              </label>
            ) : null}
            {error && (
              <div role="alert" className="error">
                {error}
              </div>
            )}
            <button className="primary full" disabled={busy}>
              {busy ? <LoaderCircle className="spin" size={18} /> : null}
              {setup
                ? t('auth.createAdmin')
                : mode === 'register'
                  ? t('auth.createAccount')
                  : t('auth.login')}
              <ArrowUpRight size={18} />
            </button>
            {!setup && mode === 'login' && passwordReset && (
              <button className="text-button" type="button" onClick={() => setForgot(true)}>
                {t('reset.forgot')}
              </button>
            )}
            {!setup && registration && (
              <button
                className="text-button"
                type="button"
                onClick={() => {
                  setMode(mode === 'login' ? 'register' : 'login');
                  setPassword('');
                  setRepeatedPassword('');
                  setError('');
                }}
              >
                {mode === 'login' ? t('auth.createAccount') : t('auth.backToLogin')}
              </button>
            )}
            <div className="auth-note">
              <span className="status-dot" />
              {setup
                ? t('auth.registrationInitiallyClosed')
                : registration
                  ? t('auth.registrationOpen')
                  : t('auth.registrationClosed')}
            </div>
          </form>
        )}
      </section>
    </main>
  );
}

// Asks for a reset link. The answer never reveals whether an account exists.
function ForgotPassword({ back }: { back: () => void }) {
  useLanguage();
  const [login, setLogin] = useState('');
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useMessage();
  async function send(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api('/api/password/forgot', 'POST', { login, language: getLanguage() });
      setSent(true);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="auth-form" onSubmit={(e) => void send(e)}>
      <div className="eyebrow">{t('reset.eyebrow')}</div>
      <h2>{t('reset.forgotTitle')}</h2>
      <p className="muted">{t('reset.forgotIntro')}</p>
      {sent ? (
        <p className="success-note" role="status">
          {t('reset.sent')}
        </p>
      ) : (
        <>
          <label>
            {t('reset.login')}
            <input
              autoComplete="username"
              required
              value={login}
              onChange={(e) => setLogin(e.target.value)}
            />
          </label>
          {error && (
            <div role="alert" className="error">
              {error}
            </div>
          )}
          <button className="primary full" disabled={busy || !login.trim()}>
            {busy ? <LoaderCircle className="spin" size={18} /> : null}
            {t('reset.send')}
          </button>
        </>
      )}
      <button className="text-button" type="button" onClick={back}>
        {t('auth.backToLogin')}
      </button>
    </form>
  );
}
