import { useEffect, useRef, useState } from 'react';
import { usePaged, ShowMore } from './Paging';
import { ScanNote } from './ScanNote';
import { Select } from './Select';
import FilePreview from './FilePreview';
import { chosenFolder, droppedEntries, type Gathered } from './folderFiles';
import { childFolders, folderChain, folderLabel, folderOptions, folderSubtree } from './folderTree';
import {
  ArrowDownToLine,
  NotebookPen,
  FileArchive,
  FileUp,
  History,
  Folder as FolderIcon,
  FolderInput,
  FolderOpen,
  Files,
  Inbox,
  Pencil,
  Plus,
  Share2,
  Star,
  Trash2,
  Upload,
  X,
  FolderUp,
  LayoutGrid,
  List,
  ListChecks,
} from 'lucide-react';
import {
  api,
  ApiError,
  formatBytes,
  type Me,
  type Folder,
  type LibraryFile,
  type Share,
  type Capabilities,
  type SharedFile,
} from './api';
import {
  uploadAll,
  guardUnload,
  rememberFiles,
  forgetFiles,
  matchFile,
  type UploadStats,
} from './uploader';
import { UploadSpeed } from './UploadSpeed';
import { Modal } from './Modal';
import MoreMenu, { type MenuItem } from './MoreMenu';
import { useLook } from './look';
import FileThumb from './FileThumb';
import Trash, { loadTrash } from './Trash';
import Versions from './Versions';
import Notes from './Notes';
import NoteEditor, { type NoteRef } from './NoteEditor';
import { thumbsFromUpload } from './thumbMaker';
import TextEditor from './TextEditor';
import { useFollow } from './follow';
import LibraryShareDialog from './LibraryShareDialog';
import {
  t,
  useLanguage,
  useMessage,
  message,
  formatNumber,
  formatDate,
  LocalizedError,
} from './i18n';

const safeName = /\.(txt|md|markdown|json|ya?ml|toml|csv|go|py|rs|c|h|cpp|css|jsx?|tsx?|sql|sh)$/i;
const ALL = '*';
const STARRED = '★';
const TRASH = '⌫';
const NOTES = '✎';
const isNote = (name: string) => /\.(md|markdown)$/i.test(name);
// Each view has its own address: /library, /library/starred, /library/trash,
// /library/unfiled and /library/<folder id>.
const folderPath = (id: string) =>
  id === ALL
    ? '/library'
    : id === STARRED
      ? '/library/starred'
      : id === TRASH
        ? '/library/trash'
        : id === NOTES
          ? '/library/notes'
          : '/library/' + (id ? encodeURIComponent(id) : 'unfiled');
function folderFromPath() {
  const segment = location.pathname.match(/^\/library\/([^/]+)/)?.[1];
  if (!segment) return ALL;
  if (segment === 'starred') return STARRED;
  if (segment === 'trash') return TRASH;
  if (segment === 'notes') return NOTES;
  return segment === 'unfiled' ? '' : decodeURIComponent(segment);
}
const UNFILED = '';

interface PendingFile {
  id: string;
  name: string;
  size: number;
  offset: number;
}
interface PendingUpload {
  id: string;
  created: number;
  files: PendingFile[];
}
interface Batch {
  id: string;
  files: { target: PendingFile; source: File }[];
}

function savedView(look: string): 'grid' | 'list' | null {
  try {
    const value = localStorage.getItem('magisend-library-view-' + look);
    return value === 'grid' || value === 'list' ? value : null;
  } catch {
    return null;
  }
}

// Files dragged inside the page carry their ids under this type, so a drop
// on a folder moves them and a drop on the file area never uploads them.
const DRAG_TYPE = 'application/x-magisend-files';

export default function Library({ me, reload }: { me: Me; reload: () => Promise<void> }) {
  useLanguage();
  const [folders, setFolders] = useState<Folder[]>([]);
  const [files, setFiles] = useState<LibraryFile[]>([]);
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [trashCount, setTrashCount] = useState(0);
  const [folder, setFolder] = useState(folderFromPath);
  const [newName, setNewName] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState<{ count: number; stats: UploadStats | null } | null>(
    null,
  );
  const [pending, setPending] = useState<PendingUpload[]>([]);
  const uploadControl = useRef<AbortController | null>(null);
  // Set while the pickers are open to continue an unfinished upload.
  const resuming = useRef(false);
  const [drag, setDrag] = useState(false);
  const [error, setError] = useMessage();
  const [edit, setEdit] = useState<LibraryFile | null>(null);
  const [rename, setRename] = useState<LibraryFile | null>(null);
  const [move, setMove] = useState<LibraryFile | null>(null);
  const [remove, setRemove] = useState<LibraryFile | null>(null);
  // Replacing: the file that gets the next upload as its new version.
  const replaceTarget = useRef<LibraryFile | null>(null);
  const replacePicker = useRef<HTMLInputElement>(null);
  const [versionsOf, setVersionsOf] = useState<{ id: string; name: string } | null>(null);
  // The note being written or read; folder is where a new one goes.
  const [writing, setWriting] = useState<{ note: NoteRef | null; folder: string } | null>(null);
  const [notesVersion, setNotesVersion] = useState(0);
  const noteChanged = useRef(false);
  const [notice, setNotice] = useState('');
  const [renameFolder, setRenameFolder] = useState<Folder | null>(null);
  const [moveFolder, setMoveFolder] = useState<Folder | null>(null);
  const [previewing, setPreviewing] = useState<LibraryFile | null>(null);
  const [bulkMove, setBulkMove] = useState<string | null>(null);
  const [bulkDelete, setBulkDelete] = useState(false);
  const [dropFolder, setDropFolder] = useState<string | null>(null);
  // A file found with search: /library/<folder>?file=<id> shows and selects it.
  const [focusFile, setFocusFile] = useState(() =>
    new URLSearchParams(location.search).get('file'),
  );
  const [found, setFound] = useState<string | null>(null);
  const look = useLook();
  const [views, setViews] = useState<Record<string, 'grid' | 'list'>>({});
  // Classic keeps its list; the other looks remember grid or list per look.
  const view: 'grid' | 'list' =
    look === 'classic'
      ? 'list'
      : views[look] || savedView(look) || (look === 'studio' ? 'list' : 'grid');
  function chooseView(next: 'grid' | 'list') {
    setViews({ ...views, [look]: next });
    try {
      localStorage.setItem('magisend-library-view-' + look, next);
    } catch {}
  }
  const [sharing, setSharing] = useState<{
    share?: Share;
    folder?: string;
    files?: string[];
  } | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const folderPicker = useRef<HTMLInputElement>(null);
  // Not in React's typings: lets the picker choose a whole folder.
  useEffect(() => folderPicker.current?.setAttribute('webkitdirectory', ''), [caps]);

  // While files wait for their virus scan, the list follows.
  const scanning = files.some((f) => f.scan === 'pending');
  const waitingIds = files
    .filter((f) => f.scan === 'pending')
    .map((f) => f.id)
    .join(',');
  useFollow(scanning, () => load(), waitingIds);
  async function load() {
    try {
      const [f, ls, c, unfinished] = await Promise.all([
        api<Folder[]>('/api/folders'),
        api<LibraryFile[]>('/api/library/files'),
        api<Capabilities>('/api/capabilities'),
        api<PendingUpload[]>('/api/library/uploads').catch(() => []),
      ]);
      loadTrash()
        .then((list) => setTrashCount(list.length))
        .catch(() => {});
      setPending(Array.isArray(unfinished) ? unfinished : []);
      setFolders(f);
      // A folder that no longer exists falls back to all files.
      setFolder((current) => {
        if (
          current === ALL ||
          current === STARRED ||
          current === TRASH ||
          current === NOTES ||
          current === '' ||
          f.some((item) => item.id === current)
        )
          return current;
        history.replaceState(null, '', folderPath(ALL));
        return ALL;
      });
      setFiles(ls);
      setCaps(c);
      setSelected((current) => current.filter((id) => ls.some((file) => file.id === id)));
    } catch (e) {
      setError(e as Error);
    }
  }
  useEffect(() => {
    void load();
  }, []);

  async function action(fn: () => Promise<unknown>) {
    setBusy(true);
    setError('');
    try {
      await fn();
      await load();
      await reload();
      setNotesVersion((v) => v + 1);
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }

  // A new folder goes inside the open folder, or at the top of the library.
  async function createFolder() {
    await action(async () => {
      const f = await api<Folder>('/api/folders', 'POST', {
        name: newName,
        parent_id: active?.id ?? '',
      });
      setNewName('');
      openFolder(f.id);
    });
  }

  // Sends the unfinished part of each batch, then publishes the batches so
  // the files appear in the library.
  async function transfer(batches: Batch[]) {
    const count = batches.reduce((n, b) => n + b.files.length, 0);
    const abort = new AbortController();
    uploadControl.current = abort;
    setUploading({ count, stats: null });
    const release = guardUnload(true);
    try {
      await uploadAll(
        batches.flatMap((b) =>
          b.files.map(({ target, source }) => ({
            source,
            size: target.size,
            offset: target.offset,
            url: '/api/uploads/' + target.id,
            status: async (signal: AbortSignal) =>
              (await api<SharedFile>('/api/uploads/' + target.id, 'GET', undefined, signal)).offset,
          })),
        ),
        { signal: abort.signal, onProgress: (stats) => setUploading({ count, stats }) },
      );
      for (const b of batches) {
        await api('/api/shares/' + b.id + '/publish', 'POST', {});
        forgetFiles(b.id);
        thumbsFromUpload(
          b.id,
          b.files.map(({ target, source }) => ({ id: target.id, source })),
        );
      }
    } catch (e) {
      throw (e as Error).name === 'AbortError' ? new LocalizedError(message('errors.paused')) : e;
    } finally {
      release();
      uploadControl.current = null;
      setUploading(null);
    }
  }

  async function upload(incoming: File[], target?: string) {
    if (!incoming.length) return;
    const destination = target ?? (folder === ALL || folder === STARRED ? UNFILED : folder);
    await action(async () => {
      const batches = await uploadGroups([{ files: incoming, target: destination }]);
      // Files added in the Starred view are starred, so they stay in view.
      if (folder === STARRED && target === undefined)
        for (const b of batches)
          for (const { target: file } of b.files)
            await api('/api/library/files/' + file.id + '/star', 'PUT', { starred: true });
    });
    if (picker.current) picker.current.value = '';
  }

  // The new bytes go up as an ordinary upload and are then swapped in, so
  // the file keeps its links. A failed swap leaves the upload as its own file.
  async function replace(target: LibraryFile, source: File) {
    setNotice('');
    await action(async () => {
      const [batch] = await uploadGroups([{ files: [source], target: UNFILED }]);
      await api('/api/library/files/' + target.id + '/replace', 'POST', {
        source: batch.files[0].target.id,
      });
      setNotice(t('library.replaced', { name: target.name }));
    });
  }

  // Unpacks a ZIP into a new folder next to it, then opens that folder.
  async function extract(file: LibraryFile) {
    setNotice('');
    await action(async () => {
      const result = await api<{ folder_id: string; name: string; files: number; skipped: number }>(
        '/api/library/files/' + file.id + '/extract',
        'POST',
        {},
      );
      setNotice(
        t('library.extracted', { count: formatNumber(result.files), name: result.name }) +
          (result.skipped === 1
            ? ' ' + t('library.extractSkippedOne')
            : result.skipped
              ? ' ' + t('library.extractSkipped', { count: formatNumber(result.skipped) })
              : ''),
      );
      openFolder(result.folder_id);
    });
  }

  // Uploads each group of files into its folder.
  async function uploadGroups(groups: { files: File[]; target: string }[]) {
    // The server takes at most 100 files per batch. Every batch is created
    // first, so an interrupted upload can be resumed as a whole.
    const batches: Batch[] = [];
    try {
      for (const group of groups)
        for (let first = 0; first < group.files.length; first += 100) {
          const batch = group.files.slice(first, first + 100);
          const sh = await api<Share>('/api/library/uploads', 'POST', {
            folder_id: group.target,
            files: batch.map((f) => ({ name: f.name, size: f.size })),
          });
          rememberFiles(sh.id, batch);
          batches.push({
            id: sh.id,
            files: sh.files.map((file) => ({
              target: file,
              source: batch.find((f) => f.name === file.name)!,
            })),
          });
        }
    } catch (e) {
      for (const b of batches) {
        await api('/api/shares/' + b.id, 'DELETE', {}).catch(() => {});
        forgetFiles(b.id);
      }
      throw e;
    }
    await transfer(batches);
    return batches;
  }

  // Continues the unfinished uploads with files chosen again.
  async function resume(chosen: File[]) {
    resuming.current = false;
    const missing: string[] = [];
    const batches = pending.map((p) => ({
      id: p.id,
      files: p.files
        .filter((f) => f.offset < f.size)
        .map((target) => {
          const source = matchFile(p.id, target, chosen);
          if (!source) missing.push(target.name);
          return { target, source: source! };
        }),
    }));
    if (picker.current) picker.current.value = '';
    if (folderPicker.current) folderPicker.current.value = '';
    if (missing.length)
      return setError(
        message('library.resumeMissing', {
          count: formatNumber(missing.length),
          names: missing.slice(0, 3).join(', ') + (missing.length > 3 ? ' …' : ''),
        }),
      );
    setPending([]);
    await action(() => transfer(batches));
  }

  async function discardPending() {
    await action(async () => {
      for (const p of pending) {
        await api('/api/shares/' + p.id, 'DELETE', {});
        forgetFiles(p.id);
      }
    });
  }

  // Creates a folder, adding " (2)" and so on when the name is taken.
  async function createFolderNamed(name: string, parent: string) {
    for (let n = 1; n <= 20; n++) {
      try {
        return await api<Folder>('/api/folders', 'POST', {
          name: n === 1 ? name : `${name} (${n})`,
          parent_id: parent,
        });
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 409)) throw e;
      }
    }
    throw new LocalizedError(message('library.folderNameTaken'));
  }

  // A dropped or chosen folder keeps its folders: it becomes a folder inside
  // the open folder (or at the top of the library) with its subfolders.
  async function uploadFolder({ files, tree }: Gathered) {
    if (folderPicker.current) folderPicker.current.value = '';
    if (!files.length) return;
    if (!tree.some((item) => item.dirs.length)) return upload(files);
    const groups = new Map<string, File[]>();
    for (const item of tree) {
      const key = item.dirs.join('/');
      groups.set(key, [...(groups.get(key) ?? []), item.file]);
    }
    const crowded = [...groups.values()].find((list) => list.length > 1000);
    if (crowded) {
      setError(message('library.folderTooMany', { count: formatNumber(crowded.length) }));
      return;
    }
    const parent = active?.id ?? '';
    const paths = new Set<string>();
    for (const item of tree)
      for (let i = 1; i <= item.dirs.length; i++) paths.add(item.dirs.slice(0, i).join('/'));
    // Folders above come first: fewer levels sort earlier.
    const ordered = [...paths].sort((a, b) => a.split('/').length - b.split('/').length);
    await action(async () => {
      const ids = new Map([['', folder === ALL || folder === STARRED ? UNFILED : folder]]);
      for (const path of ordered) {
        const cut = path.lastIndexOf('/');
        const under = cut < 0 ? parent : ids.get(path.slice(0, cut))!;
        ids.set(path, (await createFolderNamed(path.slice(cut + 1), under)).id);
      }
      const tops = ordered.filter((path) => !path.includes('/'));
      if (tops.length === 1) openFolder(ids.get(tops[0])!);
      await uploadGroups(
        [...groups].map(([key, list]) => ({ files: list, target: ids.get(key)! })),
      );
    });
  }

  function openFolder(id: string) {
    if (location.pathname !== folderPath(id)) history.pushState(null, '', folderPath(id));
    setFolder(id);
    setSelected([]);
  }
  useEffect(() => {
    const back = () => {
      if (!location.pathname.startsWith('/library')) return;
      setFolder(folderFromPath());
      setSelected([]);
      setFocusFile(new URLSearchParams(location.search).get('file'));
    };
    addEventListener('popstate', back);
    return () => removeEventListener('popstate', back);
  }, []);

  const inView = (f: LibraryFile, id: string) =>
    id === ALL || (id === STARRED ? !!f.starred : f.folder_id === id);
  const visible = files.filter((f) => inView(f, folder));
  const pagedFiles = usePaged(visible, folder);
  useEffect(() => {
    if (!focusFile || !caps) return;
    const index = visible.findIndex((f) => f.id === focusFile);
    if (index >= pagedFiles.shown.length) return pagedFiles.all();
    if (index >= 0) {
      setSelected([focusFile]);
      setFound(focusFile);
      document
        .querySelector('[data-file-id="' + CSS.escape(focusFile) + '"]')
        ?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
    history.replaceState(null, '', location.pathname);
    setFocusFile(null);
  }, [focusFile, caps, files, folder, pagedFiles.shown.length]);
  useEffect(() => {
    if (!found) return;
    const timer = window.setTimeout(() => setFound(null), 2400);
    return () => window.clearTimeout(timer);
  }, [found]);
  const active = folders.find((f) => f.id === folder);
  const folderName = (id: string) => (id && folderLabel(folders, id)) || t('library.unfiled');
  // A folder counts the files in its subfolders too.
  const countIn = (id: string) => {
    if (!folders.some((f) => f.id === id)) return files.filter((f) => inView(f, id)).length;
    const tree = folderSubtree(folders, id);
    return files.filter((f) => tree.has(f.folder_id)).length;
  };
  // Folders shown above the files: the top ones in All files, otherwise the
  // open folder's own subfolders.
  const subfolders =
    folder === ALL ? childFolders(folders, '') : active ? childFolders(folders, active.id) : [];
  const chain = active ? folderChain(folders, active.id) : [];
  // The sidebar shows the top folders and opens the way down to the open one.
  const openWay = new Set(chain.map((f) => f.id));
  const sidebarFolders: { folder: Folder; depth: number }[] = [];
  const addLevel = (parent: string, depth: number) => {
    for (const f of childFolders(folders, parent)) {
      sidebarFolders.push({ folder: f, depth });
      if (openWay.has(f.id)) addLevel(f.id, depth + 1);
    }
  };
  addLevel('', 0);
  const contents = (id: string) => {
    const inside = childFolders(folders, id).length;
    const count = countIn(id);
    const filesText =
      count === 1 ? t('files.countOne') : t('files.count', { count: formatNumber(count) });
    if (!inside) return filesText;
    return (
      (inside === 1
        ? t('library.folderCountOne')
        : t('library.folderCount', { count: formatNumber(inside) })) +
      ' · ' +
      filesText
    );
  };
  const heading = active
    ? active.name
    : folder === ALL
      ? t('library.all')
      : folder === STARRED
        ? t('library.starred')
        : folder === NOTES
          ? t('notes.all')
          : folder === TRASH
            ? t('trash.title')
            : t('library.unfiled');

  // A bookmark only for this user; the list updates at once.
  async function toggleStar(file: LibraryFile) {
    const starred = !file.starred;
    const mark = (value: boolean) =>
      setFiles((current) => current.map((f) => (f.id === file.id ? { ...f, starred: value } : f)));
    mark(starred);
    try {
      await api('/api/library/files/' + file.id + '/star', 'PUT', { starred });
    } catch (e) {
      mark(!starred);
      setError(e as Error);
    }
  }
  // Moves several files with one request each; the list reloads once.
  function moveFiles(ids: string[], folder_id: string) {
    void action(async () => {
      for (const id of ids) await api('/api/library/files/' + id, 'PATCH', { folder_id });
      setSelected([]);
    });
  }
  function dragFiles(e: React.DragEvent, f: LibraryFile) {
    const ids = selected.includes(f.id) ? selected : [f.id];
    e.dataTransfer.setData(DRAG_TYPE, JSON.stringify(ids));
    e.dataTransfer.effectAllowed = 'move';
  }
  function dropOnFolder(e: React.DragEvent, id: string) {
    setDropFolder(null);
    if (!e.dataTransfer.types.includes(DRAG_TYPE)) return;
    e.preventDefault();
    e.stopPropagation();
    try {
      const ids: unknown = JSON.parse(e.dataTransfer.getData(DRAG_TYPE));
      if (Array.isArray(ids) && id !== ALL && id !== STARRED && id !== TRASH && id !== NOTES)
        moveFiles(
          ids.filter((x): x is string => typeof x === 'string'),
          id,
        );
    } catch {}
  }
  // One file's pieces, shared by the list and the grid.
  // Select all picks every finished file in the open view, also those
  // behind "Show more".
  const selectable = visible.filter((f) => f.offset === f.size).map((f) => f.id);
  const allSelected = selectable.length > 0 && selectable.every((id) => selected.includes(id));
  const selectAll = (className: string) =>
    selectable.length > 1 &&
    !allSelected && (
      <button className={className} disabled={busy} onClick={() => setSelected(selectable)}>
        <ListChecks size={15} />
        {t('library.selectAll', { count: formatNumber(selectable.length) })}
      </button>
    );
  const deleteLabel =
    selected.length === 1
      ? t('library.deleteFile')
      : t('library.deleteFiles', { count: formatNumber(selected.length) });
  const fileCheck = (f: LibraryFile, complete: boolean, className?: string) => (
    <input
      type="checkbox"
      className={className}
      aria-label={t('library.selectNamed', { name: f.name })}
      checked={selected.includes(f.id)}
      disabled={busy || !complete}
      onChange={(e) =>
        setSelected(e.target.checked ? [...selected, f.id] : selected.filter((id) => id !== f.id))
      }
    />
  );
  const fileName = (f: LibraryFile, complete: boolean) => (
    <div className="file-name">
      {complete ? (
        <button
          className="filename-preview"
          onClick={() =>
            isNote(f.name)
              ? setWriting({ note: { id: f.id, name: f.name, auto: f.auto_title }, folder: '' })
              : setPreviewing(f)
          }
        >
          {f.name}
        </button>
      ) : (
        <strong>{f.name}</strong>
      )}
      <small>
        {formatBytes(f.size)} · {complete ? folderName(f.folder_id) : t('share.draft')}
        {f.received &&
          ' · ' +
            (f.received_from
              ? t('library.receivedFrom', { name: f.received_from })
              : t('library.receivedVia', { title: f.received_via || '' }))}
      </small>
      {!!f.shared && (
        <span className="shared-mark">
          <Share2 size={12} aria-hidden="true" />
          {f.shared > 1 ? t('library.sharedCount', { count: f.shared }) : t('library.shared')}
        </span>
      )}
      <ScanNote scan={f.scan} />
    </div>
  );
  const fileActions = (f: LibraryFile, complete: boolean) => (
    <div className="row-actions">
      <button
        className={'icon-button star-button' + (f.starred ? ' active' : '')}
        aria-pressed={!!f.starred}
        aria-label={t('library.starNamed', { name: f.name })}
        title={f.starred ? t('library.unstar') : t('library.star')}
        onClick={() => void toggleStar(f)}
      >
        <Star size={17} fill={f.starred ? 'currentColor' : 'none'} />
      </button>
      {complete && !f.scan && (
        <a
          className="icon-button"
          href={'/api/library/files/' + f.id + '/download'}
          aria-label={t('public.downloadNamed', { name: f.name })}
        >
          <ArrowDownToLine size={17} />
        </a>
      )}
      {safeName.test(f.name) && (
        <button
          className="icon-button"
          aria-label={t('library.editNamed', { name: f.name })}
          disabled={busy || !caps?.edit_own || !complete}
          onClick={() =>
            isNote(f.name)
              ? setWriting({ note: { id: f.id, name: f.name, auto: f.auto_title }, folder: '' })
              : setEdit(f)
          }
        >
          <Pencil size={16} />
        </button>
      )}
      <MoreMenu label={t('library.moreNamed', { name: f.name })} items={fileMenu(f, complete)} />
    </div>
  );
  // A note in the notes list is written, not replaced by an upload.
  const fileMenu = (f: LibraryFile, complete: boolean, note = false): MenuItem[] => [
    {
      label: t('library.rename'),
      icon: <Pencil size={15} />,
      disabled: busy || !caps?.folders || !complete,
      onSelect: () => setRename({ ...f }),
    },
    {
      label: t('library.move'),
      icon: <FolderInput size={15} />,
      disabled: busy || !caps?.folders || !complete,
      onSelect: () => setMove({ ...f }),
    },
    ...(note
      ? []
      : [
          {
            label: t('library.replace'),
            icon: <FileUp size={15} />,
            disabled: busy || !caps?.edit_own || !caps?.upload || !complete,
            onSelect: () => {
              replaceTarget.current = f;
              replacePicker.current?.click();
            },
          },
        ]),
    ...(/\.(zip|tar|tgz|tbz2?|tar\.gz|tar\.bz2)$/i.test(f.name)
      ? [
          {
            label: t('library.extract'),
            icon: <FileArchive size={15} />,
            disabled: busy || !caps?.folders || !caps?.upload || !complete,
            onSelect: () => void extract(f),
          },
        ]
      : []),
    {
      label: t('library.history'),
      icon: <History size={15} />,
      disabled: !complete,
      onSelect: () => setVersionsOf(f),
    },
    {
      label: t('library.deleteFile'),
      icon: <Trash2 size={15} />,
      danger: true,
      disabled: busy || !caps?.edit_own,
      onSelect: () => setRemove(f),
    },
  ];
  const folderDrop = (id: string) => ({
    onDragOver: (e: React.DragEvent) => {
      if (!caps?.folders || !e.dataTransfer.types.includes(DRAG_TYPE)) return;
      e.preventDefault();
      setDropFolder(id);
    },
    onDragLeave: () => setDropFolder(null),
    onDrop: (e: React.DragEvent) => dropOnFolder(e, id),
  });
  const canUpload = !!caps?.upload && !busy;
  const unfinishedCount = pending.reduce(
    (n, p) => n + p.files.filter((f) => f.offset < f.size).length,
    0,
  );
  const unfinishedLeft = pending.reduce(
    (n, p) => n + p.files.reduce((m, f) => m + f.size - f.offset, 0),
    0,
  );

  return (
    <>
      <header className="page-heading">
        <div>
          <div className="eyebrow">{t('library.step')}</div>
          <h1>{t('library.title')}</h1>
          <p>{t('library.privateHint')}</p>
        </div>
        <div className="page-actions">
          {canUpload && (
            <button
              className="secondary"
              onClick={() =>
                setWriting({
                  note: null,
                  folder: folders.some((f) => f.id === folder) ? folder : '',
                })
              }
            >
              <NotebookPen size={17} />
              {t('notes.new')}
            </button>
          )}
          {caps?.folders && (
            <button
              className="secondary"
              disabled={!canUpload}
              onClick={() => {
                resuming.current = false;
                folderPicker.current?.click();
              }}
            >
              <FolderUp size={17} />
              {t('library.uploadFolder')}
            </button>
          )}
          <input
            ref={folderPicker}
            className="sr-only"
            type="file"
            aria-label={t('library.uploadFolder')}
            disabled={!canUpload}
            onChange={(e) =>
              void (resuming.current
                ? resume(chosenFolder(e.target.files).files)
                : uploadFolder(chosenFolder(e.target.files)))
            }
          />
          <button
            className="primary"
            disabled={!canUpload}
            onClick={() => {
              resuming.current = false;
              picker.current?.click();
            }}
          >
            <Upload size={17} />
            {t('library.upload')}
          </button>
          <input
            ref={picker}
            className="sr-only"
            type="file"
            multiple
            aria-label={t('library.uploadFiles')}
            disabled={!canUpload}
            onChange={(e) => {
              const chosen = Array.from(e.target.files || []);
              void (resuming.current ? resume(chosen) : upload(chosen));
            }}
          />
          <input
            ref={replacePicker}
            className="sr-only"
            type="file"
            tabIndex={-1}
            aria-hidden="true"
            disabled={!canUpload}
            onChange={(e) => {
              const chosen = e.target.files?.[0];
              const target = replaceTarget.current;
              e.target.value = '';
              if (chosen && target) void replace(target, chosen);
            }}
          />
        </div>
      </header>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notice && (
        <div role="status" className="success-note">
          {notice}
        </div>
      )}
      <div className="library-layout">
        <aside className="panel folder-pane">
          <div className="pane-label">{t('library.folders')}</div>
          <nav className="folder-list" aria-label={t('library.folders')}>
            {[
              { id: ALL, name: t('library.all'), icon: <Files size={16} /> },
              { id: STARRED, name: t('library.starred'), icon: <Star size={16} /> },
              { id: NOTES, name: t('notes.all'), icon: <NotebookPen size={16} /> },
              { id: UNFILED, name: t('library.unfiled'), icon: <Inbox size={16} /> },
              ...sidebarFolders.map(({ folder: f, depth }) => ({
                id: f.id,
                name: f.name,
                depth,
                icon: openWay.has(f.id) ? <FolderOpen size={16} /> : <FolderIcon size={16} />,
              })),
              { id: TRASH, name: t('trash.title'), icon: <Trash2 size={16} /> },
            ].map((item) => (
              <button
                key={item.id || 'unfiled'}
                style={
                  'depth' in item && item.depth
                    ? ({ '--depth': item.depth } as React.CSSProperties)
                    : undefined
                }
                className={
                  'folder-item' +
                  (folder === item.id ? ' active' : '') +
                  (dropFolder === item.id ? ' drop-target' : '')
                }
                aria-pressed={folder === item.id}
                onClick={() => openFolder(item.id)}
                onDragOver={(e) => {
                  if (
                    !caps?.folders ||
                    item.id === ALL ||
                    item.id === STARRED ||
                    item.id === TRASH ||
                    item.id === NOTES ||
                    !e.dataTransfer.types.includes(DRAG_TYPE)
                  )
                    return;
                  e.preventDefault();
                  setDropFolder(item.id);
                }}
                onDragLeave={() => setDropFolder(null)}
                onDrop={(e) => dropOnFolder(e, item.id)}
              >
                {item.icon}
                <span>{item.name}</span>
                <span className="count" aria-hidden="true">
                  {formatNumber(
                    item.id === TRASH
                      ? trashCount
                      : item.id === NOTES
                        ? files.filter((f) => isNote(f.name) && f.offset === f.size).length
                        : countIn(item.id),
                  )}
                </span>
              </button>
            ))}
          </nav>
          {caps?.folders && (
            <form
              className="new-folder"
              onSubmit={(e) => {
                e.preventDefault();
                void createFolder();
              }}
            >
              <input
                aria-label={t('library.newFolder')}
                placeholder={
                  active
                    ? t('library.newFolderIn', { name: active.name })
                    : t('library.newFolderPlaceholder')
                }
                value={newName}
                maxLength={100}
                onChange={(e) => setNewName(e.target.value)}
              />
              <button
                className="icon-button"
                aria-label={t('library.createFolder')}
                disabled={busy || !newName.trim()}
              >
                <Plus size={17} />
              </button>
            </form>
          )}
        </aside>
        <div className="library-main">
          {folder === NOTES ? (
            <section className="panel library-files">
              <Notes
                version={notesVersion}
                folders={folders}
                menu={(note) => {
                  const f: LibraryFile = {
                    id: note.id,
                    name: note.name,
                    size: note.size,
                    offset: note.size,
                    backend: '',
                    has_thumb: false,
                    folder_id: note.folder_id || UNFILED,
                    share_id: note.share_id,
                    starred: note.starred,
                    auto_title: note.auto,
                  };
                  return [
                    {
                      label: f.starred ? t('library.unstar') : t('library.star'),
                      icon: <Star size={15} />,
                      onSelect: () =>
                        void api('/api/library/files/' + f.id + '/star', 'PUT', {
                          starred: !f.starred,
                        })
                          .then(() => setNotesVersion((v) => v + 1))
                          .catch((e) => setError(e as Error)),
                    },
                    {
                      label: t('notes.share'),
                      icon: <Share2 size={15} />,
                      disabled: busy || !caps?.share,
                      onSelect: () => setSharing({ files: [f.id] }),
                    },
                    ...fileMenu(f, true, true),
                  ];
                }}
                open={(note) => setWriting({ note, folder: '' })}
                create={canUpload ? () => setWriting({ note: null, folder: '' }) : undefined}
              />
            </section>
          ) : folder === TRASH ? (
            <section className="panel library-files">
              <Trash
                folders={folders}
                editable={!!caps?.edit_own}
                changed={async () => {
                  await load();
                }}
              />
            </section>
          ) : (
            <>
              <section
                className={'panel library-files' + (drag ? ' dragging' : '')}
                onDragOver={(e) => {
                  if (!canUpload || e.dataTransfer.types.includes(DRAG_TYPE)) return;
                  e.preventDefault();
                  setDrag(true);
                }}
                onDragLeave={() => setDrag(false)}
                onDrop={(e) => {
                  e.preventDefault();
                  setDrag(false);
                  if (!canUpload || e.dataTransfer.types.includes(DRAG_TYPE)) return;
                  const gather = droppedEntries(e.dataTransfer);
                  void gather().then(uploadFolder);
                }}
              >
                {selected.length && look === 'classic' ? (
                  <div className="library-bar selecting">
                    <h2>{t('library.selected', { count: formatNumber(selected.length) })}</h2>
                    <div className="row-actions">
                      {selectAll('quiet small')}
                      <button
                        className="primary small"
                        disabled={busy || !caps?.share}
                        onClick={() => setSharing({ files: selected })}
                      >
                        <Share2 size={15} />
                        {t('library.shareSelection')}
                      </button>
                      <button className="quiet small" onClick={() => setSelected([])}>
                        <X size={15} />
                        {t('library.clearSelection')}
                      </button>
                    </div>
                  </div>
                ) : (
                  <div className="library-bar">
                    <h2 className="folder-crumbs">
                      {chain.slice(0, -1).map((f) => (
                        <span key={f.id}>
                          <button className="crumb" onClick={() => openFolder(f.id)}>
                            {f.name}
                          </button>
                          <span className="crumb-sep" aria-hidden="true">
                            /
                          </span>
                        </span>
                      ))}
                      {heading}
                    </h2>
                    {selectAll('quiet small select-all')}
                    {look !== 'classic' && (
                      <div className="view-switch" role="group" aria-label={t('library.view')}>
                        <button
                          aria-pressed={view === 'grid'}
                          aria-label={t('library.viewGrid')}
                          title={t('library.viewGrid')}
                          onClick={() => chooseView('grid')}
                        >
                          <LayoutGrid size={16} />
                        </button>
                        <button
                          aria-pressed={view === 'list'}
                          aria-label={t('library.viewList')}
                          title={t('library.viewList')}
                          onClick={() => chooseView('list')}
                        >
                          <List size={16} />
                        </button>
                      </div>
                    )}
                    {active && (
                      <div className="row-actions">
                        <button
                          className="secondary small"
                          disabled={busy || !caps?.share}
                          onClick={() => setSharing({ folder: active.id })}
                        >
                          <Share2 size={15} />
                          {t('library.shareFolder')}
                        </button>
                        <MoreMenu
                          label={t('library.folderActions', { name: active.name })}
                          items={[
                            {
                              label: t('library.renameFolder'),
                              icon: <Pencil size={15} />,
                              disabled: busy || !caps?.folders,
                              onSelect: () => setRenameFolder({ ...active }),
                            },
                            {
                              label: t('library.moveFolder'),
                              icon: <FolderInput size={15} />,
                              disabled: busy || !caps?.folders,
                              onSelect: () =>
                                setMoveFolder({ ...active, parent_id: active.parent_id || '' }),
                            },
                            {
                              label: t('library.deleteFolder'),
                              icon: <Trash2 size={15} />,
                              danger: true,
                              disabled:
                                busy ||
                                !caps?.folders ||
                                subfolders.length > 0 ||
                                files.some((f) => f.folder_id === active.id),
                              onSelect: () =>
                                void action(async () => {
                                  await api('/api/folders/' + active.id, 'DELETE', {});
                                  openFolder(active.parent_id || ALL);
                                }),
                            },
                          ]}
                        />
                      </div>
                    )}
                  </div>
                )}
                {uploading && (
                  <div className="upload-status" role="status">
                    <div className="upload-status-row">
                      <span>
                        {uploading.count === 1
                          ? t('library.uploadingOne')
                          : t('library.uploading', { count: formatNumber(uploading.count) })}
                      </span>
                      <button
                        className="quiet small"
                        onClick={() => uploadControl.current?.abort()}
                      >
                        {t('upload.pause')}
                      </button>
                    </div>
                    <progress
                      aria-label={t('upload.uploading')}
                      value={
                        uploading.stats?.total
                          ? Math.round((uploading.stats.sent / uploading.stats.total) * 100)
                          : 0
                      }
                      max={100}
                    />
                    <UploadSpeed stats={uploading.stats} />
                  </div>
                )}
                {!uploading && pending.length > 0 && (
                  <div className="upload-status unfinished" role="status">
                    <strong>
                      {t(unfinishedCount === 1 ? 'library.unfinishedOne' : 'library.unfinished', {
                        count: formatNumber(unfinishedCount),
                        size: formatBytes(unfinishedLeft),
                      })}
                    </strong>
                    <span className="muted">{t('library.unfinishedHint')}</span>
                    <div className="row-actions">
                      <button
                        className="secondary small"
                        disabled={!canUpload}
                        onClick={() => {
                          resuming.current = true;
                          picker.current?.click();
                        }}
                      >
                        <Upload size={15} />
                        {t('library.resumeFiles')}
                      </button>
                      {caps?.folders && (
                        <button
                          className="secondary small"
                          disabled={!canUpload}
                          onClick={() => {
                            resuming.current = true;
                            folderPicker.current?.click();
                          }}
                        >
                          <FolderUp size={15} />
                          {t('library.resumeFolder')}
                        </button>
                      )}
                      <button
                        className="quiet small danger"
                        disabled={busy}
                        onClick={() => void discardPending()}
                      >
                        <Trash2 size={15} />
                        {t('library.discardUnfinished')}
                      </button>
                    </div>
                  </div>
                )}
                {visible.length === 0 && subfolders.length === 0 ? (
                  <div className="library-empty">
                    {folder === STARRED ? <Star size={28} /> : <Upload size={28} />}
                    <strong>
                      {folder === STARRED ? t('library.noStarred') : t('library.empty')}
                    </strong>
                    <span>
                      {folder === STARRED ? t('library.noStarredHint') : t('library.emptyHint')}
                    </span>
                  </div>
                ) : view === 'grid' ? (
                  <div className="library-grid">
                    {subfolders.map((fd) => (
                      <button
                        key={fd.id}
                        className={'folder-tile' + (dropFolder === fd.id ? ' drop-target' : '')}
                        onClick={() => openFolder(fd.id)}
                        {...folderDrop(fd.id)}
                      >
                        <span className="tile-preview">
                          <FolderIcon size={32} />
                        </span>
                        <span className="tile-info">
                          <strong>{fd.name}</strong>
                          <small>{contents(fd.id)}</small>
                        </span>
                      </button>
                    ))}
                    {pagedFiles.shown.map((f) => {
                      const complete = f.offset === f.size;
                      return (
                        <div
                          className={
                            'file-tile' +
                            (selected.includes(f.id) ? ' selected' : '') +
                            (found === f.id ? ' just-found' : '')
                          }
                          key={f.id}
                          data-file-id={f.id}
                          draggable={complete && !!caps?.folders}
                          onDragStart={(e) => dragFiles(e, f)}
                        >
                          <div className="tile-preview">
                            {complete ? (
                              <FileThumb shareId={f.share_id} file={f} owner />
                            ) : (
                              <span className="file-icon">
                                <Upload size={17} />
                              </span>
                            )}
                            {fileCheck(f, complete, 'tile-check')}
                          </div>
                          <div className="tile-info">{fileName(f, complete)}</div>
                          {fileActions(f, complete)}
                        </div>
                      );
                    })}
                  </div>
                ) : (
                  <div className="library-list">
                    {subfolders.map((fd) => (
                      <div
                        key={fd.id}
                        className={
                          'file-row library-row folder-row' +
                          (dropFolder === fd.id ? ' drop-target' : '')
                        }
                        {...folderDrop(fd.id)}
                      >
                        <span className="file-icon folder-icon">
                          <FolderIcon size={19} />
                        </span>
                        <div className="file-name">
                          <button className="filename-preview" onClick={() => openFolder(fd.id)}>
                            {fd.name}
                          </button>
                          <small>{contents(fd.id)}</small>
                        </div>
                      </div>
                    ))}
                    {pagedFiles.shown.map((f) => {
                      const complete = f.offset === f.size;
                      return (
                        <div
                          className={'file-row library-row' + (found === f.id ? ' just-found' : '')}
                          key={f.id}
                          data-file-id={f.id}
                          draggable={look !== 'classic' && complete && !!caps?.folders}
                          onDragStart={(e) => dragFiles(e, f)}
                        >
                          {fileCheck(f, complete)}
                          {complete ? (
                            <FileThumb shareId={f.share_id} file={f} owner />
                          ) : (
                            <span className="file-icon">
                              <Upload size={17} />
                            </span>
                          )}
                          {fileName(f, complete)}
                          {fileActions(f, complete)}
                        </div>
                      );
                    })}
                  </div>
                )}
                {look !== 'classic' && selected.length > 0 && (
                  <div className="selection-bar" role="region" aria-label={t('library.selection')}>
                    <strong>
                      {t('library.selected', { count: formatNumber(selected.length) })}
                    </strong>
                    {selectAll('quiet small')}
                    <button
                      className="primary small"
                      disabled={busy || !caps?.share}
                      onClick={() => setSharing({ files: selected })}
                    >
                      <Share2 size={15} />
                      {t('library.shareSelection')}
                    </button>
                    {caps?.folders && (
                      <button
                        className="quiet small"
                        disabled={busy}
                        onClick={() => setBulkMove(UNFILED)}
                      >
                        <FolderInput size={15} />
                        {t('library.move')}
                      </button>
                    )}
                    {caps?.edit_own && (
                      <button
                        className="quiet small danger"
                        disabled={busy}
                        onClick={() => setBulkDelete(true)}
                      >
                        <Trash2 size={15} />
                        {deleteLabel}
                      </button>
                    )}
                    <button
                      className="icon-button"
                      aria-label={t('library.clearSelection')}
                      title={t('library.clearSelection')}
                      onClick={() => setSelected([])}
                    >
                      <X size={16} />
                    </button>
                  </div>
                )}
                <ShowMore paged={pagedFiles} />
                <p className="library-foot">
                  {caps && !caps.edit_own ? t('library.editingOff') + ' ' : ''}
                  {t('library.maxText')}
                </p>
              </section>
            </>
          )}
        </div>
      </div>
      {writing && (
        <NoteEditor
          note={writing.note}
          folder={writing.folder}
          editable={!!caps?.edit_own}
          changed={() => (noteChanged.current = true)}
          close={() => {
            setWriting(null);
            if (noteChanged.current) {
              noteChanged.current = false;
              setNotesVersion((v) => v + 1);
              void load();
            }
          }}
          share={caps?.share ? (id) => setSharing({ files: [id] }) : undefined}
          history={(note) => setVersionsOf(note)}
          remove={(id) =>
            void action(async () => {
              await api('/api/library/files/' + id, 'DELETE', {});
              setWriting(null);
              setNotesVersion((v) => v + 1);
            })
          }
        />
      )}
      {versionsOf && (
        <Versions
          file={versionsOf}
          editable={!!caps?.edit_own}
          close={() => setVersionsOf(null)}
          changed={load}
        />
      )}
      {edit && (
        <TextEditor
          base={'/api/library/files/' + edit.id}
          name={edit.name}
          editable={!!caps?.edit_own}
          close={() => setEdit(null)}
          changed={() => void load()}
        />
      )}
      {sharing && caps && (
        <LibraryShareDialog
          {...sharing}
          daysDefault={me.policy.default_days}
          caps={caps}
          close={() => setSharing(null)}
          changed={() => void load()}
        />
      )}
      {previewing && (
        <FilePreview
          shareId=""
          file={previewing}
          library
          files={visible.filter((f) => f.offset === f.size)}
          show={(f) => setPreviewing(f as LibraryFile)}
          close={() => setPreviewing(null)}
        />
      )}
      {rename && (
        <Modal title={t('library.rename')} close={() => setRename(null)}>
          <form
            className="form-stack"
            onSubmit={(e) => {
              e.preventDefault();
              void action(async () => {
                await api('/api/library/files/' + rename.id, 'PATCH', { name: rename.name });
                setRename(null);
              });
            }}
          >
            <label>
              {t('library.fileName')}
              <input
                value={rename.name}
                onChange={(e) => setRename({ ...rename, name: e.target.value })}
                maxLength={240}
              />
            </label>
            <div className="dialog-actions">
              <button type="button" className="secondary" onClick={() => setRename(null)}>
                {t('common.cancel')}
              </button>
              <button className="primary" disabled={busy || !rename.name.trim()}>
                {t('common.save')}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {move && (
        <Modal title={t('library.moveTitle')} close={() => setMove(null)}>
          <form
            className="form-stack"
            onSubmit={(e) => {
              e.preventDefault();
              void action(async () => {
                await api('/api/library/files/' + move.id, 'PATCH', { folder_id: move.folder_id });
                setMove(null);
              });
            }}
          >
            <Select
              label={t('library.move')}
              ariaLabel={t('library.moveNamed', { name: move.name })}
              value={move.folder_id}
              onChange={(folder_id) => setMove({ ...move, folder_id })}
              options={[
                { value: UNFILED, label: t('library.unfiled'), icon: <Inbox size={16} /> },
                ...folderOptions(folders).map(({ folder: f, label }) => ({
                  value: f.id,
                  label,
                  icon: <FolderIcon size={16} />,
                })),
              ]}
            />
            <div className="dialog-actions">
              <button type="button" className="secondary" onClick={() => setMove(null)}>
                {t('common.cancel')}
              </button>
              <button className="primary" disabled={busy}>
                {t('library.moveAction')}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {renameFolder && (
        <Modal title={t('library.renameFolder')} close={() => setRenameFolder(null)}>
          <form
            className="form-stack"
            onSubmit={(e) => {
              e.preventDefault();
              void action(async () => {
                await api('/api/folders/' + renameFolder.id, 'PATCH', {
                  name: renameFolder.name,
                });
                setRenameFolder(null);
              });
            }}
          >
            <label>
              {t('library.folderName')}
              <input
                value={renameFolder.name}
                maxLength={100}
                onChange={(e) => setRenameFolder({ ...renameFolder, name: e.target.value })}
              />
            </label>
            <div className="dialog-actions">
              <button type="button" className="secondary" onClick={() => setRenameFolder(null)}>
                {t('common.cancel')}
              </button>
              <button className="primary" disabled={busy || !renameFolder.name.trim()}>
                {t('common.save')}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {moveFolder && (
        <Modal title={t('library.moveFolder')} close={() => setMoveFolder(null)}>
          <form
            className="form-stack"
            onSubmit={(e) => {
              e.preventDefault();
              void action(async () => {
                await api('/api/folders/' + moveFolder.id, 'PATCH', {
                  parent_id: moveFolder.parent_id,
                });
                setMoveFolder(null);
              });
            }}
          >
            <Select
              label={t('library.moveFolderTo')}
              ariaLabel={t('library.moveNamed', { name: moveFolder.name })}
              value={moveFolder.parent_id || ''}
              onChange={(parent_id) => setMoveFolder({ ...moveFolder, parent_id })}
              options={[
                { value: '', label: t('library.topLevel'), icon: <Files size={16} /> },
                ...folderOptions(folders, moveFolder.id).map(({ folder: f, label }) => ({
                  value: f.id,
                  label,
                  icon: <FolderIcon size={16} />,
                })),
              ]}
            />
            <div className="dialog-actions">
              <button type="button" className="secondary" onClick={() => setMoveFolder(null)}>
                {t('common.cancel')}
              </button>
              <button className="primary" disabled={busy}>
                {t('library.moveAction')}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {bulkMove !== null && (
        <Modal title={t('library.moveSelected')} close={() => setBulkMove(null)}>
          <form
            className="form-stack"
            onSubmit={(e) => {
              e.preventDefault();
              moveFiles(selected, bulkMove);
              setBulkMove(null);
            }}
          >
            <Select
              label={t('library.move')}
              value={bulkMove}
              onChange={setBulkMove}
              options={[
                { value: UNFILED, label: t('library.unfiled'), icon: <Inbox size={16} /> },
                ...folderOptions(folders).map(({ folder: f, label }) => ({
                  value: f.id,
                  label,
                  icon: <FolderIcon size={16} />,
                })),
              ]}
            />
            <div className="dialog-actions">
              <button type="button" className="secondary" onClick={() => setBulkMove(null)}>
                {t('common.cancel')}
              </button>
              <button className="primary" disabled={busy}>
                {t('library.moveAction')}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {bulkDelete && (
        <Modal title={deleteLabel} close={() => setBulkDelete(false)}>
          <p>
            {selected.length === 1
              ? t('library.deleteSelectedWarningOne')
              : t('library.deleteSelectedWarning', { count: formatNumber(selected.length) })}
          </p>
          <div className="dialog-actions">
            <button className="secondary" onClick={() => setBulkDelete(false)}>
              {t('common.cancel')}
            </button>
            <button
              className="primary danger-solid"
              disabled={busy}
              onClick={() =>
                void action(async () => {
                  for (const id of selected) await api('/api/library/files/' + id, 'DELETE', {});
                  setSelected([]);
                  setBulkDelete(false);
                })
              }
            >
              <Trash2 size={16} />
              {deleteLabel}
            </button>
          </div>
        </Modal>
      )}
      {remove && (
        <Modal title={t('library.deleteFile')} close={() => setRemove(null)}>
          <p>{t('library.deleteWarning')}</p>
          <div className="dialog-actions">
            <button className="secondary" onClick={() => setRemove(null)}>
              {t('common.cancel')}
            </button>
            <button
              className="primary danger-solid"
              disabled={busy}
              onClick={() =>
                void action(async () => {
                  await api('/api/library/files/' + remove.id, 'DELETE', {});
                  setRemove(null);
                })
              }
            >
              <Trash2 size={16} />
              {t('library.deleteFile')}
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
