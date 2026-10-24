import { useEffect, useRef, useState } from 'react';
import { ImagePlus, LoaderCircle, Save, Trash2 } from 'lucide-react';
import { api, responseData, type User } from './api';
import { Modal } from './Modal';
import { message, t, useLanguage, useMessage } from './i18n';

// A person's picture, or their initials when they have none.
export function Avatar({ user, size = 28 }: { user: User; size?: number }) {
  const style = { width: size, height: size, fontSize: Math.round(size * 0.42) };
  if (user.avatar)
    return (
      <img
        className="avatar avatar-picture"
        src={'/api/avatars/' + encodeURIComponent(user.id) + '?v=' + user.avatar}
        alt=""
        style={style}
      />
    );
  const words = (user.display_name || user.username).split(/\s+/).filter(Boolean);
  const initials = words
    .slice(0, 2)
    .map((word) => Array.from(word)[0])
    .join('')
    .toUpperCase();
  return (
    <span className="avatar" aria-hidden="true" style={style}>
      {initials}
    </span>
  );
}

export function displayName(user: User) {
  return user.display_name || user.username;
}

const VIEW = 240; // the crop square on screen, in CSS pixels
const OUTPUT = 512; // the square sent to the server

export function ProfileDialog({
  user,
  close,
  saved,
}: {
  user: User;
  close: () => void;
  saved: () => Promise<void>;
}) {
  useLanguage();
  const [name, setName] = useState(user.display_name || '');
  const [image, setImage] = useState<HTMLImageElement | null>(null);
  const [zoom, setZoom] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useMessage();
  const picker = useRef<HTMLInputElement>(null);
  const drag = useRef<{ x: number; y: number; start: { x: number; y: number } } | null>(null);
  const source = useRef('');
  useEffect(() => () => URL.revokeObjectURL(source.current), []);

  // The picture covers the square at zoom 1; offset moves its centre.
  const base = image ? Math.max(VIEW / image.naturalWidth, VIEW / image.naturalHeight) : 1;
  const width = image ? image.naturalWidth * base * zoom : VIEW;
  const height = image ? image.naturalHeight * base * zoom : VIEW;
  function clamp(next: { x: number; y: number }, w = width, h = height) {
    const maxX = (w - VIEW) / 2;
    const maxY = (h - VIEW) / 2;
    return {
      x: Math.max(-maxX, Math.min(maxX, next.x)),
      y: Math.max(-maxY, Math.min(maxY, next.y)),
    };
  }

  function choose(file: File | undefined) {
    if (!file) return;
    setError('');
    URL.revokeObjectURL(source.current);
    const url = URL.createObjectURL(file);
    source.current = url;
    const img = new Image();
    img.onload = () => {
      setImage(img);
      setZoom(1);
      setOffset({ x: 0, y: 0 });
    };
    img.onerror = () => setError(message('errors.invalid_avatar'));
    img.src = url;
  }

  function cropped(): Promise<Blob | null> {
    if (!image) return Promise.resolve(null);
    const canvas = document.createElement('canvas');
    canvas.width = OUTPUT;
    canvas.height = OUTPUT;
    const context = canvas.getContext('2d');
    if (!context) return Promise.resolve(null);
    const scale = OUTPUT / VIEW;
    context.fillStyle = '#fff';
    context.fillRect(0, 0, OUTPUT, OUTPUT);
    context.drawImage(
      image,
      ((VIEW - width) / 2 + offset.x) * scale,
      ((VIEW - height) / 2 + offset.y) * scale,
      width * scale,
      height * scale,
    );
    return new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.9));
  }

  async function save() {
    setBusy(true);
    setError('');
    try {
      const blob = await cropped();
      if (blob)
        await responseData(
          await fetch('/api/account/avatar', {
            method: 'PUT',
            credentials: 'same-origin',
            headers: { 'X-MagiSend': '1', 'Content-Type': 'image/jpeg' },
            body: blob,
          }),
        );
      if (name.trim() !== (user.display_name || ''))
        await api('/api/account/profile', 'PUT', { display_name: name });
      await saved();
      close();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  async function removePicture() {
    setBusy(true);
    setError('');
    try {
      await api('/api/account/avatar', 'DELETE', {});
      await saved();
      close();
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={t('profile.title')} close={close} className="profile-dialog">
      <form
        className="form-stack"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="profile-picture">
          {image ? (
            <div className="cropper">
              <div
                className="crop-view"
                role="img"
                aria-label={t('profile.cropHint')}
                tabIndex={0}
                style={{ width: VIEW, height: VIEW }}
                onPointerDown={(e) => {
                  e.currentTarget.setPointerCapture(e.pointerId);
                  drag.current = { x: e.clientX, y: e.clientY, start: offset };
                }}
                onPointerMove={(e) => {
                  const d = drag.current;
                  if (d)
                    setOffset(
                      clamp({ x: d.start.x + e.clientX - d.x, y: d.start.y + e.clientY - d.y }),
                    );
                }}
                onPointerUp={() => (drag.current = null)}
                onPointerCancel={() => (drag.current = null)}
                onKeyDown={(e) => {
                  const step = e.shiftKey ? 20 : 5;
                  const moves: Record<string, [number, number]> = {
                    ArrowLeft: [step, 0],
                    ArrowRight: [-step, 0],
                    ArrowUp: [0, step],
                    ArrowDown: [0, -step],
                  };
                  const move = moves[e.key];
                  if (!move) return;
                  e.preventDefault();
                  setOffset(clamp({ x: offset.x + move[0], y: offset.y + move[1] }));
                }}
              >
                <img
                  src={image.src}
                  alt=""
                  draggable={false}
                  style={{
                    width,
                    height,
                    left: (VIEW - width) / 2 + offset.x,
                    top: (VIEW - height) / 2 + offset.y,
                  }}
                />
              </div>
              <label className="zoom-field">
                {t('profile.zoom')}
                <input
                  type="range"
                  min={1}
                  max={3}
                  step={0.01}
                  value={zoom}
                  onChange={(e) => {
                    const next = Number(e.target.value);
                    setZoom(next);
                    const w = image.naturalWidth * base * next;
                    const h = image.naturalHeight * base * next;
                    setOffset(clamp(offset, w, h));
                  }}
                />
              </label>
              <small className="field-hint">{t('profile.cropHint')}</small>
            </div>
          ) : (
            <Avatar user={{ ...user, display_name: name }} size={112} />
          )}
          <div className="profile-picture-actions">
            <button
              type="button"
              className="secondary small"
              disabled={busy}
              onClick={() => picker.current?.click()}
            >
              <ImagePlus size={15} />
              {user.avatar || image ? t('profile.change') : t('profile.choose')}
            </button>
            {!!user.avatar && !image && (
              <button
                type="button"
                className="quiet small danger"
                disabled={busy}
                onClick={() => void removePicture()}
              >
                <Trash2 size={15} />
                {t('profile.remove')}
              </button>
            )}
            <input
              ref={picker}
              className="sr-only"
              type="file"
              accept="image/png,image/jpeg,image/gif"
              aria-label={t('profile.choose')}
              tabIndex={-1}
              onChange={(e) => {
                choose(e.target.files?.[0]);
                e.target.value = '';
              }}
            />
          </div>
        </div>
        <label>
          {t('profile.displayName')} <span className="muted">{t('share.optional')}</span>
          <input
            value={name}
            maxLength={60}
            placeholder={user.username}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <small className="field-hint">{t('profile.displayNameHint')}</small>
        {error && (
          <div role="alert" className="error">
            {error}
          </div>
        )}
        <div className="dialog-actions">
          <button type="button" className="secondary" onClick={close}>
            {t('common.cancel')}
          </button>
          <button className="primary" disabled={busy}>
            {busy ? <LoaderCircle className="spin" size={16} /> : <Save size={16} />}
            {t('common.save')}
          </button>
        </div>
      </form>
    </Modal>
  );
}
