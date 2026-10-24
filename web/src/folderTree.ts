import type { Folder } from './api';

// Library folders nest: helpers for walking them by parent.

const byName = (a: Folder, b: Folder) =>
  a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });

export function childFolders(folders: Folder[], parent: string) {
  return folders.filter((f) => (f.parent_id || '') === parent).sort(byName);
}

// The folder and the folders above it, from the top of the library down.
export function folderChain(folders: Folder[], id: string): Folder[] {
  const chain: Folder[] = [];
  const seen = new Set<string>();
  for (let f = folders.find((x) => x.id === id); f && !seen.has(f.id);) {
    seen.add(f.id);
    chain.unshift(f);
    const parent = f.parent_id;
    f = parent ? folders.find((x) => x.id === parent) : undefined;
  }
  return chain;
}

// "Work / Trip / Day 2"
export function folderLabel(folders: Folder[], id: string) {
  return folderChain(folders, id)
    .map((f) => f.name)
    .join(' / ');
}

// The folder and every folder below it.
export function folderSubtree(folders: Folder[], id: string) {
  const ids = new Set([id]);
  for (let grew = true; grew;) {
    grew = false;
    for (const f of folders)
      if (f.parent_id && ids.has(f.parent_id) && !ids.has(f.id)) {
        ids.add(f.id);
        grew = true;
      }
  }
  return ids;
}

// Every folder depth-first, each under its parent, for pickers. Folders in
// the subtree of leave out (a folder cannot move into itself) are skipped.
export function folderOptions(folders: Folder[], leaveOut?: string) {
  const skip = leaveOut ? folderSubtree(folders, leaveOut) : new Set<string>();
  const out: { folder: Folder; depth: number; label: string }[] = [];
  const walk = (parent: string, depth: number, path: string) => {
    for (const f of childFolders(folders, parent)) {
      if (skip.has(f.id)) continue;
      const label = path ? path + ' / ' + f.name : f.name;
      out.push({ folder: f, depth, label });
      walk(f.id, depth + 1, label);
    }
  };
  walk('', 0, '');
  return out;
}
