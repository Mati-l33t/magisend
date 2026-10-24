import { useEffect, useState } from 'react';
import { Folder as FolderIcon, Inbox } from 'lucide-react';
import { api, type Capabilities, type Folder } from './api';
import { Select } from './Select';
import { folderOptions } from './folderTree';
import { t } from './i18n';

// The folders a link made on Send can keep its files in, or none when the
// person cannot use folders.
export function useKeepFolders(active: boolean) {
  const [folders, setFolders] = useState<Folder[]>([]);
  useEffect(() => {
    if (!active) return;
    let cancelled = false;
    Promise.all([api<Capabilities>('/api/capabilities'), api<Folder[]>('/api/folders')])
      .then(([caps, list]) => {
        if (!cancelled) setFolders(caps?.folders && Array.isArray(list) ? list : []);
      })
      .catch(() => !cancelled && setFolders([]));
    return () => {
      cancelled = true;
    };
  }, [active]);
  return folders;
}

export function KeepFolderSelect({
  folders,
  value,
  onChange,
  disabled,
}: {
  folders: Folder[];
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  if (!folders.length) return null;
  return (
    <Select
      label={t('share.keepFolder')}
      value={folders.some((f) => f.id === value) ? value : ''}
      disabled={disabled}
      onChange={onChange}
      options={[
        { value: '', label: t('library.unfiled'), icon: <Inbox size={16} /> },
        ...folderOptions(folders).map(({ folder: f, label }) => ({
          value: f.id,
          label,
          icon: <FolderIcon size={16} />,
        })),
      ]}
    />
  );
}
