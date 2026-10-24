import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { MoreHorizontal } from 'lucide-react';

export interface MenuItem {
  label: string;
  icon?: React.ReactNode;
  onSelect: () => void;
  disabled?: boolean;
  danger?: boolean;
}

// A compact overflow menu for row actions. Items are plain buttons, so every
// action keeps its own accessible name. The list is placed on top of the page
// next to its button, so a tile or panel that clips its content cannot hide
// it; it opens upwards when there is no room below and closes on scroll.
export default function MoreMenu({ label, items }: { label: string; items: MenuItem[] }) {
  const [open, setOpen] = useState(false);
  const [place, setPlace] = useState<React.CSSProperties>({ visibility: 'hidden' });
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const id = useId();
  // Lined up with the button's right edge, but never past either side of the
  // screen (a tile in the left column of a phone's grid is narrower than the
  // menu); below the button, or above it when there is no room below.
  function position() {
    if (!button.current || !list.current) return false;
    const at = button.current.getBoundingClientRect();
    if (at.bottom < 0 || at.top > window.innerHeight) return false;
    const height = list.current.offsetHeight;
    const width = list.current.offsetWidth;
    const below = at.bottom + 6 + height <= window.innerHeight || at.top < height + 6;
    const room = document.documentElement.clientWidth;
    setPlace({
      left: Math.max(8, Math.min(at.right - width, room - width - 8)),
      ...(below ? { top: at.bottom + 6 } : { bottom: window.innerHeight - at.top + 6 }),
    });
    return true;
  }
  useLayoutEffect(() => {
    if (open) position();
  }, [open]);
  // Once placed (and so visible), the first action takes the focus.
  useEffect(() => {
    if (open && !place.visibility)
      list.current
        ?.querySelector<HTMLButtonElement>('button:not(:disabled)')
        ?.focus({ preventScroll: true });
  }, [open, place]);
  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    function outside(event: MouseEvent) {
      const target = event.target as Node;
      if (!root.current?.contains(target) && !list.current?.contains(target)) close();
    }
    function escape(event: KeyboardEvent) {
      if (event.key !== 'Escape') return;
      close();
      button.current?.focus();
    }
    // The menu follows its button while the page scrolls or the window
    // changes, and closes once the button is off the screen.
    const follow = () => {
      if (!position()) close();
    };
    document.addEventListener('mousedown', outside);
    document.addEventListener('keydown', escape);
    window.addEventListener('scroll', follow, true);
    window.addEventListener('resize', follow);
    return () => {
      document.removeEventListener('mousedown', outside);
      document.removeEventListener('keydown', escape);
      window.removeEventListener('scroll', follow, true);
      window.removeEventListener('resize', follow);
    };
  }, [open]);
  return (
    <div className="menu" ref={root}>
      <button
        ref={button}
        className="icon-button"
        aria-label={label}
        aria-haspopup="true"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => {
          setPlace({ visibility: 'hidden' });
          setOpen(!open);
        }}
      >
        <MoreHorizontal size={18} />
      </button>
      {open &&
        createPortal(
          <div id={id} className="menu-list floating-menu" ref={list} style={place}>
            {items.map((item) => (
              <button
                key={item.label}
                className={item.danger ? 'danger' : undefined}
                disabled={item.disabled}
                onClick={() => {
                  setOpen(false);
                  item.onSelect();
                }}
              >
                {item.icon}
                {item.label}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}
