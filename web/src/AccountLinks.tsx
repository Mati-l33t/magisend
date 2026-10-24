import { useEffect, useState } from 'react';
import { ArrowUpRight, KeyRound, LoaderCircle, UserPlus } from 'lucide-react';
import { api, ApiError } from './api';
import { t, useLanguage, useMessage, message, validateCredentialsMessage } from './i18n';

// Pages opened from emails: accepting an invitation and choosing a new
// password. Both lead back to the sign-in page when done.
export function InvitePage({ token }: { token: string }) {
  useLanguage();
  const [email, setEmail] = useState<string | null>(null);
  const [invalid, setInvalid] = useState(false);
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [repeated, setRepeated] = useState('');
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useMessage();
  useEffect(() => {
    api<{ email: string }>('/api/invites/' + token).then(
      (invite) => setEmail(invite.email),
      (e) => {
        setInvalid(e instanceof ApiError && e.status === 404);
        setError(e as Error);
      },
    );
  }, [token]);
  async function accept(event: React.FormEvent) {
    event.preventDefault();
    const invalidInput = validateCredentialsMessage(name, password);
    if (invalidInput) return setError(invalidInput);
    if (password !== repeated) return setError(message('auth.passwordMismatch'));
    setBusy(true);
    setError('');
    try {
      await api('/api/invites/' + token, 'POST', { username: name, password });
      await api('/api/login', 'POST', { username: name, password });
      setDone(true);
      location.replace('/send');
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="public-page account-link">
      <div className="eyebrow">{t('invite.eyebrow')}</div>
      <section className="panel unlock-panel">
        <div className="upload-symbol">
          <UserPlus size={26} />
        </div>
        <h1>{t('invite.title')}</h1>
        {invalid ? (
          <>
            <p className="muted">{t('errors.invite_invalid')}</p>
            <a className="secondary" href="/">
              {t('public.home')}
            </a>
          </>
        ) : email === null ? (
          error ? (
            <div role="alert" className="error">
              {error}
            </div>
          ) : (
            <LoaderCircle className="spin" />
          )
        ) : (
          <form className="form-stack" onSubmit={(e) => void accept(e)} noValidate>
            <p className="muted">{t('invite.intro', { email })}</p>
            <div className="field-with-hint">
              <label>
                {t('auth.username')}
                <input
                  autoComplete="username"
                  value={name}
                  aria-describedby="invite-username-help"
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <small id="invite-username-help" className="field-hint">
                {t('auth.usernameHint')}
              </small>
            </div>
            <div className="field-with-hint">
              <label>
                {t('auth.password')}
                <input
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  aria-describedby="invite-password-help"
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <small id="invite-password-help" className="field-hint">
                {t('auth.passwordHint')}
              </small>
            </div>
            <label>
              {t('auth.repeatPassword')}
              <input
                type="password"
                autoComplete="new-password"
                value={repeated}
                onChange={(e) => setRepeated(e.target.value)}
              />
            </label>
            {error && (
              <div role="alert" className="error">
                {error}
              </div>
            )}
            <button className="primary full" disabled={busy || done}>
              {busy ? <LoaderCircle className="spin" size={16} /> : null}
              {t('auth.createAccount')}
              <ArrowUpRight size={16} />
            </button>
          </form>
        )}
      </section>
    </main>
  );
}

export function ResetPage({ token }: { token: string }) {
  useLanguage();
  const [user, setUser] = useState<string | null>(null);
  const [invalid, setInvalid] = useState(false);
  const [password, setPassword] = useState('');
  const [repeated, setRepeated] = useState('');
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useMessage();
  useEffect(() => {
    api<{ username: string }>('/api/password/reset/' + token).then(
      (reset) => setUser(reset.username),
      (e) => {
        setInvalid(e instanceof ApiError && e.status === 404);
        setError(e as Error);
      },
    );
  }, [token]);
  async function save(event: React.FormEvent) {
    event.preventDefault();
    const invalidInput = validateCredentialsMessage(user || 'user', password);
    if (invalidInput) return setError(invalidInput);
    if (password !== repeated) return setError(message('auth.passwordMismatch'));
    setBusy(true);
    setError('');
    try {
      await api('/api/password/reset/' + token, 'POST', { password });
      setDone(true);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="public-page account-link">
      <div className="eyebrow">{t('reset.eyebrow')}</div>
      <section className="panel unlock-panel">
        <div className="upload-symbol">
          <KeyRound size={26} />
        </div>
        <h1>{t('reset.title')}</h1>
        {done ? (
          <>
            <p className="success-note" role="status">
              {t('reset.done')}
            </p>
            <a className="primary full" href="/">
              {t('auth.login')}
            </a>
          </>
        ) : invalid ? (
          <>
            <p className="muted">{t('errors.reset_invalid')}</p>
            <a className="secondary" href="/">
              {t('public.home')}
            </a>
          </>
        ) : user === null ? (
          error ? (
            <div role="alert" className="error">
              {error}
            </div>
          ) : (
            <LoaderCircle className="spin" />
          )
        ) : (
          <form className="form-stack" onSubmit={(e) => void save(e)} noValidate>
            <p className="muted">{t('reset.intro', { user })}</p>
            <div className="field-with-hint">
              <label>
                {t('account.newPassword')}
                <input
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  aria-describedby="reset-password-help"
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <small id="reset-password-help" className="field-hint">
                {t('auth.passwordHint')}
              </small>
            </div>
            <label>
              {t('auth.repeatPassword')}
              <input
                type="password"
                autoComplete="new-password"
                value={repeated}
                onChange={(e) => setRepeated(e.target.value)}
              />
            </label>
            {error && (
              <div role="alert" className="error">
                {error}
              </div>
            )}
            <button className="primary full" disabled={busy}>
              {busy ? <LoaderCircle className="spin" size={16} /> : null}
              {t('reset.save')}
            </button>
          </form>
        )}
      </section>
    </main>
  );
}
