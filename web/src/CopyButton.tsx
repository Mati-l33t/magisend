import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Check, Copy } from 'lucide-react';
import { copyText } from './clipboard';
import { t } from './i18n';

// A copy button that shows it worked: a check and a short "Copied" note for
// a moment, or that copying failed. With children it is a labelled button,
// otherwise an icon.
export default function CopyButton({
  text,
  label,
  className = 'icon-button',
  size = 16,
  disabled,
  failed,
  children,
}: {
  text: string;
  label: string;
  className?: string;
  size?: number;
  disabled?: boolean;
  // Called when the browser refused, for example to select the text instead.
  failed?: () => void;
  children?: ReactNode;
}) {
  const [state, setState] = useState<'' | 'copied' | 'failed'>('');
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  function copy() {
    copyText(text).then(
      () => show('copied'),
      () => {
        show('failed');
        failed?.();
      },
    );
  }
  function show(next: 'copied' | 'failed') {
    setState(next);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState(''), 2000);
  }
  // A labelled button says it in its label; the note is for icons and failures.
  const note =
    state === 'failed' ? t('copy.failed') : state === 'copied' && !children ? t('code.copied') : '';
  return (
    <button
      type="button"
      className={className + ' copy-button' + (state ? ' ' + state : '')}
      aria-label={children ? undefined : label}
      title={label}
      disabled={disabled}
      onClick={copy}
    >
      {state === 'copied' ? <Check size={size} /> : <Copy size={size} />}
      {children && (state === 'copied' ? t('code.copied') : children)}
      <span className="copy-note" role="status">
        {note}
      </span>
    </button>
  );
}
