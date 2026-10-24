// Files from a dropped or chosen folder. `files` is every file in one flat
// list (for sending); `tree` keeps each file's folders from the drop, which
// the library recreates. A clashing name gets " (2)" before its extension.
// System clutter such as .DS_Store is left out.
export interface TreeFile {
  file: File;
  // The folders from the dropped item down, such as ["Trip", "day2"].
  dirs: string[];
}
export interface Gathered {
  files: File[];
  // The folder's name when exactly one folder was dropped or chosen.
  folder: string | null;
  tree: TreeFile[];
}

const clutter = new Set(['.ds_store', 'thumbs.db', 'desktop.ini']);
const wanted = (file: File) => !clutter.has(file.name.toLowerCase());

export function uniqueNames(files: File[]): File[] {
  const used = new Set<string>();
  return files.map((file) => {
    let name = file.name;
    const dot = name.lastIndexOf('.') > 0 ? name.lastIndexOf('.') : name.length;
    for (let n = 2; used.has(name.toLowerCase()); n++)
      name = file.name.slice(0, dot) + ` (${n})` + file.name.slice(dot);
    used.add(name.toLowerCase());
    return name === file.name
      ? file
      : new File([file], name, { type: file.type, lastModified: file.lastModified });
  });
}

// Names only need to differ within each folder.
function uniqueTree(tree: TreeFile[]): TreeFile[] {
  const groups = new Map<string, TreeFile[]>();
  for (const item of tree) {
    const key = item.dirs.join('/');
    groups.set(key, [...(groups.get(key) || []), item]);
  }
  return [...groups.values()].flatMap((items) => {
    const named = uniqueNames(items.map((item) => item.file));
    return items.map((item, i) => ({ file: named[i], dirs: item.dirs }));
  });
}

function gathered(tree: TreeFile[], folder: string | null): Gathered {
  const kept = tree.filter((item) => wanted(item.file));
  return {
    files: uniqueNames(kept.map((item) => item.file)),
    folder,
    tree: uniqueTree(kept),
  };
}

function entryFiles(entry: FileSystemEntry, dirs: string[]): Promise<TreeFile[]> {
  if (entry.isFile)
    return new Promise((resolve) =>
      (entry as FileSystemFileEntry).file(
        (file) => resolve([{ file, dirs }]),
        () => resolve([]),
      ),
    );
  const inside = [...dirs, entry.name];
  const reader = (entry as FileSystemDirectoryEntry).createReader();
  return new Promise((resolve) => {
    const all: TreeFile[] = [];
    // readEntries returns the listing in batches until an empty one.
    const next = () =>
      reader.readEntries(
        async (batch) => {
          if (!batch.length) return resolve(all);
          for (const child of batch) all.push(...(await entryFiles(child, inside)));
          next();
        },
        () => resolve(all),
      );
    next();
  });
}

// Call synchronously inside the drop handler: the browser empties the
// DataTransfer once the event is over.
export function droppedEntries(transfer: DataTransfer) {
  const entries = Array.from(transfer.items || [])
    .map((item) => item.webkitGetAsEntry?.() ?? null)
    .filter((entry): entry is FileSystemEntry => !!entry);
  const plain = Array.from(transfer.files);
  return async (): Promise<Gathered> => {
    if (!entries.some((entry) => entry.isDirectory))
      return gathered(
        plain.map((file) => ({ file, dirs: [] })),
        null,
      );
    const tree: TreeFile[] = [];
    for (const entry of entries) tree.push(...(await entryFiles(entry, [])));
    const folder = entries.length === 1 && entries[0].isDirectory ? entries[0].name : null;
    return gathered(tree, folder);
  };
}

// From an <input webkitdirectory>: every file carries its relative path.
export function chosenFolder(list: FileList | null): Gathered {
  const files = Array.from(list || []);
  const top = files[0]?.webkitRelativePath.split('/')[0] || null;
  return gathered(
    files.map((file) => ({ file, dirs: file.webkitRelativePath.split('/').slice(0, -1) })),
    top,
  );
}
