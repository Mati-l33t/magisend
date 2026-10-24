import { useSyncExternalStore } from 'react';

// The look is a per-browser choice of visual style. Classic is the original
// design; the others restyle the screens through looks.css and, where the
// layout differs, through look-aware components.
export const looks = ['classic', 'refined', 'aurora', 'studio'] as const;
export type Look = (typeof looks)[number];
// Used until the administrator or the person picks another look.
export const defaultLook: Look = 'aurora';

const key = 'magisend-look';
// The administrator's default (Administration → Branding), remembered so the
// next visit paints in it straight away.
const siteKey = 'magisend-site-look';
const listeners = new Set<() => void>();
let choice = stored(key);
let site = stored(siteKey);
let current: Look = choice || site || defaultLook;

function valid(value: unknown): Look | null {
  return looks.find((look) => look === value) || null;
}
function stored(name: string): Look | null {
  try {
    return valid(localStorage.getItem(name));
  } catch {
    return null;
  }
}
function save(name: string, value: Look | null) {
  try {
    if (value) localStorage.setItem(name, value);
    else localStorage.removeItem(name);
  } catch {}
}

export function getLook() {
  return current;
}

function render() {
  current = choice || site || defaultLook;
  const root = document.documentElement;
  if (current === 'classic') delete root.dataset.look;
  else root.dataset.look = current;
  listeners.forEach((listener) => listener());
}

// Without an argument, shows the current look (before the first render);
// with one, it is this browser's own choice and wins over the default.
export function applyLook(look?: Look) {
  if (look) {
    choice = look;
    save(key, look);
  }
  render();
}

// The installation's default, used until someone picks a look themselves.
export function applySiteLook(look: string | undefined) {
  const next = valid(look);
  if (next === site) return;
  site = next;
  save(siteKey, next);
  render();
}

export function useLook() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getLook,
    getLook,
  );
}
