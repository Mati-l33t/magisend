import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { History, ListChecks, LoaderCircle, Share2, Trash2, X } from 'lucide-react';
import { api, ApiError } from './api';
import { useFollow } from './follow';
import { getLanguage, t, useLanguage, useMessage } from './i18n';
import { toggleTask } from './Markdown';

const Markdown = lazy(() => import('./Markdown'));

export interface NoteRef {
  id: string;
  name: string;
  auto?: boolean;
}

export const noteTitle = (name: string) => name.replace(/\.(md|markdown)$/i, '');

// Writes and shows one note. Changes are saved a moment after typing stops
// and when the note is closed; a new note is created on its first save, so
// an editor opened and closed again leaves nothing behind.
export default function NoteEditor({
  note,
  folder = '',
  editable,
  close,
  changed,
  share,
  history,
  remove,
}: {
  note: NoteRef | null;
  folder?: string;
  editable: boolean;
  close: () => void;
  changed: () => void;
  share?: (id: string) => void;
  history?: (note: NoteRef) => void;
  remove?: (id: string) => void;
}) {
  useLanguage();
  const dialog = useRef<HTMLDialogElement>(null);
  const [id, setId] = useState(note?.id ?? '');
  const [name, setName] = useState(note?.name ?? '');
  const [title, setTitle] = useState(note && !note.auto ? noteTitle(note.name) : '');
  const [text, setText] = useState('');
  const [mode, setMode] = useState<'write' | 'read'>(note ? 'read' : 'write');
  const [loading, setLoading] = useState(!!note);
  // The note waits for its virus scan: read again once it is done.
  const [waiting, setWaiting] = useState(false);
  const [status, setStatus] = useState<'saved' | 'saving' | 'unsaved' | 'conflict'>('saved');
  const [error, setError] = useMessage();
  // The latest values, for saves that run after a render.
  const latest = useRef({ id, title, text, version: 0, dirty: false });
  const saving = useRef<Promise<void> | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (dialog.current?.showModal) dialog.current.showModal();
    else dialog.current?.setAttribute('open', '');
    if (!note) area.current?.focus();
  }, []);
  async function loadText() {
    if (!note) return;
    try {
      const state = await api<{ text: string; version: number }>(
        '/api/library/files/' + note.id + '/text',
      );
      setText(state.text);
      Object.assign(latest.current, { text: state.text, version: state.version });
      if (!state.text) setMode('write');
      setWaiting(false);
      setLoading(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 423) {
        setWaiting(true);
        return;
      }
      setWaiting(false);
      setError(e as Error);
      setLoading(false);
    }
  }
  useEffect(() => {
    void loadText();
  }, []);
  useFollow(waiting, loadText);

  async function save() {
    if (saving.current) await saving.current;
    const current = latest.current;
    if (!current.dirty || !editable) return;
    current.dirty = false;
    setStatus('saving');
    saving.current = (async () => {
      try {
        if (!current.id) {
          if (!current.text.trim() && !current.title.trim()) {
            setStatus('saved');
            return;
          }
          const created = await api<NoteRef & { version?: number }>('/api/library/notes', 'POST', {
            title: current.title,
            text: current.text,
            folder_id: folder,
            language: getLanguage(),
          });
          // Known before anything else is asked, so a failure next never
          // makes the note a second time.
          current.id = created.id;
          current.version =
            created.version ??
            (await api<{ version: number }>('/api/library/files/' + created.id + '/text')).version;
          setId(created.id);
          setName(created.name);
        } else {
          const result = await api<{ version: number; name: string }>(
            '/api/library/notes/' + current.id,
            'PUT',
            {
              version: current.version,
              title: current.title,
              text: current.text,
              language: getLanguage(),
            },
          );
          current.version = result.version;
          setName(result.name);
        }
        setStatus(current.dirty ? 'unsaved' : 'saved');
        changed();
      } catch (e) {
        current.dirty = true;
        if (e instanceof ApiError && e.status === 409) setStatus('conflict');
        else setStatus('unsaved');
        setError(e as Error);
      } finally {
        saving.current = null;
      }
    })();
    await saving.current;
  }
  function update(next: { title?: string; text?: string }) {
    // Nothing is changed before the note's text is known.
    if (note && !latest.current.version) return;
    if (next.title !== undefined) setTitle(next.title);
    if (next.text !== undefined) setText(next.text);
    Object.assign(latest.current, next, { dirty: true });
    setStatus('unsaved');
    setError('');
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void save(), 1200);
  }
  async function finish() {
    window.clearTimeout(timer.current);
    if (note && !latest.current.version) {
      close();
      return;
    }
    await save();
    if (latest.current.dirty) return;
    close();
  }
  function addChecklist() {
    const el = area.current;
    const at = el?.selectionStart ?? text.length;
    const before = text.slice(0, at);
    const prefix = before && !before.endsWith('\n') ? '\n- [ ] ' : '- [ ] ';
    update({ text: before + prefix + text.slice(at) });
    setMode('write');
    window.setTimeout(() => {
      el?.focus();
      el?.setSelectionRange(at + prefix.length, at + prefix.length);
    });
  }
  const statusText =
    status === 'saving'
      ? t('notes.saving')
      : status === 'unsaved'
        ? t('notes.unsaved')
        : status === 'conflict'
          ? t('notes.conflict')
          : id
            ? t('notes.saved')
            : '';
  return (
    <dialog
      ref={dialog}
      className="dialog note-editor"
      aria-label={name ? noteTitle(name) : t('notes.new')}
      onCancel={(e) => {
        e.preventDefault();
        void finish();
      }}
    >
      <div className="note-head">
        <input
          className="note-title"
          aria-label={t('notes.title')}
          value={title}
          maxLength={120}
          readOnly={!editable}
          placeholder={name && !title ? noteTitle(name) : t('notes.titlePlaceholder')}
          onChange={(e) => update({ title: e.target.value })}
        />
        <button
          className="icon-button"
          aria-label={t('common.close')}
          onClick={() => void finish()}
        >
          <X size={18} />
        </button>
      </div>
      <div className="note-tools">
        <div role="radiogroup" aria-label={t('notes.view')} className="segmented">
          {(['write', 'read'] as const).map((option) => (
            <label key={option} className={mode === option ? 'checked inherit' : ''}>
              <input
                type="radio"
                name="note-mode"
                checked={mode === option}
                onChange={() => setMode(option)}
              />
              {t(option === 'write' ? 'notes.write' : 'notes.read')}
            </label>
          ))}
        </div>
        {editable && (
          <button
            className="icon-button"
            aria-label={t('notes.checklist')}
            title={t('notes.checklist')}
            onClick={addChecklist}
          >
            <ListChecks size={18} />
          </button>
        )}
        {id && history && (
          <button
            className="icon-button"
            aria-label={t('library.history')}
            title={t('library.history')}
            onClick={() => void save().then(() => history({ id, name }))}
          >
            <History size={18} />
          </button>
        )}
        {id && share && (
          <button
            className="icon-button"
            aria-label={t('notes.share')}
            title={t('notes.share')}
            onClick={() => void save().then(() => share(id))}
          >
            <Share2 size={18} />
          </button>
        )}
        {id && remove && editable && (
          <button
            className="icon-button danger"
            aria-label={t('notes.delete')}
            title={t('notes.delete')}
            onClick={() => remove(id)}
          >
            <Trash2 size={18} />
          </button>
        )}
        <span className="note-status" role="status">
          {statusText}
        </span>
      </div>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {loading ? (
        <p role="status" className="muted">
          <LoaderCircle className="spin" size={16} />{' '}
          {waiting ? t('scan.pending') : t('preview.loading')}
        </p>
      ) : mode === 'write' && editable ? (
        <textarea
          ref={area}
          className="note-text"
          aria-label={t('notes.text')}
          value={text}
          placeholder={t('notes.textPlaceholder')}
          onChange={(e) => update({ text: e.target.value })}
        />
      ) : (
        <div className="note-read preview-markdown">
          {text.trim() ? (
            <Suspense fallback={<pre className="preview-text">{text}</pre>}>
              <Markdown
                text={text}
                onToggle={editable ? (line) => update({ text: toggleTask(text, line) }) : undefined}
              />
            </Suspense>
          ) : (
            <p className="muted">{t('notes.empty')}</p>
          )}
        </div>
      )}
    </dialog>
  );
}
