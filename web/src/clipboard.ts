// Copies text, also on plain http (a LAN address) where the browser has no
// clipboard API.
export async function copyText(text: string) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.opacity = '0';
  // Everything outside an open modal dialog is inert and cannot be selected.
  const before = document.activeElement as HTMLElement | null;
  (document.querySelector('dialog[open]') ?? document.body).append(area);
  area.select();
  const ok = document.execCommand('copy');
  area.remove();
  before?.focus?.({ preventScroll: true });
  if (!ok) throw new Error('copy failed');
}
