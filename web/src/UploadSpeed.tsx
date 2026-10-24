import { formatBytes } from './api';
import { t, formatDuration } from './i18n';
import type { UploadStats } from './uploader';

// Speed and time left under an upload's progress bar.
export function UploadSpeed({ stats }: { stats: UploadStats | null }) {
  if (!stats) return null;
  if (stats.retrying)
    return (
      <small className="upload-speed warn" role="status">
        {t('upload.retrying')}
      </small>
    );
  if (!stats.speed) return <small className="upload-speed">{t('upload.measuring')}</small>;
  return (
    <small className="upload-speed">
      {stats.left === null || stats.left < 1
        ? t('upload.speed', { speed: formatBytes(stats.speed) })
        : t('upload.speedLeft', {
            speed: formatBytes(stats.speed),
            left: formatDuration(stats.left),
          })}
    </small>
  );
}
