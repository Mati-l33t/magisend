import { LoaderCircle, ShieldAlert } from 'lucide-react';
import { t, useLanguage } from './i18n';

// Below a file's name and size: while it waits for its virus scan, or when a virus
// was found in it. Either way it cannot be downloaded.
export function ScanNote({ scan }: { scan?: string }) {
  useLanguage();
  if (scan === 'pending')
    return (
      <small className="scan-note">
        <LoaderCircle size={12} className="spin" /> {t('scan.pending')}
      </small>
    );
  if (scan === 'infected')
    return (
      <small className="scan-note infected">
        <ShieldAlert size={12} /> {t('scan.infected')}
      </small>
    );
  return null;
}
