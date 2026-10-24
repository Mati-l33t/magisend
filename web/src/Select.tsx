import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Check, ChevronDown } from 'lucide-react';

export interface SelectOption<T> {
  value: T;
  label: string;
  icon?: ReactNode;
}

// The app's own dropdown, used instead of the browser's: a select-only
// combobox with a listbox popup (WAI-ARIA pattern). Focus stays on the
// button; the arrow keys, Home/End, typing and Enter choose an option.
export function Select<T extends string | number>({
  label,
  labelExtra,
  ariaLabel,
  value,
  options,
  onChange,
  disabled,
  hint,
  className,
}: {
  label?: ReactNode;
  labelExtra?: ReactNode;
  ariaLabel?: string;
  value: T;
  options: SelectOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  hint?: ReactNode;
  className?: string;
}) {
  const id = useId();
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const typed = useRef({ text: '', at: 0 });
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  // Out of the page flow from the first frame, so opening never moves the
  // button it is measured against.
  const hidden: React.CSSProperties = { position: 'fixed', visibility: 'hidden' };
  const [position, setPosition] = useState<React.CSSProperties>(hidden);
  const selected = Math.max(
    0,
    options.findIndex((option) => option.value === value),
  );
  const current = options[selected];

  function show() {
    if (disabled || !options.length) return;
    setActive(selected);
    setPosition(hidden);
    setOpen(true);
  }
  function choose(index: number) {
    const option = options[index];
    setOpen(false);
    button.current?.focus();
    if (option && option.value !== value) onChange(option.value);
  }

  // Placed against the viewport so dialogs and panels never clip it; it
  // opens upwards when there is more room above.
  useLayoutEffect(() => {
    if (!open || !button.current) return;
    const place = () => {
      const rect = button.current!.getBoundingClientRect();
      const below = innerHeight - rect.bottom - 12;
      const above = rect.top - 12;
      const height = Math.min(list.current?.scrollHeight || 280, 280);
      const up = below < height && above > below;
      setPosition({
        position: 'fixed',
        left: rect.left,
        width: rect.width,
        maxHeight: Math.max(120, Math.min(280, up ? above : below)),
        ...(up ? { bottom: innerHeight - rect.top + 4 } : { top: rect.bottom + 4 }),
      });
    };
    place();
    addEventListener('resize', place);
    return () => removeEventListener('resize', place);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!button.current?.contains(target) && !list.current?.contains(target)) setOpen(false);
    };
    const scrolled = (event: Event) => {
      if (event.target !== list.current) setOpen(false);
    };
    document.addEventListener('pointerdown', outside);
    document.addEventListener('scroll', scrolled, true);
    return () => {
      document.removeEventListener('pointerdown', outside);
      document.removeEventListener('scroll', scrolled, true);
    };
  }, [open]);

  useEffect(() => {
    if (open)
      (list.current?.children[active] as HTMLElement | undefined)?.scrollIntoView?.({
        block: 'nearest',
      });
  }, [open, active]);

  function keyDown(event: React.KeyboardEvent) {
    const last = options.length - 1;
    const move = (index: number) => {
      event.preventDefault();
      if (open) setActive(Math.max(0, Math.min(last, index)));
      else show();
    };
    switch (event.key) {
      case 'ArrowDown':
        return move(active + 1);
      case 'ArrowUp':
        return move(active - 1);
      case 'Home':
        return move(0);
      case 'End':
        return move(last);
      case 'Enter':
      case ' ':
        event.preventDefault();
        if (open) choose(active);
        else show();
        return;
      case 'Escape':
        if (open) {
          // Close only the list, not a dialog around it.
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
        }
        return;
      case 'Tab':
        if (open) choose(active);
        return;
    }
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      const now = Date.now();
      typed.current.text =
        (now - typed.current.at < 700 ? typed.current.text : '') + event.key.toLowerCase();
      typed.current.at = now;
      const match = options.findIndex((option) =>
        option.label.toLowerCase().startsWith(typed.current.text),
      );
      if (match >= 0) {
        if (open) setActive(match);
        else if (options[match].value !== value) onChange(options[match].value);
      }
    }
  }

  return (
    <div className={'select-field' + (className ? ' ' + className : '')}>
      {label && (
        <span className="field-label" id={id + 'label'}>
          {label} {labelExtra}
        </span>
      )}
      <button
        ref={button}
        type="button"
        role="combobox"
        className="select-button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id + 'list' : undefined}
        aria-activedescendant={open ? id + 'option' + active : undefined}
        aria-labelledby={label && !ariaLabel ? id + 'label' : undefined}
        aria-label={ariaLabel}
        aria-describedby={hint ? id + 'hint' : undefined}
        disabled={disabled}
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={keyDown}
      >
        <span className="select-value">
          {current?.icon}
          {current?.label}
        </span>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      {open &&
        createPortal(
          <ul
            ref={list}
            id={id + 'list'}
            role="listbox"
            className={
              'select-list' +
              (className
                ? ' ' +
                  className
                    .split(' ')
                    .map((name) => name + '-list')
                    .join(' ')
                : '')
            }
            aria-labelledby={label && !ariaLabel ? id + 'label' : undefined}
            aria-label={ariaLabel}
            style={position}
          >
            {options.map((option, index) => (
              <li
                key={String(option.value)}
                id={id + 'option' + index}
                role="option"
                aria-selected={index === selected}
                className={index === active ? 'active' : undefined}
                onPointerMove={() => setActive(index)}
                onPointerDown={(event) => event.preventDefault()}
                onClick={() => choose(index)}
              >
                {option.icon}
                <span>{option.label}</span>
                {index === selected && <Check size={15} aria-hidden="true" />}
              </li>
            ))}
          </ul>,
          // Outside the page flow, so a panel's blur or stacking never moves or
          // covers it; inside an open dialog it stays in the dialog's top layer.
          button.current?.closest('dialog') ?? document.body,
        )}
      {hint && (
        <small id={id + 'hint'} className="field-hint">
          {hint}
        </small>
      )}
    </div>
  );
}
