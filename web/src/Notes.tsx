import { useEffect, useState } from 'react';
import { Check, Folder as FolderIcon, Inbox, NotebookPen, Plus, Search, Star } from 'lucide-react';
import { api, type Folder } from './api';
import { folderLabel } from './folderTree';
import { formatDate, t, useLanguage, useMessage } from './i18n';
import MoreMenu, { type MenuItem } from './MoreMenu';
import { noteTitle, type NoteRef } from './NoteEditor';

export interface Note {
  id: string;
  name: string;
  size: number;
  folder_id: string;
  share_id: string;
  modified: number;
  auto: boolean;
  starred: boolean;
  excerpt: string;
}

// The opening of a note as plain words: Markdown signs and the line the
// title came from are left out, and checklist items keep their box.
function preview(note: Note) {
  const title = noteTitle(note.name).toLocaleLowerCase();
  return note.excerpt
    .split('\n')
    .map((raw) => {
      let line = raw.replace(/^\s*(#{1,6}\s+|[-*+]\s+|>\s*|\d+[.)]\s+)/, '');
      let task: 'open' | 'done' | undefined;
      const box = /^\[( |x|X)\]\s+/.exec(line);
      if (box) {
        task = box[1] === ' ' ? 'open' : 'done';
        line = line.slice(box[0].length);
      }
      return { text: line.replace(/[*_`~]/g, '').trim(), task };
    })
    .filter((line, i) => line.text && !(i === 0 && line.text.toLocaleLowerCase() === title));
}

// All of a person's notes, the most recently changed first.
export default function Notes({
  version,
  folders = [],
  menu,
  open,
  create,
}: {
  // Changes when a note was saved, so the list reloads.
  version: number;
  folders?: Folder[];
  menu?: (note: Note) => MenuItem[];
  open: (note: NoteRef) => void;
  create?: () => void;
}) {
  useLanguage();
  const [notes, setNotes] = useState<Note[] | null>(null);
  const [query, setQuery] = useState('');
  const [error, setError] = useMessage();
  useEffect(() => {
    api<Note[]>('/api/library/notes')
      .then((list) => setNotes(Array.isArray(list) ? list : []))
      .catch((e) => setError(e as Error));
  }, [version]);
  const words = query.trim().toLocaleLowerCase();
  const shown = (notes ?? []).filter(
    (n) => !words || (n.name + ' ' + n.excerpt).toLocaleLowerCase().includes(words),
  );
  return (
    <>
      <div className="library-bar">
        <h2>{t('notes.all')}</h2>
        {create && (
          <div className="row-actions">
            <button className="primary small" onClick={create}>
              <Plus size={15} />
              {t('notes.new')}
            </button>
          </div>
        )}
      </div>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notes && notes.length > 3 && (
        <label className="notes-search">
          <Search size={16} />
          <input
            type="search"
            aria-label={t('notes.search')}
            placeholder={t('notes.search')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
      )}
      {notes && shown.length === 0 ? (
        <div className="library-empty">
          <NotebookPen size={28} />
          <strong>{notes.length ? t('notes.noMatch') : t('notes.none')}</strong>
          {!notes.length && <span>{t('notes.noneHint')}</span>}
        </div>
      ) : (
        <div className="notes-grid">
          {shown.map((n) => {
            const where = n.folder_id ? folderLabel(folders, n.folder_id) : '';
            return (
              <div key={n.id} className="note-card">
                <button
                  className="note-open"
                  onClick={() => open({ id: n.id, name: n.name, auto: n.auto })}
                >
                  <strong>
                    {n.starred && <Star size={13} fill="currentColor" />}
                    {noteTitle(n.name)}
                  </strong>
                  <span className="note-excerpt">
                    {preview(n).map((line, i) => (
                      <span
                        key={i}
                        className={line.task ? 'note-line task ' + line.task : 'note-line'}
                      >
                        {line.task && (
                          <span className="note-box" aria-hidden="true">
                            {line.task === 'done' && <Check size={10} strokeWidth={3.5} />}
                          </span>
                        )}
                        {line.text}
                      </span>
                    ))}
                  </span>
                </button>
                <div className="note-foot">
                  <span className="note-where" title={where || t('library.unfiled')}>
                    {where ? <FolderIcon size={12} /> : <Inbox size={12} />}
                    {where || t('library.unfiled')}
                  </span>
                  <small>{formatDate(n.modified * 1000, true)}</small>
                  {menu && (
                    <MoreMenu label={t('library.moreNamed', { name: n.name })} items={menu(n)} />
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </>
  );
}
