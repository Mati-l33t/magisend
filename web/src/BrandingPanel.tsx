import { useEffect, useRef, useState } from 'react';
import { ImageUp, Palette, Save, Trash2 } from 'lucide-react';
import { api, responseData } from './api';
import { SettingRow } from './SettingRow';
import { Select } from './Select';
import { defaultLook, looks, type Look } from './look';
import { brandLogo, type Branding } from './branding';
import { t, useLanguage, useMessage, message } from './i18n';

const palette = [
  '#ff5f00',
  '#e5484d',
  '#d6409f',
  '#8e4ec6',
  '#3e63dd',
  '#0090ff',
  '#12a594',
  '#30a46c',
  '#ffc53d',
  '#ad7f58',
  '#8b8d98',
];

// The installation's name, accent color and logo. A swatch row instead of
// the browser's color picker; any #rrggbb can still be typed.
export function BrandingPanel({ changed }: { changed: () => Promise<void> }) {
  useLanguage();
  const [initial, setInitial] = useState<Branding>({ name: '', color: '', logo: false });
  const [name, setName] = useState('');
  const [color, setColor] = useState('');
  const [look, setLook] = useState<Look>(defaultLook);
  async function load() {
    const status = await api<{ branding?: Branding }>('/api/status');
    if (!status?.branding) return;
    setInitial(status.branding);
    setName(status.branding.name);
    setColor(status.branding.color);
    setLook(looks.find((l) => l === status.branding?.look) || defaultLook);
  }
  useEffect(() => {
    void load().catch(() => {});
  }, []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const [notice, setNotice] = useMessage();
  const picker = useRef<HTMLInputElement>(null);
  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await action();
      setNotice(message('branding.saved'));
      await changed();
      await load();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  const validColor = color === '' || /^#[0-9a-fA-F]{6}$/.test(color);
  return (
    <section className="panel admin-wide brand-panel">
      <h2>
        <Palette size={18} /> {t('branding.title')}
      </h2>
      <p className="muted panel-intro">{t('branding.intro')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notice && (
        <div role="status" className="success-note">
          {notice}
        </div>
      )}
      <form
        className="settings-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (!validColor) return setError(message('branding.badColor'));
          void run(() => api('/api/admin/branding', 'PUT', { name, color, look }));
        }}
      >
        <SettingRow id="brand-name" label={t('branding.name')} hint={t('branding.nameHint')}>
          <input
            id="brand-name"
            aria-describedby="brand-name-hint"
            value={name}
            maxLength={40}
            placeholder="MagiSend"
            onChange={(e) => setName(e.target.value)}
          />
        </SettingRow>
        <SettingRow id="brand-color" label={t('branding.color')} hint={t('branding.colorHint')}>
          <input
            id="brand-color"
            aria-describedby="brand-color-hint"
            className="mono"
            value={color}
            maxLength={7}
            placeholder="#ff5f00"
            onChange={(e) => setColor(e.target.value.trim())}
          />
          <div role="radiogroup" aria-label={t('branding.color')} className="swatches">
            {palette.map((swatch) => (
              <button
                type="button"
                key={swatch}
                role="radio"
                aria-checked={color.toLowerCase() === swatch}
                aria-label={swatch}
                className="swatch"
                style={{ background: swatch }}
                onClick={() => setColor(swatch)}
              />
            ))}
          </div>
        </SettingRow>
        <SettingRow id="brand-look" label={t('branding.look')} hint={t('branding.lookHint')}>
          <Select
            ariaLabel={t('branding.look')}
            value={look}
            options={looks.map((l) => ({ value: l, label: t(`look.${l}`) }))}
            onChange={setLook}
          />
        </SettingRow>
        <SettingRow label={t('branding.logo')} hint={t('branding.logoHint')}>
          <div className="brand-logo-row">
            <img src={brandLogo()} alt={t('branding.logo')} />
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => picker.current?.click()}
            >
              <ImageUp size={16} />
              {t('branding.uploadLogo')}
            </button>
            {initial.logo && (
              <button
                type="button"
                className="quiet danger"
                disabled={busy}
                onClick={() => void run(() => api('/api/admin/branding/logo', 'DELETE', {}))}
              >
                <Trash2 size={16} />
                {t('branding.removeLogo')}
              </button>
            )}
            <input
              ref={picker}
              className="sr-only"
              type="file"
              accept="image/png"
              aria-label={t('branding.uploadLogo')}
              onChange={(e) => {
                const file = e.target.files?.[0];
                e.target.value = '';
                if (file)
                  void run(async () =>
                    responseData(
                      await fetch('/api/admin/branding/logo', {
                        method: 'PUT',
                        credentials: 'same-origin',
                        headers: { 'X-MagiSend': '1', 'Content-Type': 'image/png' },
                        body: file,
                      }),
                    ),
                  );
              }}
            />
          </div>
        </SettingRow>
        <div className="setting-actions">
          <button className="primary" disabled={busy}>
            <Save size={16} />
            {t('common.save')}
          </button>
        </div>
      </form>
    </section>
  );
}
