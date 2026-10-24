import { useEffect, useState, type ReactNode } from 'react';
import { ArrowUp } from 'lucide-react';
import { t, useLanguage } from './i18n';

// One setting: what it is and a short explanation on the left, the control on
// the right. The control carries `id` (and `aria-describedby={id + '-hint'}`
// when there is a hint) so the label and the explanation belong to it.
export function SettingRow({
  id,
  label,
  hint,
  children,
  stack,
}: {
  id?: string;
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
  stack?: boolean;
}) {
  return (
    <div className={'setting-row' + (stack ? ' stack' : '')}>
      <div className="setting-text">
        {id ? <label htmlFor={id}>{label}</label> : <span className="setting-label">{label}</span>}
        {hint && <small id={id ? id + '-hint' : undefined}>{hint}</small>}
      </div>
      <div className="setting-control">{children}</div>
    </div>
  );
}

// Appears once a page has been scrolled well down and takes it back up.
export function ScrollTop() {
  useLanguage();
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const check = () => setShown(window.scrollY > 600);
    check();
    addEventListener('scroll', check, { passive: true });
    return () => removeEventListener('scroll', check);
  }, []);
  if (!shown) return null;
  return (
    <button
      type="button"
      className="scroll-top"
      aria-label={t('app.backToTop')}
      title={t('app.backToTop')}
      onClick={() =>
        window.scrollTo({
          top: 0,
          behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
        })
      }
    >
      <ArrowUp size={18} />
    </button>
  );
}
