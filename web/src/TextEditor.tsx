import { useEffect, useState } from 'react';
import { api, ApiError } from './api';
import { Modal } from './Modal';
import { t, useLanguage, useMessage, formatDate } from './i18n';
interface State {
  text: string;
  version: number;
}
interface Version {
  version: number;
  size: number;
  name: string;
  created: number;
  actor: string;
}
export default function TextEditor({
  base,
  name,
  editable,
  close,
  changed,
}: {
  base: string;
  name: string;
  editable: boolean;
  close: () => void;
  changed: () => void;
}) {
  useLanguage();
  const [text, setText] = useState('');
  const [version, setVersion] = useState(0);
  const [history, setHistory] = useState<Version[]>([]);
  const [busy, setBusy] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [error, setError] = useMessage();
  async function load() {
    setBusy(true);
    setError('');
    try {
      const state = await api<State>(base + '/text');
      setText(state.text);
      setVersion(state.version);
      setConflict(false);
      if (editable) setHistory(await api<Version[]>(base + '/versions'));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, [base]);
  async function save(restore?: number) {
    setBusy(true);
    setError('');
    try {
      const state = await api<State>(
        base + (restore ? '/restore' : '/text'),
        restore ? 'POST' : 'PUT',
        restore ? { version, restore } : { version, text },
      );
      setText(state.text);
      setVersion(state.version);
      setHistory(await api<Version[]>(base + '/versions'));
      changed();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) setConflict(true);
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={name} close={close} className="editor-dialog">
      <p className="muted">{t('library.textSafety')}</p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      <label>
        {t('library.plainText')}
        <textarea
          className="text-editor mono"
          aria-label={t('library.plainText')}
          value={text}
          disabled={busy || !version}
          readOnly={!editable}
          onChange={(e) => setText(e.target.value)}
          spellCheck={false}
        />
      </label>
      <small>
        {t('library.version', { version })} · {new TextEncoder().encode(text).length} / 32768 B
      </small>
      {editable && (
        <div className="dialog-actions">
          <button
            className="secondary"
            disabled={busy}
            onClick={() => {
              if (!conflict || window.confirm(t('library.discard'))) void load();
            }}
          >
            {t('library.reloadText')}
          </button>
          <button
            className="primary"
            disabled={busy || !version || conflict || new TextEncoder().encode(text).length > 32768}
            onClick={() => void save()}
          >
            {t('library.saveText')}
          </button>
        </div>
      )}
      {editable && (
        <details className="version-history">
          <summary>{t('library.history')}</summary>
          <p>{t('library.restoreHint')}</p>
          {history.map((item) => (
            <div className="storage-detail" key={item.version}>
              <span>
                {t('library.version', { version: item.version })} ·{' '}
                {formatDate(item.created * 1000, true)} · {item.size} B
              </span>
              <button
                className="secondary"
                disabled={busy || conflict || item.version === version}
                onClick={() => void save(item.version)}
              >
                {t('library.restore')}
              </button>
            </div>
          ))}
        </details>
      )}
    </Modal>
  );
}
