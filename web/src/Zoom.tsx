import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type TouchList as Touches,
} from 'react';
import { ZoomIn, ZoomOut } from 'lucide-react';
import { t, useLanguage, formatNumber } from './i18n';

const maxZoom = 6;
const step = 1.25;

function clamp(zoom: number) {
  return Math.min(maxZoom, Math.max(1, Math.round(zoom * 100) / 100));
}

// A scrollable area whose content can be zoomed with the buttons, the mouse
// wheel, a pinch on touch screens, a double click or the + - 0 keys. At 100 %
// the content fits the area; zooming keeps the point under the pointer still.
export default function Zoomable({
  natural,
  wheel,
  onResize,
  className = '',
  children,
}: {
  // Size of an image to fit inside the area; without it the content fills the
  // area's width (pages of a document).
  natural?: { width: number; height: number };
  // 'plain': the wheel always zooms. 'ctrl': only with Ctrl or ⌘ held, so the
  // wheel still scrolls through pages. Trackpad pinches count as Ctrl.
  wheel: 'plain' | 'ctrl';
  // Called after the content changed size, e.g. to draw it sharper.
  onResize?: () => void;
  className?: string;
  children: ReactNode;
}) {
  useLanguage();
  const area = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState(1);
  const [box, setBox] = useState<{ width: number; height: number } | null>(null);
  const current = useRef(1);
  const shown = useRef(1);
  const anchor = useRef<{ x: number; y: number } | null>(null);
  const pinch = useRef<{ distance: number; zoom: number } | null>(null);
  current.current = zoom;

  function zoomTo(next: number, clientX?: number, clientY?: number) {
    const el = area.current;
    next = clamp(next);
    if (!el || next === current.current) return;
    const rect = el.getBoundingClientRect();
    anchor.current =
      clientX === undefined || clientY === undefined
        ? null
        : { x: clientX - rect.left, y: clientY - rect.top };
    current.current = next;
    setZoom(next);
  }

  useEffect(() => {
    const el = area.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const measure = () =>
      // An image fits the whole area; pages fit its width beside the scrollbar.
      setBox(
        natural
          ? { width: el.offsetWidth - 2, height: el.offsetHeight - 2 }
          : { width: el.clientWidth, height: el.clientHeight },
      );
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [!!natural]);

  useEffect(() => {
    const el = area.current;
    if (!el) return;
    const onWheel = (event: WheelEvent) => {
      if (wheel === 'ctrl' && !event.ctrlKey && !event.metaKey) return;
      event.preventDefault();
      const delta = event.deltaMode === 1 ? event.deltaY * 16 : event.deltaY;
      zoomTo(current.current * Math.exp(-delta * 0.002), event.clientX, event.clientY);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (event.ctrlKey || event.metaKey || event.altKey) return;
      if (target?.closest('input, textarea, select, [contenteditable="true"]')) return;
      if (event.key === '+' || event.key === '=') zoomTo(current.current * step);
      else if (event.key === '-') zoomTo(current.current / step);
      else if (event.key === '0') zoomTo(1);
      else return;
      event.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => {
      el.removeEventListener('wheel', onWheel);
      window.removeEventListener('keydown', onKey);
    };
  }, [wheel]);

  // Moves the scroll position so the anchor point stays under the pointer.
  useLayoutEffect(() => {
    const el = area.current;
    const from = shown.current;
    shown.current = zoom;
    if (!el || from === zoom) return;
    const point = anchor.current ?? { x: el.clientWidth / 2, y: el.clientHeight / 2 };
    anchor.current = null;
    const ratio = zoom / from;
    el.scrollLeft = (el.scrollLeft + point.x) * ratio - point.x;
    el.scrollTop = (el.scrollTop + point.y) * ratio - point.y;
  }, [zoom]);

  useEffect(() => {
    onResize?.();
  }, [zoom, box]);

  let width: string | undefined;
  if (box) {
    const fit = natural
      ? Math.min(natural.width, box.width, (box.height * natural.width) / natural.height)
      : box.width;
    width = Math.max(1, Math.floor(fit * zoom)) + 'px';
  }

  function distance(touches: Touches) {
    return Math.hypot(
      touches[0].clientX - touches[1].clientX,
      touches[0].clientY - touches[1].clientY,
    );
  }

  return (
    <div className={'zoom-stage ' + className}>
      <div
        className="zoom-area"
        ref={area}
        onDoubleClick={(event) =>
          zoomTo(current.current > 1 ? 1 : 2.5, event.clientX, event.clientY)
        }
        onTouchStart={(event) => {
          if (event.touches.length === 2)
            pinch.current = { distance: distance(event.touches), zoom: current.current };
        }}
        onTouchMove={(event) => {
          if (event.touches.length !== 2 || !pinch.current) return;
          const [a, b] = [event.touches[0], event.touches[1]];
          zoomTo(
            (pinch.current.zoom * distance(event.touches)) / pinch.current.distance,
            (a.clientX + b.clientX) / 2,
            (a.clientY + b.clientY) / 2,
          );
        }}
        onTouchEnd={(event) => {
          if (event.touches.length < 2) pinch.current = null;
        }}
      >
        <div className="zoom-content" style={{ width }}>
          {children}
        </div>
      </div>
      <div className="zoom-tools">
        <button
          type="button"
          className="icon-button"
          aria-label={t('preview.zoomOut')}
          title={t('preview.zoomOut')}
          disabled={zoom <= 1}
          onClick={() => zoomTo(zoom / step)}
        >
          <ZoomOut size={17} />
        </button>
        <button
          type="button"
          className="zoom-level"
          aria-label={t('preview.zoomReset')}
          title={t('preview.zoomReset')}
          onClick={() => zoomTo(1)}
        >
          {formatNumber(Math.round(zoom * 100))} %
        </button>
        <button
          type="button"
          className="icon-button"
          aria-label={t('preview.zoomIn')}
          title={t('preview.zoomIn')}
          disabled={zoom >= maxZoom}
          onClick={() => zoomTo(zoom * step)}
        >
          <ZoomIn size={17} />
        </button>
      </div>
    </div>
  );
}
