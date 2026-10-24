import { useEffect, useRef, useState } from 'react';
import {
  Bell,
  ChevronDown,
  KeyRound,
  LogOut,
  LoaderCircle,
  Mail,
  ShieldCheck,
  Terminal,
  UserRound,
} from 'lucide-react';
import { AccountEmailDialog } from './Email';
import { PushDialog } from './Push';
import { TwoFactorDialog } from './TwoFactor';
import { ApiTokensDialog } from './ApiTokens';
import { api, type Me } from './api';
import { Modal } from './Modal';
import { Avatar, ProfileDialog, displayName } from './Profile';
import { t, useLanguage, useMessage, message } from './i18n';

export default function AccountMenu({
  me,
  logout,
  reload,
}: {
  me: Me;
  logout: () => Promise<void>;
  reload: () => Promise<void>;
}) {
  useLanguage();
  const [open, setOpen] = useState(false);
  const [changing, setChanging] = useState(false);
  const [editingEmail, setEditingEmail] = useState(false);
  const [securing, setSecuring] = useState(false);
  const [tokens, setTokens] = useState(false);
  const [pushing, setPushing] = useState(false);
  const [profile, setProfile] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    function outside(event: MouseEvent) {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    }
    function escape(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', outside);
    document.addEventListener('keydown', escape);
    return () => {
      document.removeEventListener('mousedown', outside);
      document.removeEventListener('keydown', escape);
    };
  }, [open]);
  return (
    <div className="menu" ref={root}>
      <button
        className="account-button"
        aria-label={t('account.menu')}
        aria-haspopup="true"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <Avatar user={me.user} />
        <span className="account-name">{displayName(me.user)}</span>
        <ChevronDown size={15} />
      </button>
      {open && (
        <div className="menu-list">
          <div className="menu-header with-avatar">
            <Avatar user={me.user} size={40} />
            <span>
              <strong>{displayName(me.user)}</strong>
              <small>
                {me.user.display_name ? me.user.username + ' · ' : ''}
                {me.user.role === 'admin' ? t('roles.admin') : t('roles.user')}
                {me.user.email ? ' · ' + me.user.email : ''}
              </small>
            </span>
          </div>
          <button
            onClick={() => {
              setOpen(false);
              setProfile(true);
            }}
          >
            <UserRound size={16} />
            {t('profile.menu')}
          </button>
          <button
            onClick={() => {
              setOpen(false);
              setEditingEmail(true);
            }}
          >
            <Mail size={16} />
            {me.user.email ? t('email.accountChange') : t('email.accountAdd')}
          </button>
          <button
            onClick={() => {
              setOpen(false);
              setChanging(true);
            }}
          >
            <KeyRound size={16} />
            {t('account.changePassword')}
          </button>
          <button
            onClick={() => {
              setOpen(false);
              setSecuring(true);
            }}
          >
            <ShieldCheck size={16} />
            {t('twoFactor.title')}
            {me.user.two_factor && <span className="menu-badge">{t('twoFactor.onShort')}</span>}
          </button>
          <button
            onClick={() => {
              setOpen(false);
              setTokens(true);
            }}
          >
            <Terminal size={16} />
            {t('tokens.title')}
          </button>
          <button
            onClick={() => {
              setOpen(false);
              setPushing(true);
            }}
          >
            <Bell size={16} />
            {t('push.menu')}
          </button>
          <button className="danger" onClick={() => void logout()}>
            <LogOut size={16} />
            {t('common.logout')}
          </button>
        </div>
      )}
      {changing && <ChangePassword close={() => setChanging(false)} />}
      {profile && <ProfileDialog user={me.user} close={() => setProfile(false)} saved={reload} />}
      {tokens && <ApiTokensDialog close={() => setTokens(false)} />}
      {pushing && <PushDialog close={() => setPushing(false)} />}
      {securing && (
        <TwoFactorDialog
          close={() => setSecuring(false)}
          changed={() => void reload().catch(() => {})}
        />
      )}
      {editingEmail && (
        <AccountEmailDialog
          current={me.user.email || ''}
          remind={me.user.remind_expiry !== false}
          close={() => setEditingEmail(false)}
          saved={() => void reload().catch(() => {})}
        />
      )}
    </div>
  );
}

function ChangePassword({ close }: { close: () => void }) {
  useLanguage();
  const [current, setCurrent] = useState('');
  const [password, setPassword] = useState('');
  const [repeated, setRepeated] = useState('');
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useMessage();
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    const bytes = new TextEncoder().encode(password).length;
    if (bytes < 12 || bytes > 72) {
      setError(message('errors.invalid_password'));
      return;
    }
    if (password !== repeated) {
      setError(message('auth.passwordMismatch'));
      return;
    }
    setBusy(true);
    try {
      await api('/api/account/password', 'POST', { current, password });
      setDone(true);
      setCurrent('');
      setPassword('');
      setRepeated('');
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t('account.changePassword')} close={close}>
      {done ? (
        <>
          <p className="success-note" role="status">
            {t('account.passwordChanged')}
          </p>
          <div className="dialog-actions">
            <button className="primary" onClick={close}>
              {t('common.close')}
            </button>
          </div>
        </>
      ) : (
        <form className="form-stack" onSubmit={submit} noValidate>
          <label>
            {t('account.currentPassword')}
            <input
              type="password"
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </label>
          <div>
            <label>
              {t('account.newPassword')}
              <input
                type="password"
                autoComplete="new-password"
                aria-describedby="new-password-help"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            <small id="new-password-help" className="field-help">
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
          <div className="dialog-actions">
            <button type="button" className="secondary" onClick={close}>
              {t('common.cancel')}
            </button>
            <button className="primary" disabled={busy || !current || !password}>
              {busy && <LoaderCircle className="spin" size={16} />}
              {t('account.changePassword')}
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}
