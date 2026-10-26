import { useEffect } from 'react';

// Aurora's menu floats in the middle of the top bar. When it would touch the
// name on the left or the search and buttons on the right (a TV or laptop
// with scaling, a long user name, a longer language), the page is marked
// crowded: first the menu shows only its icons, then the search too.
function fit() {
  const root = document.documentElement;
  const menu = document.querySelector('.sidebar');
  const brand = document.querySelector('.topbar .brand');
  const actions = document.querySelector('.topbar .top-actions');
  const touches = () => {
    if (!menu || !brand || !actions) return false;
    const m = menu.getBoundingClientRect();
    return (
      m.left - 12 < brand.getBoundingClientRect().right ||
      m.right + 12 > actions.getBoundingClientRect().left
    );
  };
  if (root.dataset.look !== 'aurora' || innerWidth <= 1024) {
    delete root.dataset.crowded;
    return;
  }
  for (const level of ['', '1', '2']) {
    if (level) root.dataset.crowded = level;
    else delete root.dataset.crowded;
    if (!touches()) return;
  }
}

export function useAuroraFit() {
  useEffect(() => {
    let frame = 0;
    const later = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(fit);
    };
    later();
    addEventListener('resize', later);
    // The menu's own size changes with the language and the tabs shown, and
    // the look is a setting that can change while the page is open.
    const sizes = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(later);
    for (const el of document.querySelectorAll('.sidebar, .topbar .brand, .topbar .top-actions'))
      sizes?.observe(el);
    const looks = new MutationObserver(later);
    looks.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-look', 'lang'],
    });
    return () => {
      cancelAnimationFrame(frame);
      removeEventListener('resize', later);
      sizes?.disconnect();
      looks.disconnect();
      delete document.documentElement.dataset.crowded;
    };
  }, []);
}
