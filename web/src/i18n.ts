import { useState, useSyncExternalStore } from 'react';
type Catalog = Record<string, string>;
const modules = import.meta.glob('./locales/*.json', { eager: true, import: 'default' }) as Record<
  string,
  Catalog
>;
export const catalogs: Record<string, Catalog> = Object.fromEntries(
  Object.entries(modules).map(([path, data]) => [
    path
      .split('/')
      .pop()!
      .replace(/\.json$/, ''),
    data,
  ]),
);
export const languages = Object.keys(catalogs)
  .sort()
  .map((code) => ({ code, name: catalogs[code]['_meta.languageName'] }));
const supported = (code: string) => Object.hasOwn(catalogs, code);
export function resolveLanguage(
  browser?: readonly string[],
  single?: string,
  saved?: string | null,
) {
  if (saved && supported(saved)) return saved;
  for (const candidate of [...(browser || []), ...(single ? [single] : [])]) {
    const normalized = candidate.toLowerCase();
    if (supported(normalized)) return normalized;
    const base = normalized.split('-')[0];
    if (supported(base)) return base;
  }
  return 'en';
}
let language = 'en';
let choice = 'automatic';
const listeners = new Set<() => void>();
export function getLanguage() {
  return language;
}
export function getLanguageChoice() {
  return choice;
}
export function translate(
  key: string,
  values: Record<string, string | number> = {},
  locale = language,
  source = catalogs,
): string {
  const selected = source[locale];
  const text =
    selected && Object.hasOwn(selected, key)
      ? selected[key]
      : Object.hasOwn(catalogs.en, key)
        ? catalogs.en[key]
        : key;
  return text.replace(/\{(\w+)\}/g, (match, name) =>
    Object.hasOwn(values, name) ? String(values[name]) : match,
  );
}
export const t = translate;
export interface MessageDescriptor {
  kind: 'translation';
  key: string;
  values: Record<string, string | number | MessageDescriptor | Error>;
}
export type Message = string | MessageDescriptor | Error;
export function message(key: string, values: MessageDescriptor['values'] = {}): MessageDescriptor {
  return { kind: 'translation', key, values };
}
export class LocalizedError extends Error {
  readonly descriptor?: MessageDescriptor;
  constructor(value: string | MessageDescriptor) {
    super(typeof value === 'string' ? value : renderMessage(value));
    if (typeof value !== 'string') this.descriptor = value;
  }
}
export function renderMessage(value: Message): string {
  if (typeof value === 'string') return value;
  if (value instanceof Error)
    return value instanceof LocalizedError && value.descriptor
      ? renderMessage(value.descriptor)
      : value.message;
  return t(
    value.key,
    Object.fromEntries(
      Object.entries(value.values).map(([key, item]) => [
        key,
        typeof item === 'number' ? item : renderMessage(item),
      ]),
    ),
  );
}
export function useMessage(initial: Message = '') {
  useLanguage();
  const [value, setValue] = useState<Message>(initial);
  return [renderMessage(value), setValue] as const;
}
function updateDocument() {
  document.documentElement.lang = language;
  document.title = t('metadata.title');
  document
    .querySelector('meta[name="description"]')
    ?.setAttribute('content', t('metadata.description'));
  document.querySelector('meta[property="og:title"]')?.setAttribute('content', t('metadata.title'));
}
export function setLanguage(value: string, persist = true) {
  choice = value === 'automatic' || !supported(value) ? 'automatic' : value;
  language = resolveLanguage(navigator.languages, navigator.language, choice);
  if (persist)
    try {
      if (choice === 'automatic') localStorage.removeItem('magisend-language');
      else localStorage.setItem('magisend-language', choice);
    } catch {}
  updateDocument();
  listeners.forEach((listener) => listener());
}
export function initializeLanguage() {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem('magisend-language');
  } catch {
    setLanguage(choice, false);
    return;
  }
  setLanguage(saved || 'automatic', false);
}
export function useLanguage() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => language + '|' + choice,
    () => 'en|automatic',
  );
}
export function formatNumber(n: number, options?: Intl.NumberFormatOptions) {
  return new Intl.NumberFormat(language, options).format(n);
}
// A rough duration such as "40 s", "3 min" or "1 h 5 min".
export function formatDuration(seconds: number) {
  const unit = (n: number, name: string) =>
    new Intl.NumberFormat(language, { style: 'unit', unit: name, unitDisplay: 'short' }).format(n);
  if (seconds < 60) return unit(Math.max(1, Math.ceil(seconds)), 'second');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return unit(minutes, 'minute');
  const hours = Math.floor(minutes / 60);
  return minutes % 60
    ? unit(hours, 'hour') + ' ' + unit(minutes % 60, 'minute')
    : unit(hours, 'hour');
}
export function formatDate(n: number, time = false) {
  return time ? new Date(n).toLocaleString(language) : new Date(n).toLocaleDateString(language);
}
export function validateCredentialsMessage(username: string, password: string) {
  if (!/^[a-z0-9_.-]{3,40}$/.test(username)) return message('auth.usernameHint');
  const bytes = new TextEncoder().encode(password).byteLength;
  return bytes < 12 || bytes > 72 ? message('auth.passwordHint') : '';
}
export function validateCredentials(username: string, password: string) {
  return renderMessage(validateCredentialsMessage(username, password));
}
