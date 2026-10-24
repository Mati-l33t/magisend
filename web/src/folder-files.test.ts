import { expect, it } from 'vitest';
import { chosenFolder, uniqueNames } from './folderFiles';

function picked(path: string, body = 'x') {
  const file = new File([body], path.split('/').pop()!);
  Object.defineProperty(file, 'webkitRelativePath', { value: path });
  return file;
}

it('flattens a chosen folder, keeps every file and drops system clutter', () => {
  const list = [
    picked('Holiday/beach.png'),
    picked('Holiday/day2/beach.png'),
    picked('Holiday/day2/BEACH.png'),
    picked('Holiday/notes'),
    picked('Holiday/.DS_Store'),
    picked('Holiday/day2/Thumbs.db'),
  ];
  const gathered = chosenFolder(list as unknown as FileList);
  expect(gathered.folder).toBe('Holiday');
  expect(gathered.files.map((f) => f.name)).toEqual([
    'beach.png',
    'beach (2).png',
    'BEACH (3).png',
    'notes',
  ]);
});

it('renames only clashing names and keeps their content', async () => {
  const [a, b] = uniqueNames([new File(['one'], 'a.txt'), new File(['two'], 'a.txt')]);
  expect([a.name, b.name]).toEqual(['a.txt', 'a (2).txt']);
  expect(await b.text()).toBe('two');
  expect(uniqueNames([new File([''], '.env'), new File([''], '.env')]).map((f) => f.name)).toEqual([
    '.env',
    '.env (2)',
  ]);
});

it('keeps each file in its folder for the library', () => {
  const list = [
    picked('Holiday/beach.png'),
    picked('Holiday/day2/beach.png'),
    picked('Holiday/day2/BEACH.png'),
    picked('Holiday/day2/.DS_Store'),
  ];
  const { tree } = chosenFolder(list as unknown as FileList);
  expect(tree.map((item) => item.dirs.join('/') + '|' + item.file.name)).toEqual([
    'Holiday|beach.png',
    'Holiday/day2|beach.png',
    'Holiday/day2|BEACH (2).png',
  ]);
});
