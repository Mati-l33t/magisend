import { useEffect, useState } from 'react';
import { rasterName } from './FileThumb';
import FileKindIcon from './fileKind';

// A file that has not been uploaded yet has no server thumbnail, so the preview
// comes straight from the browser's own File object. The object URL is revoked when
// the row disappears. Only raster extensions qualify: an SVG object URL would put
// active markup in an image context, so it keeps the icon of its kind.
export default function LocalThumb({ file }: { file: File }) {
  const [wanted] = useState(() => rasterName.test(file.name));
  const [source, setSource] = useState('');
  useEffect(() => {
    if (!wanted || typeof URL.createObjectURL !== 'function') return;
    const url = URL.createObjectURL(file);
    setSource(url);
    return () => URL.revokeObjectURL(url);
  }, [file, wanted]);
  if (!wanted || !source) return <FileKindIcon name={file.name} />;
  return (
    <span className="file-thumb" aria-hidden="true">
      <img
        src={source}
        alt=""
        loading="lazy"
        decoding="async"
        width={48}
        height={48}
        onError={() => setSource('')}
      />
    </span>
  );
}
