import { Check } from 'lucide-react';
import { t, useLanguage } from './i18n';
import { looks, type Look } from './look';

// A row of small previews; each one draws its look in miniature.
export default function LookPicker({
  value,
  onChange,
}: {
  value: Look;
  onChange: (look: Look) => void;
}) {
  useLanguage();
  return (
    <fieldset className="look-picker">
      <legend>{t('settings.look')}</legend>
      <p className="muted">{t('settings.lookHint')}</p>
      <div className="look-options" role="radiogroup" aria-label={t('settings.look')}>
        {looks.map((look) => (
          <button
            key={look}
            type="button"
            role="radio"
            aria-checked={value === look}
            className={'look-option' + (value === look ? ' selected' : '')}
            onClick={() => onChange(look)}
          >
            <span className={'look-sample sample-' + look} aria-hidden="true">
              <i className="s-nav" />
              <i className="s-title" />
              <i className="s-card" />
              <i className="s-button" />
            </span>
            <span className="look-text">
              <strong>
                {t(`look.${look}`)}
                {value === look && <Check size={15} />}
              </strong>
              <small>{t(`look.${look}Description`)}</small>
            </span>
          </button>
        ))}
      </div>
    </fieldset>
  );
}
