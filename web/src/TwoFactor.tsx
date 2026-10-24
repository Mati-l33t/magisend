import { useEffect, useState } from 'react';
import { copyText } from './clipboard';
import QRCode from 'qrcode';
import { Copy, Download, LoaderCircle, ShieldCheck } from 'lucide-react';
import { api } from './api';
import { Modal } from './Modal';
import { t, useLanguage, useMessage, formatNumber } from './i18n';

type Step = 'loading' | 'off' | 'scan' | 'codes' | 'on' | 'disable' | 'renew';

// Two-step login with an authenticator app: turn on (password, scan, code,
// save recovery codes), turn off (password and a code), new recovery codes.
export function TwoFactorDialog({ close, changed }: { close: () => void; changed: () => void }) {
  useLanguage();
  const [step, setStep] = useState<Step>('loading');
  const [left, setLeft] = useState(0);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [secret, setSecret] = useState('');
  const [qr, setQr] = useState('');
  const [codes, setCodes] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();

  async function load() {
    try {
      const state = await api<{ enabled: boolean; recovery_left: number }>('/api/account/2fa');
      setLeft(state.recovery_left);
      setStep(state.enabled ? 'on' : 'off');
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
  }, []);

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError('');
    try {
      await action();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  const start = () =>
    run(async () => {
      const setup = await api<{ secret: string; uri: string }>('/api/account/2fa/setup', 'POST', {
        password,
      });
      setSecret(setup.secret);
      setQr(
        await QRCode.toDataURL(setup.uri, {
          width: 200,
          margin: 2,
          color: { dark: '#050505', light: '#ffffff' },
        }),
      );
      setPassword('');
      setStep('scan');
    });
  const enable = () =>
    run(async () => {
      const result = await api<{ recovery_codes: string[] }>('/api/account/2fa/enable', 'POST', {
        code,
      });
      setCodes(result.recovery_codes);
      setCode('');
      setStep('codes');
      changed();
    });
  const disable = () =>
    run(async () => {
      await api('/api/account/2fa/disable', 'POST', { password, code });
      setPassword('');
      setCode('');
      changed();
      await load();
    });
  const renew = () =>
    run(async () => {
      const result = await api<{ recovery_codes: string[] }>('/api/account/2fa/recovery', 'POST', {
        password,
      });
      setCodes(result.recovery_codes);
      setPassword('');
      setStep('codes');
    });
  const saveCodes = () => {
    const blob = new Blob([codes.join('\n') + '\n'], { type: 'text/plain' });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = 'magisend-recovery-codes.txt';
    link.click();
    URL.revokeObjectURL(link.href);
  };

  const passwordField = (
    <label>
      {t('account.currentPassword')}
      <input
        type="password"
        autoComplete="current-password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
    </label>
  );
  const codeField = (label: string) => (
    <label>
      {label}
      <input
        className="mono code-input"
        inputMode="numeric"
        autoComplete="one-time-code"
        maxLength={16}
        value={code}
        onChange={(e) => setCode(e.target.value)}
      />
    </label>
  );
  const errorBox = error && (
    <div role="alert" className="error">
      {error}
    </div>
  );

  return (
    <Modal title={t('twoFactor.title')} close={close}>
      {step === 'loading' && (error ? errorBox : <LoaderCircle className="spin" />)}
      {step === 'off' && (
        <form
          className="form-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void start();
          }}
        >
          <p className="muted">{t('twoFactor.intro')}</p>
          {passwordField}
          {errorBox}
          <div className="dialog-actions">
            <button className="primary" disabled={busy || !password}>
              {busy ? <LoaderCircle className="spin" size={16} /> : <ShieldCheck size={16} />}
              {t('twoFactor.start')}
            </button>
          </div>
        </form>
      )}
      {step === 'scan' && (
        <form
          className="form-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void enable();
          }}
        >
          <p className="muted">{t('twoFactor.scan')}</p>
          {qr && <img className="qr-image" src={qr} alt={t('twoFactor.qrAlt')} />}
          <details className="secret-details">
            <summary>{t('twoFactor.manual')}</summary>
            <code className="secret">{secret.replace(/(.{4})/g, '$1 ').trim()}</code>
          </details>
          {codeField(t('twoFactor.code'))}
          {errorBox}
          <div className="dialog-actions">
            <button type="button" className="secondary" onClick={close}>
              {t('common.cancel')}
            </button>
            <button className="primary" disabled={busy || code.trim().length < 6}>
              {t('twoFactor.turnOn')}
            </button>
          </div>
        </form>
      )}
      {step === 'codes' && (
        <div className="form-stack">
          <p className="success-note" role="status">
            {t('twoFactor.codesTitle')}
          </p>
          <p className="muted">{t('twoFactor.codesHint')}</p>
          <ul className="recovery-codes">
            {codes.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
          <div className="dialog-actions">
            <button
              className="secondary"
              onClick={() => void copyText(codes.join('\n')).catch(() => {})}
            >
              <Copy size={16} />
              {t('share.copy')}
            </button>
            <button className="secondary" onClick={saveCodes}>
              <Download size={16} />
              {t('twoFactor.download')}
            </button>
            <button className="primary" onClick={() => void load()}>
              {t('twoFactor.saved')}
            </button>
          </div>
        </div>
      )}
      {step === 'on' && (
        <div className="form-stack">
          <p className="success-note" role="status">
            <ShieldCheck size={16} /> {t('twoFactor.isOn')}
          </p>
          <p className="muted">{t('twoFactor.left', { count: formatNumber(left) })}</p>
          <div className="dialog-actions">
            <button className="secondary" onClick={() => setStep('renew')}>
              {t('twoFactor.newCodes')}
            </button>
            <button className="quiet danger" onClick={() => setStep('disable')}>
              {t('twoFactor.turnOff')}
            </button>
          </div>
        </div>
      )}
      {step === 'renew' && (
        <form
          className="form-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void renew();
          }}
        >
          <p className="muted">{t('twoFactor.renewHint')}</p>
          {passwordField}
          {errorBox}
          <div className="dialog-actions">
            <button type="button" className="secondary" onClick={() => setStep('on')}>
              {t('common.cancel')}
            </button>
            <button className="primary" disabled={busy || !password}>
              {t('twoFactor.newCodes')}
            </button>
          </div>
        </form>
      )}
      {step === 'disable' && (
        <form
          className="form-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void disable();
          }}
        >
          <p className="muted">{t('twoFactor.disableHint')}</p>
          {passwordField}
          {codeField(t('twoFactor.codeOrRecovery'))}
          {errorBox}
          <div className="dialog-actions">
            <button type="button" className="secondary" onClick={() => setStep('on')}>
              {t('common.cancel')}
            </button>
            <button className="primary danger-solid" disabled={busy || !password || !code.trim()}>
              {t('twoFactor.turnOff')}
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}
