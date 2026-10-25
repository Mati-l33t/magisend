import { useEffect, useState } from 'react';
import type { SharedFile } from './api';
import FileKindIcon, { fileKind } from './fileKind';
import { browserThumbName, ensureThumb } from './thumbMaker';

// Raster names a thumbnail may exist for. The backend still validates every byte
// before it serves anything, so this is only a request hint, never a trust
// decision.
export const rasterName = /\.(png|jpe?g|gif)$/i;

// A thumbnail is decoration only: small, lazy, cropped and never a second control
// for the same action — the filename button next to it stays the single, keyboard
// reachable way to open the full preview. Files that are not raster images keep the
// icon of their kind and cause no request; an image without a stored thumbnail is
// filled in by the server on first request, so the answer does not depend on timing.
// For the owner, a PDF, video or other image type without a picture gets one drawn
// by the browser (thumbMaker) and shows it once the server has stored it.
export default function FileThumb({
  shareId,
  file,
  owner = false,
}: {
  shareId: string;
  file: SharedFile;
  owner?: boolean;
}) {
  // Not while the file waits for its virus scan: it cannot be read yet.
  const drawable =
    owner &&
    !file.scan &&
    !file.has_thumb &&
    browserThumbName.test(file.name) &&
    (file.offset ?? file.size) === file.size;
  const initial = file.has_thumb || rasterName.test(file.name) ? 'loading' : 'none';
  const [state, setState] = useState<'loading' | 'ready' | 'none'>(initial);
  useEffect(() => {
    if (!drawable) return;
    let current = true;
    void ensureThumb(shareId, file).then((ok) => {
      if (ok && current) setState('loading');
    });
    return () => {
      current = false;
    };
  }, [drawable, shareId, file.id]);
  // Rows are keyed by file id, so the same component can outlive a list refresh:
  // follow the file rather than the state captured on first render. Resetting
  // during render (not in an effect) means a load error that arrives right
  // after mounting can never be overwritten by a late reset.
  const identity = file.id + '\n' + file.name + '\n' + file.has_thumb;
  const [seen, setSeen] = useState(identity);
  if (seen !== identity) {
    setSeen(identity);
    setState(initial);
  }
  if (state === 'none') return <FileKindIcon name={file.name} />;
  const source =
    '/api/' +
    (owner ? 'shares' : 'public') +
    '/' +
    encodeURIComponent(shareId) +
    '/files/' +
    encodeURIComponent(file.id) +
    '/thumbnail';
  return (
    <span
      className={'file-thumb' + (fileKind(file.name) === 'pdf' ? ' page-thumb' : '')}
      aria-hidden="true"
    >
      <img
        src={source}
        alt=""
        loading="lazy"
        decoding="async"
        width={48}
        height={48}
        onLoad={() => setState('ready')}
        onError={() => setState('none')}
      />
    </span>
  );
}
