import { Download } from 'lucide-react';
import type { Share, SharedFile } from './api';
import { Select, type SelectOption } from './Select';
import { t, useLanguage, formatDate, formatNumber } from './i18n';

export const limits = [1, 2, 3, 5, 10, 25, 50, 100];

// How many times each file of a link may be downloaded; 0 means no limit.
export function DownloadLimitSelect({
  value,
  onChange,
  disabled,
  hint,
}: {
  value: number;
  onChange: (limit: number) => void;
  disabled?: boolean;
  hint?: string;
}) {
  useLanguage();
  return (
    <Select
      label={t('downloads.limit')}
      value={value}
      disabled={disabled}
      hint={hint || undefined}
      onChange={onChange}
      options={[
        { value: 0, label: t('downloads.unlimited') },
        ...Array.from(new Set([...limits, value]))
          .filter((n) => n > 0)
          .sort((a, b) => a - b)
          .map((n) => ({
            value: n,
            label: n === 1 ? t('downloads.once') : t('downloads.times', { count: formatNumber(n) }),
          })),
      ]}
    />
  );
}

// Validity choices in days; 0 never expires. Extra values (the policy
// default, the current setting) are merged in.
export function validityOptions(extra: number[]): SelectOption<number>[] {
  return [
    { value: 0, label: t('share.neverExpires') },
    ...Array.from(new Set([1, 3, 7, 14, 30, ...extra]))
      .filter((d) => d > 0)
      .sort((a, b) => a - b)
      .map((d) => ({
        value: d,
        label: formatNumber(d) + ' ' + (d === 1 ? t('share.day') : t('share.days')),
      })),
  ];
}

export function limitHint(limit: number, library = false) {
  if (limit === 1) return t(library ? 'downloads.closesLibraryOnce' : 'downloads.closesOnce');
  return t(library ? 'downloads.closesLibrary' : 'downloads.closes', {
    count: formatNumber(limit),
  });
}

// The owner's per-file count, e.g. "↓ 2/5", with the last download as a tooltip.
export function FileDownloads({ share, file }: { share: Share; file: SharedFile }) {
  useLanguage();
  const count = file.downloads || 0;
  const limit = share.download_limit || 0;
  const label = limit ? `${formatNumber(count)}/${formatNumber(limit)}` : formatNumber(count);
  const title = file.last_download
    ? t('downloads.fileLast', {
        count: formatNumber(count),
        date: formatDate(file.last_download * 1000, true),
      })
    : t('downloads.fileNever');
  return (
    <span
      className={'file-downloads' + (limit && count >= limit ? ' used-up' : '')}
      title={title}
      aria-label={title}
    >
      <Download size={12} aria-hidden="true" />
      {label}
    </span>
  );
}
