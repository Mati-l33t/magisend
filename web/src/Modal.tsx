import { useEffect, useRef } from 'react';
import { X } from 'lucide-react';
import { t, useLanguage } from './i18n';

export function Modal({
  title,
  close,
  children,
  className,
}: {
  title: string;
  close: () => void;
  children: React.ReactNode;
  className?: string;
}) {
  useLanguage();
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (dialog.current?.showModal) dialog.current.showModal();
    else dialog.current?.setAttribute('open', '');
  }, []);
  return (
    <dialog
      ref={dialog}
      className={'dialog' + (className ? ' ' + className : '')}
      onCancel={(e) => {
        e.preventDefault();
        close();
      }}
      aria-label={title}
    >
      <div className="dialog-heading">
        <h2>{title}</h2>
        <button className="icon-button" aria-label={t('common.close')} onClick={close}>
          <X size={18} />
        </button>
      </div>
      {children}
    </dialog>
  );
}
