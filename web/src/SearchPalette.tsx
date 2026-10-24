import { useEffect, useMemo, useRef, useState } from 'react';
import {
  CornerDownLeft,
  File as FileIcon,
  Folder as FolderIcon,
  FolderOpen,
  Inbox,
  Link,
  Search,
  Settings,
  Upload,
} from 'lucide-react';
import { api, formatBytes, type Folder, type LibraryFile, type Share } from './api';
import { formatNumber, t, useLanguage } from './i18n';
import { folderLabel } from './folderTree';

export type SearchTab = 'send' | 'library' | 'shares' | 'requests' | 'admin';

interface Item {
  key: string;
  group: 'pages' | 'links' | 'folders' | 'files';
  label: string;
  detail?: string;
  icon: React.ReactNode;
  run: () => void;
}

// Opens the search from anywhere, such as the button in the top bar.
export const openSearchEvent = 'magisend:search';
export const searchShortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K';

const limits = { pages: 6, links: 6, folders: 5, files: 8 };

// Lower is better: names that start with the words come first.
function rank(text: string, query: string) {
  const value = text.toLowerCase();
  if (!query) return 0;
  if (value.startsWith(query)) return 0;
  if (value.includes(' ' + query)) return 1;
  if (value.includes(query)) return 2;
  return -1;
}

export function SearchPalette({
  close,
  admin,
  shares,
  openTab,
  openShare,
  openPath,
}: {
  close: () => void;
  admin: boolean;
  shares: Share[];
  openTab: (tab: SearchTab) => void;
  openShare: (share: Share) => void;
  openPath: (path: string) => void;
}) {
  useLanguage();
  const dialog = useRef<HTMLDialogElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [files, setFiles] = useState<LibraryFile[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  useEffect(() => {
    dialog.current?.showModal?.();
    // People without a library simply get no files or folders.
    api<LibraryFile[]>('/api/library/files')
      .then((f) => setFiles(Array.isArray(f) ? f : []))
      .catch(() => {});
    api<Folder[]>('/api/folders')
      .then((f) => setFolders(Array.isArray(f) ? f : []))
      .catch(() => {});
  }, []);

  const items = useMemo(() => {
    const q = query.trim().toLowerCase();
    const done = (fn: () => void) => () => {
      close();
      fn();
    };
    const pick = <T,>(values: T[], text: (value: T) => string, limit: number) =>
      values
        .map((value) => ({ value, score: rank(text(value), q) }))
        .filter((entry) => entry.score >= 0)
        .sort((a, b) => a.score - b.score)
        .slice(0, limit)
        .map((entry) => entry.value);
    const pages: [SearchTab, string, React.ReactNode][] = [
      ['send', t('workspace.send'), <Upload size={17} />],
      ['library', t('library.title'), <FolderOpen size={17} />],
      ['shares', t('workspace.shares'), <Link size={17} />],
      ['requests', t('requests.title'), <Inbox size={17} />],
    ];
    if (admin) pages.push(['admin', t('workspace.admin'), <Settings size={17} />]);
    const folderName = (id: string) => (id && folderLabel(folders, id)) || t('library.unfiled');
    const now = Date.now();
    const result: Item[] = [
      ...pick(pages, (page) => page[1], limits.pages).map(([tab, label, icon]): Item => ({
        key: 'page-' + tab,
        group: 'pages',
        label,
        icon,
        run: done(() => openTab(tab)),
      })),
      ...pick(
        // Newest first, so an empty search shows the latest links.
        [...shares].sort((a, b) => b.created - a.created),
        (share) => share.title,
        q ? limits.links : 4,
      ).map((share): Item => ({
        key: 'link-' + share.id,
        group: 'links',
        label: share.title,
        detail: !share.published
          ? t('share.draft')
          : share.expires !== 0 && share.expires * 1000 <= now
            ? t('share.expired')
            : share.mode
              ? t('shares.fromLibrary')
              : t('files.count', { count: formatNumber(share.files.length) }),
        icon: <Link size={17} />,
        run: done(() => openShare(share)),
      })),
    ];
    // Folders and files only once something is typed; there can be many.
    if (q) {
      result.push(
        ...pick(folders, (folder) => folder.name, limits.folders).map((folder): Item => ({
          key: 'folder-' + folder.id,
          group: 'folders',
          label: folder.name,
          detail: folder.parent_id ? folderLabel(folders, folder.parent_id) : undefined,
          icon: <FolderIcon size={17} />,
          run: done(() => openPath('/library/' + encodeURIComponent(folder.id))),
        })),
        ...pick(
          files.filter((file) => file.offset === file.size),
          (file) => file.name,
          limits.files,
        ).map((file): Item => ({
          key: 'file-' + file.id,
          group: 'files',
          label: file.name,
          detail: folderName(file.folder_id) + ' · ' + formatBytes(file.size),
          icon: <FileIcon size={17} />,
          run: done(() =>
            openPath(
              '/library/' +
                (file.folder_id ? encodeURIComponent(file.folder_id) : 'unfiled') +
                '?file=' +
                encodeURIComponent(file.id),
            ),
          ),
        })),
      );
    }
    return result;
  }, [query, shares, files, folders, admin]);

  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>('[data-index="' + active + '"]')
      ?.scrollIntoView({ block: 'nearest' });
  }, [active]);

  const groups = (['pages', 'links', 'folders', 'files'] as const).filter((group) =>
    items.some((item) => item.group === group),
  );
  const headings = {
    pages: t('search.pages'),
    links: t('search.links'),
    folders: t('search.folders'),
    files: t('search.files'),
  };
  const activeItem = items[active];
  return (
    <dialog
      ref={dialog}
      className="dialog search-palette"
      aria-label={t('search.open')}
      onCancel={(e) => {
        e.preventDefault();
        close();
      }}
      onClick={(e) => {
        // A click on the backdrop closes the search.
        if (e.target === dialog.current) close();
      }}
    >
      <div className="search-field">
        <Search size={18} />
        <input
          autoFocus
          role="combobox"
          aria-expanded="true"
          aria-controls="search-results"
          aria-activedescendant={activeItem ? 'search-' + activeItem.key : undefined}
          aria-label={t('search.open')}
          placeholder={t('search.placeholder')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
              e.preventDefault();
              if (!items.length) return;
              const step = e.key === 'ArrowDown' ? 1 : -1;
              setActive((current) => (current + step + items.length) % items.length);
            } else if (e.key === 'Enter') {
              e.preventDefault();
              activeItem?.run();
            }
          }}
        />
        <kbd>Esc</kbd>
      </div>
      <div className="search-results" id="search-results" role="listbox" ref={list}>
        {!items.length && (
          <p className="search-empty">{t('search.empty', { query: query.trim() })}</p>
        )}
        {groups.map((group) => (
          <div key={group} role="group" aria-label={headings[group]}>
            <div className="search-group">{headings[group]}</div>
            {items.map((item, index) =>
              item.group !== group ? null : (
                <div
                  key={item.key}
                  id={'search-' + item.key}
                  data-index={index}
                  role="option"
                  aria-selected={index === active}
                  className={'search-item' + (index === active ? ' active' : '')}
                  onMouseMove={() => index !== active && setActive(index)}
                  onClick={item.run}
                >
                  <span className="search-icon">{item.icon}</span>
                  <span className="search-label">{item.label}</span>
                  {item.detail && <small className="search-detail">{item.detail}</small>}
                  {index === active && <CornerDownLeft className="search-enter" size={15} />}
                </div>
              ),
            )}
          </div>
        ))}
      </div>
      <div className="search-hint">{t('search.hint')}</div>
    </dialog>
  );
}
