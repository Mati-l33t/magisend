import { applySiteLook } from './look';

// The installation's own name, accent color and logo (Administration →
// Branding). Pages read the name from here; the color is applied to the
// document so every accent in the stylesheet follows it.
export interface Branding {
  name: string;
  color: string;
  logo: boolean;
  look?: string;
}

let current: Branding = { name: '', color: '', logo: false, look: '' };
let version = 0;

export function brandName() {
  return current.name || 'MagiSend';
}
export function brandLogo() {
  return current.logo ? '/api/branding/logo?v=' + version : '/logo.png?v=3';
}
export function customBrand() {
  return current.name !== '';
}

// Text on the accent color stays readable: dark ink on light colors.
function ink(hex: string) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.3 ? '#0a0a0a' : '#ffffff';
}

export function applyBranding(next: Branding | undefined) {
  current = next
    ? { name: next.name || '', color: next.color || '', logo: !!next.logo, look: next.look || '' }
    : current;
  if (next) applySiteLook(current.look);
  version++;
  const root = document.documentElement;
  if (/^#[0-9a-f]{6}$/i.test(current.color)) {
    root.dataset.brand = '';
    root.style.setProperty('--brand', current.color);
    root.style.setProperty('--brand-ink', ink(current.color));
  } else {
    delete root.dataset.brand;
    root.style.removeProperty('--brand');
    root.style.removeProperty('--brand-ink');
  }
}
