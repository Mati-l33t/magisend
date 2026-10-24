import { useEffect, useRef, useState, type ReactNode } from 'react';
import { copyText } from './clipboard';
import {
  ArrowUpRight,
  Check,
  Clock,
  Copy,
  Download,
  FolderUp,
  Link,
  Lock,
  Pause,
  Play,
  Plus,
  RotateCw,
  ShieldCheck,
  SlidersHorizontal,
  Upload,
  X,
} from 'lucide-react';
import { formatBytes, shareUrl, type Folder, type Me, type Share } from './api';
import { KeepFolderSelect } from './KeepFiles';
import { Select } from './Select';
import { DownloadLimitSelect, limitHint, limits, validityOptions } from './Downloads';
import { chosenFolder, droppedEntries } from './folderFiles';
import LocalThumb from './LocalThumb';
import { UploadSpeed } from './UploadSpeed';
import type { UploadStats } from './uploader';
import { formatDate, formatNumber, t, useLanguage } from './i18n';
import type { Look } from './look';

// Everything the Send page knows. The layouts below only arrange it; the
// upload itself stays in Workspace, so every look sends the same way.
export interface SendState {
  me: Me;
  files: File[];
  setFiles: (files: File[]) => void;
  select: (files: File[]) => void;
  busy: boolean;
  draft: Share | null;
  progress: number;
  stats: UploadStats | null;
  title: string;
  setTitle: (value: string) => void;
  description: string;
  setDescription: (value: string) => void;
  slug: string;
  setSlug: (value: string) => void;
  days: number;
  setDays: (value: number) => void;
  limit: number;
  setLimit: (value: number) => void;
  password: string;
  setPassword: (value: string) => void;
  notifyNew: boolean;
  setNotifyNew: (value: boolean) => void;
  keep: boolean;
  setKeep: (value: boolean) => void;
  keepFolder: string;
  setKeepFolder: (value: string) => void;
  keepFolders: Folder[];
  stripLocation: boolean;
  setStripLocation: (value: boolean) => void;
  canEmail: boolean;
  canNotify: boolean;
  send: () => void;
  pause: () => void;
  deleteDraft: () => void;
  recent: Share[];
  openShares: () => void;
}

export default function SendLayout({ look, s }: { look: Look; s: SendState }) {
  useLanguage();
  if (look === 'aurora') return <AuroraSend s={s} />;
  if (look === 'studio') return <StudioSend s={s} />;
  return <RefinedSend s={s} />;
}

/* ---------- Shared pieces ---------- */

function usePickers(s: SendState) {
  const files = useRef<HTMLInputElement>(null);
  const folder = useRef<HTMLInputElement>(null);
  // Not in React's typings: lets the picker choose a whole folder.
  useEffect(() => folder.current?.setAttribute('webkitdirectory', ''), []);
  const inputs = (
    <>
      <input
        ref={files}
        className="sr-only"
        type="file"
        aria-label={t('upload.choose')}
        tabIndex={-1}
        multiple
        disabled={s.busy}
        onChange={(e) => {
          s.select(Array.from(e.target.files || []));
          e.target.value = '';
        }}
      />
      <input
        ref={folder}
        className="sr-only"
        type="file"
        aria-label={t('upload.chooseFolder')}
        tabIndex={-1}
        disabled={s.busy || !!s.draft}
        onChange={(e) => {
          s.select(chosenFolder(e.target.files).files);
          e.target.value = '';
        }}
      />
    </>
  );
  return {
    inputs,
    chooseFiles: () => files.current?.click(),
    chooseFolder: () => folder.current?.click(),
  };
}

function useDrop(s: SendState) {
  const [dragging, setDragging] = useState(false);
  return {
    dragging,
    handlers: {
      onDragOver: (e: React.DragEvent) => {
        e.preventDefault();
        if (!s.busy) setDragging(true);
      },
      onDragLeave: (e: React.DragEvent) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false);
      },
      onDrop: (e: React.DragEvent) => {
        e.preventDefault();
        setDragging(false);
        const gather = droppedEntries(e.dataTransfer);
        void gather().then((g) => s.select(g.files));
      },
    },
  };
}

function total(s: SendState) {
  return s.files.reduce((n, f) => n + f.size, 0);
}

// "1 file · 4 KB" or "3 files · 2 MB".
export function filesText(count: number, size: number) {
  return (
    (count === 1 ? t('files.countOne') : t('files.count', { count: formatNumber(count) })) +
    ' · ' +
    formatBytes(size)
  );
}

function summary(s: SendState) {
  return s.files.length ? filesText(s.files.length, total(s)) : t('share.linkAfterUpload');
}

function limitsText(s: SendState) {
  return t('upload.limits', {
    file: formatBytes(s.me.policy.max_file),
    share: formatBytes(s.me.policy.max_share),
  });
}

function FileRows({ s }: { s: SendState }) {
  return (
    <ul className="send-rows">
      {s.files.map((f) => (
        <li key={f.name}>
          <LocalThumb file={f} />
          <div className="file-name">
            <strong>{f.name}</strong>
            <small>{formatBytes(f.size)}</small>
          </div>
          {!s.draft && (
            <button
              aria-label={t('files.remove', { name: f.name })}
              className="icon-button"
              disabled={s.busy}
              onClick={() => s.setFiles(s.files.filter((n) => n !== f))}
            >
              <X size={16} />
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

function Progress({ s }: { s: SendState }) {
  if (!s.draft) return null;
  return (
    <div className="upload-progress">
      <div>
        <span>{s.busy ? t('upload.uploading') : t('upload.saved')}</span>
        <strong className="mono">{formatNumber(s.progress / 100, { style: 'percent' })}</strong>
      </div>
      <progress aria-label={t('upload.uploading')} value={s.progress} max="100" />
      {s.busy && <UploadSpeed stats={s.stats} />}
      <small>{t('upload.draftExpiry')}</small>
      <button className="quiet" disabled={s.busy} onClick={s.deleteDraft}>
        {t('upload.deleteDraft')}
      </button>
    </div>
  );
}

function CreateButton({ s, className = 'primary full' }: { s: SendState; className?: string }) {
  if (s.busy)
    return (
      <button className={className.replace('primary', 'secondary')} onClick={s.pause}>
        <Pause size={17} />
        {t('upload.pause')}
      </button>
    );
  return (
    <button className={className} disabled={s.files.length === 0} onClick={s.send}>
      {s.draft ? <Play size={17} /> : <Link size={17} />}
      {s.draft ? t('upload.continue') : t('share.createLink')}
      {!s.draft && <ArrowUpRight size={17} />}
    </button>
  );
}

function Switch({
  checked,
  onChange,
  disabled,
  children,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      className="switch-row"
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="switch-label">{children}</span>
      <span className="switch" aria-hidden="true" />
    </button>
  );
}

function NameField({ s }: { s: SendState }) {
  return (
    <label>
      {t('share.title')}
      <input
        disabled={s.busy || !!s.draft}
        value={s.title}
        onChange={(e) => s.setTitle(e.target.value)}
        placeholder={t('share.titlePlaceholder')}
        maxLength={160}
      />
    </label>
  );
}

function PasswordInput({ s, label }: { s: SendState; label?: boolean }) {
  const input = (
    <input
      type="password"
      autoComplete="new-password"
      aria-label={label ? undefined : t('auth.password')}
      disabled={s.busy || !!s.draft}
      value={s.password}
      onChange={(e) => s.setPassword(e.target.value)}
      maxLength={72}
      placeholder={t('share.passwordPlaceholder')}
    />
  );
  if (!label) return input;
  return (
    <label>
      {t('auth.password')} <span className="muted">{t('share.optional')}</span>
      {input}
    </label>
  );
}

function NoteField({ s }: { s: SendState }) {
  return (
    <label>
      {t('share.description')} <span className="muted">{t('share.optional')}</span>
      <textarea
        rows={3}
        disabled={s.busy || !!s.draft}
        value={s.description}
        onChange={(e) => s.setDescription(e.target.value)}
        maxLength={1000}
        placeholder={t('share.descriptionPlaceholder')}
      />
    </label>
  );
}

function SlugField({ s }: { s: SendState }) {
  return (
    <div className="field-with-hint">
      <label>
        {t('share.customLink')} <span className="muted">{t('share.optional')}</span>
        <span className="prefixed">
          <span className="mono">{location.host}/s/</span>
          <input
            className="mono"
            disabled={s.busy || !!s.draft}
            value={s.slug}
            onChange={(e) => s.setSlug(e.target.value.toLowerCase())}
            maxLength={48}
            placeholder={t('share.customLinkPlaceholder')}
          />
        </span>
      </label>
      <small className="field-hint">{t('share.customLinkHint')}</small>
    </div>
  );
}

function NotifySwitch({ s }: { s: SendState }) {
  if (!s.canEmail) return null;
  return (
    <div>
      <Switch
        checked={s.notifyNew && s.canNotify}
        disabled={s.busy || !!s.draft || !s.canNotify}
        onChange={s.setNotifyNew}
      >
        {t('email.notifyOnDownload')}
      </Switch>
      {!s.canNotify && <small className="field-help">{t('email.addAddressFirst')}</small>}
    </div>
  );
}

function StripSwitch({ s }: { s: SendState }) {
  return (
    <div>
      <Switch
        checked={s.stripLocation}
        disabled={s.busy || !!s.draft}
        onChange={s.setStripLocation}
      >
        {t('share.stripLocation')}
      </Switch>
      <small className="field-help">{t('share.stripLocationHint')}</small>
    </div>
  );
}

function KeepSwitch({ s }: { s: SendState }) {
  const locked = s.busy || !!s.draft;
  return (
    <>
      <div>
        <Switch checked={s.keep} disabled={locked} onChange={s.setKeep}>
          {t('share.keep')}
        </Switch>
        {!s.keep && <small className="field-help">{t('share.keepOff')}</small>}
      </div>
      {s.keep && (
        <KeepFolderSelect
          folders={s.keepFolders}
          value={s.keepFolder}
          onChange={s.setKeepFolder}
          disabled={locked}
        />
      )}
    </>
  );
}

// Opens by itself when it already holds a value, so nothing set is hidden.
function Reveal({ label, open, children }: { label: string; open: boolean; children: ReactNode }) {
  const [shown, setShown] = useState(open);
  if (shown || open) return <>{children}</>;
  return (
    <button type="button" className="reveal-button" onClick={() => setShown(true)}>
      <Plus size={15} />
      {label}
    </button>
  );
}

function Trust() {
  return (
    <div className="send-trust">
      <span>
        <ShieldCheck size={15} />
        {t('share.onServer')}
      </span>
      <span>
        <Clock size={15} />
        {t('share.cleanup')}
      </span>
      <span>
        <RotateCw size={15} />
        {t('send.resumes')}
      </span>
    </div>
  );
}

// The last links you made, so the page is useful before anything is added.
function RecentLinks({ s, cards }: { s: SendState; cards?: boolean }) {
  const [copied, setCopied] = useState('');
  const now = Date.now() / 1000;
  const list = s.recent
    .filter((sh) => sh.published && !sh.mode && (sh.expires === 0 || sh.expires > now))
    .sort((a, b) => b.created - a.created)
    .slice(0, cards ? 3 : 4);
  async function copy(sh: Share) {
    try {
      await copyText(shareUrl(sh));
      setCopied(sh.id);
    } catch {
      setCopied('');
    }
  }
  return (
    <section className={'recent-links' + (cards ? ' as-cards' : ' panel')}>
      <header>
        <strong>{t('send.recent')}</strong>
        {list.length > 0 && (
          <button className="text-button" onClick={s.openShares}>
            {t('send.seeAll')}
          </button>
        )}
      </header>
      {list.length === 0 ? (
        <p className="recent-empty">{t('send.noRecent')}</p>
      ) : (
        <ul>
          {list.map((sh) => (
            <li key={sh.id}>
              <span className="recent-icon">
                <Link size={16} />
              </span>
              <span className="recent-text">
                <strong>{sh.title}</strong>
                <small>
                  {filesText(
                    sh.files.length,
                    sh.files.reduce((n, f) => n + f.size, 0),
                  )}
                  {' · '}
                  {sh.expires === 0 ? t('share.neverExpires') : formatDate(sh.expires * 1000)}
                </small>
              </span>
              <button
                className="icon-button"
                aria-label={copied === sh.id ? t('share.copied') : t('share.copy')}
                title={copied === sh.id ? t('share.copied') : t('share.copy')}
                onClick={() => void copy(sh)}
              >
                {copied === sh.id ? <Check size={16} /> : <Copy size={16} />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/* ---------- Refined: compact drop strip, files first, quick options ---------- */

function RefinedSend({ s }: { s: SendState }) {
  const pick = usePickers(s);
  const drop = useDrop(s);
  const [passwordOn, setPasswordOn] = useState(s.password !== '');
  const locked = s.busy || !!s.draft;
  const choices = validityOptions([s.me.policy.default_days]);
  return (
    <div className="send-refined">
      <div className="send-main">
        <section
          className={'panel send-files' + (s.files.length === 0 ? ' is-empty' : '')}
          {...drop.handlers}
        >
          <div className={'drop-strip' + (drop.dragging ? ' dragging' : '')}>
            <span className="drop-icon">
              <Upload size={22} />
            </span>
            <div className="drop-text">
              <strong>{s.draft ? t('upload.continueTitle') : t('send.dropTitle')}</strong>
              <small>
                {!s.draft && t('upload.pasteHint') + ' '}
                {limitsText(s)}
              </small>
            </div>
            <div className="drop-buttons">
              <button className="secondary small" disabled={s.busy} onClick={pick.chooseFiles}>
                {t('upload.choose')}
              </button>
              <button
                className="quiet small"
                disabled={locked}
                onClick={pick.chooseFolder}
                aria-label={t('upload.chooseFolder')}
                title={t('upload.chooseFolder')}
              >
                <FolderUp size={16} />
              </button>
            </div>
            {pick.inputs}
          </div>
          {s.files.length === 0 ? (
            <button className="send-empty" disabled={s.busy} onClick={pick.chooseFiles}>
              <span className="empty-stack" aria-hidden="true">
                <i />
                <i />
                <i />
              </span>
              {t('send.empty')}
            </button>
          ) : (
            <FileRows s={s} />
          )}
          <Progress s={s} />
          <footer className="send-files-foot">
            <span>{summary(s)}</span>
            <span>{t('send.resumes')}</span>
          </footer>
        </section>
        <RecentLinks s={s} />
      </div>
      <aside className="panel send-options">
        <h2>
          <Link size={17} />
          {t('share.yourLink')}
        </h2>
        <NameField s={s} />
        <div className="choice-field" role="radiogroup" aria-label={t('share.validity')}>
          <span className="field-label">{t('share.validity')}</span>
          <div className="choice-chips">
            {choices.map((option) => (
              <button
                key={option.value}
                type="button"
                role="radio"
                aria-checked={s.days === option.value}
                disabled={locked}
                onClick={() => s.setDays(option.value)}
              >
                {option.label}
              </button>
            ))}
          </div>
        </div>
        <div className="switch-group">
          <Switch
            checked={passwordOn}
            disabled={locked}
            onChange={(on) => {
              setPasswordOn(on);
              if (!on) s.setPassword('');
            }}
          >
            <Lock size={16} />
            {t('auth.password')}
          </Switch>
          {passwordOn && <PasswordInput s={s} />}
          <Switch
            checked={s.limit > 0}
            disabled={locked}
            onChange={(on) => s.setLimit(on ? limits[3] : 0)}
          >
            <Download size={16} />
            {t('downloads.limit')}
            {s.limit > 0 && <em>{limitHint(s.limit, s.keep)}</em>}
          </Switch>
          {s.limit > 0 && (
            <Select
              ariaLabel={t('downloads.limit')}
              value={s.limit}
              disabled={locked}
              onChange={s.setLimit}
              options={limits.map((n) => ({
                value: n,
                label: t('send.perFile', { count: formatNumber(n) }),
              }))}
            />
          )}
          <NotifySwitch s={s} />
          <KeepSwitch s={s} />
          <StripSwitch s={s} />
        </div>
        <Reveal label={t('send.addNote')} open={s.description !== ''}>
          <NoteField s={s} />
        </Reveal>
        <Reveal label={t('send.ownLink')} open={s.slug !== ''}>
          <SlugField s={s} />
        </Reveal>
        <CreateButton s={s} />
        <small className="button-note">{summary(s)}</small>
      </aside>
      <Trust />
    </div>
  );
}

/* ---------- Aurora: one focused card, options as a sentence ---------- */

function AuroraSend({ s }: { s: SendState }) {
  const pick = usePickers(s);
  const drop = useDrop(s);
  const [more, setMore] = useState(s.description !== '' || s.slug !== '');
  const [passwordOn, setPasswordOn] = useState(s.password !== '');
  const locked = s.busy || !!s.draft;
  const dayChoices = validityOptions([s.me.policy.default_days]).map((o) => ({
    value: o.value,
    label:
      o.value === 0
        ? t('send.forever')
        : o.value === 1
          ? t('send.forOneDay')
          : t('send.forDays', { count: formatNumber(o.value) }),
  }));
  const limitChoices = [0, ...limits].map((n) => ({
    value: n,
    label:
      n === 0
        ? t('send.unlimitedDownloads')
        : n === 1
          ? t('send.oneDownload')
          : t('send.downloadsPerFile', { count: formatNumber(n) }),
  }));
  return (
    <div className="send-aurora">
      <section
        className={'aurora-card' + (drop.dragging ? ' dragging' : '')}
        {...drop.handlers}
        aria-label={t('upload.new')}
      >
        {pick.inputs}
        <div className="aurora-files">
          {s.files.length === 0 ? (
            <button
              type="button"
              className="aurora-drop"
              disabled={s.busy}
              onClick={pick.chooseFiles}
            >
              <span className="drop-orb">
                <Upload size={30} />
              </span>
              <strong>{s.draft ? t('upload.continueTitle') : t('send.dropTitle')}</strong>
              <small>{limitsText(s)}</small>
            </button>
          ) : (
            <div className="file-chips">
              {s.files.map((f) => (
                <span className="file-chip" key={f.name}>
                  <LocalThumb file={f} />
                  <span className="file-chip-text">
                    <strong>{f.name}</strong>
                    <small>{formatBytes(f.size)}</small>
                  </span>
                  {!s.draft && (
                    <button
                      className="chip-remove"
                      aria-label={t('files.remove', { name: f.name })}
                      disabled={s.busy}
                      onClick={() => s.setFiles(s.files.filter((n) => n !== f))}
                    >
                      <X size={14} />
                    </button>
                  )}
                </span>
              ))}
              {!s.draft && (
                <button className="file-chip add" disabled={s.busy} onClick={pick.chooseFiles}>
                  <Plus size={15} />
                  {t('send.addMore')}
                </button>
              )}
            </div>
          )}
          {!s.draft && s.files.length === 0 && (
            <div className="aurora-alt">
              <button className="text-button" disabled={locked} onClick={pick.chooseFolder}>
                {t('upload.chooseFolder')}
              </button>
              <span>{t('upload.pasteHint')}</span>
            </div>
          )}
        </div>
        <div className="aurora-options">
          <input
            className="aurora-name"
            aria-label={t('share.title')}
            disabled={locked}
            value={s.title}
            onChange={(e) => s.setTitle(e.target.value)}
            placeholder={t('send.namePlaceholder')}
            maxLength={160}
          />
          <p className="sentence">
            {t('send.sentenceWorks')}{' '}
            <Select
              className="inline-select"
              ariaLabel={t('share.validity')}
              value={s.days}
              disabled={locked}
              onChange={s.setDays}
              options={dayChoices}
            />
            , {t('send.sentenceAllows')}{' '}
            <Select
              className="inline-select"
              ariaLabel={t('downloads.limit')}
              value={s.limit}
              disabled={locked}
              onChange={s.setLimit}
              options={limitChoices}
            />{' '}
            {t('send.sentenceAnd')}{' '}
            <Select
              className="inline-select"
              ariaLabel={t('auth.password')}
              value={passwordOn ? 1 : 0}
              disabled={locked}
              onChange={(on) => {
                setPasswordOn(on === 1);
                if (on === 0) s.setPassword('');
              }}
              options={[
                { value: 0, label: t('send.noPassword') },
                { value: 1, label: t('send.aPassword') },
              ]}
            />
            .
          </p>
          {passwordOn && <PasswordInput s={s} />}
          {more && (
            <div className="aurora-more">
              <NoteField s={s} />
              <SlugField s={s} />
              <NotifySwitch s={s} />
              <KeepSwitch s={s} />
              <StripSwitch s={s} />
            </div>
          )}
          <Progress s={s} />
          <div className="aurora-actions">
            <CreateButton s={s} className="primary aurora-go" />
            <button
              className="secondary"
              aria-expanded={more}
              onClick={() => setMore(!more)}
              disabled={locked && !more}
            >
              <SlidersHorizontal size={16} />
              {more ? t('send.fewer') : t('send.more')}
            </button>
          </div>
          <small className="button-note">{summary(s)}</small>
        </div>
      </section>
      <RecentLinks s={s} cards />
      <Trust />
    </div>
  );
}

/* ---------- Studio: a file table and an inspector ---------- */

function StudioSend({ s }: { s: SendState }) {
  const pick = usePickers(s);
  const drop = useDrop(s);
  const [passwordOn, setPasswordOn] = useState(s.password !== '');
  const locked = s.busy || !!s.draft;
  return (
    <div className="send-studio">
      <div className="send-main">
        <section
          className={'panel studio-files' + (drop.dragging ? ' dragging' : '')}
          {...drop.handlers}
        >
          <header className="studio-bar">
            <strong>{t('upload.new')}</strong>
            <span className="muted">{summary(s)}</span>
            <span className="studio-bar-actions">
              <button className="secondary small" disabled={locked} onClick={pick.chooseFolder}>
                <FolderUp size={15} />
                {t('upload.chooseFolder')}
              </button>
              <button className="primary small" disabled={s.busy} onClick={pick.chooseFiles}>
                <Plus size={15} />
                {t('upload.choose')}
              </button>
            </span>
            {pick.inputs}
          </header>
          {s.files.length === 0 ? (
            <div className="studio-empty">
              <Upload size={26} />
              <strong>{s.draft ? t('upload.continueTitle') : t('send.dropTitle')}</strong>
              <small>
                {t('upload.pasteHint')} {limitsText(s)}
              </small>
            </div>
          ) : (
            <table className="studio-table">
              <thead>
                <tr>
                  <th>{t('send.name')}</th>
                  <th>{t('send.size')}</th>
                  <th aria-label={t('common.actions')} />
                </tr>
              </thead>
              <tbody>
                {s.files.map((f) => (
                  <tr key={f.name}>
                    <td>
                      <span className="studio-name">
                        <LocalThumb file={f} />
                        <span>{f.name}</span>
                      </span>
                    </td>
                    <td className="mono">{formatBytes(f.size)}</td>
                    <td>
                      {!s.draft && (
                        <button
                          aria-label={t('files.remove', { name: f.name })}
                          className="icon-button"
                          disabled={s.busy}
                          onClick={() => s.setFiles(s.files.filter((n) => n !== f))}
                        >
                          <X size={15} />
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <Progress s={s} />
        </section>
        <RecentLinks s={s} />
      </div>
      <aside className="panel inspector">
        <h2>{t('share.yourLink')}</h2>
        <div className="inspector-row">
          <span>{t('share.title')}</span>
          <input
            aria-label={t('share.title')}
            disabled={locked}
            value={s.title}
            onChange={(e) => s.setTitle(e.target.value)}
            placeholder={t('share.titlePlaceholder')}
            maxLength={160}
          />
        </div>
        <div className="inspector-row">
          <span>{t('share.validity')}</span>
          <Select
            ariaLabel={t('share.validity')}
            value={s.days}
            disabled={locked}
            onChange={s.setDays}
            options={validityOptions([s.me.policy.default_days])}
          />
        </div>
        <div className="inspector-row">
          <span>{t('downloads.limit')}</span>
          <DownloadLimitSelect value={s.limit} onChange={s.setLimit} disabled={locked} />
        </div>
        <div className="inspector-row">
          <span>{t('auth.password')}</span>
          <Switch
            checked={passwordOn}
            disabled={locked}
            onChange={(on) => {
              setPasswordOn(on);
              if (!on) s.setPassword('');
            }}
          >
            {passwordOn ? t('send.on') : t('send.off')}
          </Switch>
        </div>
        {passwordOn && <PasswordInput s={s} />}
        <NotifySwitch s={s} />
        <KeepSwitch s={s} />
        <StripSwitch s={s} />
        <Reveal label={t('send.addNote')} open={s.description !== ''}>
          <NoteField s={s} />
        </Reveal>
        <Reveal label={t('send.ownLink')} open={s.slug !== ''}>
          <SlugField s={s} />
        </Reveal>
        <div className="inspector-foot">
          <CreateButton s={s} />
          <small className="button-note">
            <ShieldCheck size={14} /> {t('share.privacy')}
          </small>
        </div>
      </aside>
    </div>
  );
}
